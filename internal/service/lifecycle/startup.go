package lifecycle

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultStartupSweepTimeout is the default timeout for startup sweep
	DefaultStartupSweepTimeout = 2 * time.Minute

	// DefaultShutdownTimeout is the default timeout for graceful shutdown
	DefaultShutdownTimeout = 60 * time.Second

	// MaxParallelDestroys is the maximum number of concurrent destroy operations
	MaxParallelDestroys = 5
)

// StartupStore defines the interface for session persistence needed by startup manager
type StartupStore interface {
	GetActiveSessions(ctx context.Context) ([]*models.Session, error)
}

// InstanceDestroyer handles instance destruction at the provider level
type InstanceDestroyer interface {
	Get(name string) (provider.Provider, error)
}

// StartupShutdownManager handles startup sweep and graceful shutdown operations
type StartupShutdownManager struct {
	store      StartupStore
	reconciler *Reconciler
	providers  InstanceDestroyer
	logger     *slog.Logger

	// Configuration
	startupSweepTimeout time.Duration
	shutdownTimeout     time.Duration

	// State
	sweepComplete atomic.Bool

	// Metrics
	metrics *StartupMetrics
}

// StartupMetrics tracks startup/shutdown statistics
type StartupMetrics struct {
	mu                  sync.RWMutex
	StartupSweepRun     bool
	StartupSweepSuccess bool
	StartupSweepTime    time.Duration
	ShutdownRun         bool
	ShutdownSuccess     bool
	ShutdownTime        time.Duration
	SessionsDestroyed   int64
	DestroyFailures     int64
}

// StartupOption configures the startup manager
type StartupOption func(*StartupShutdownManager)

// WithStartupLogger sets a custom logger for the startup manager
func WithStartupLogger(logger *slog.Logger) StartupOption {
	return func(m *StartupShutdownManager) {
		m.logger = logger
	}
}

// WithStartupSweepTimeout sets the timeout for the startup sweep
func WithStartupSweepTimeout(d time.Duration) StartupOption {
	return func(m *StartupShutdownManager) {
		m.startupSweepTimeout = d
	}
}

// WithShutdownTimeout sets the timeout for graceful shutdown
func WithShutdownTimeout(d time.Duration) StartupOption {
	return func(m *StartupShutdownManager) {
		m.shutdownTimeout = d
	}
}

// NewStartupShutdownManager creates a new startup/shutdown manager
func NewStartupShutdownManager(
	store StartupStore,
	reconciler *Reconciler,
	providers InstanceDestroyer,
	opts ...StartupOption,
) *StartupShutdownManager {
	m := &StartupShutdownManager{
		store:               store,
		reconciler:          reconciler,
		providers:           providers,
		logger:              slog.Default(),
		startupSweepTimeout: DefaultStartupSweepTimeout,
		shutdownTimeout:     DefaultShutdownTimeout,
		metrics:             &StartupMetrics{},
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// RunStartupSweep runs a single reconciliation pass to clean up orphans at startup.
// This method blocks until the sweep is complete or the context is cancelled.
func (m *StartupShutdownManager) RunStartupSweep(ctx context.Context) error {
	m.logger.Info("starting startup sweep",
		slog.Duration("timeout", m.startupSweepTimeout))

	start := time.Now()
	m.metrics.mu.Lock()
	m.metrics.StartupSweepRun = true
	m.metrics.mu.Unlock()

	// Create a context with timeout
	sweepCtx, cancel := context.WithTimeout(ctx, m.startupSweepTimeout)
	defer cancel()

	// Record audit log for startup sweep
	logging.Audit(sweepCtx, "startup_sweep_started",
		"timeout", m.startupSweepTimeout.String())

	// First, recover any stuck sessions from a previous crash
	if err := m.reconciler.RecoverStuckSessions(sweepCtx); err != nil {
		m.logger.Error("failed to recover stuck sessions during startup sweep",
			slog.String("error", err.Error()))
		// Continue with reconciliation even if stuck session recovery fails
	}

	// Run reconciliation to find and clean up orphans
	m.reconciler.RunReconciliation(sweepCtx)

	elapsed := time.Since(start)
	m.metrics.mu.Lock()
	m.metrics.StartupSweepSuccess = true
	m.metrics.StartupSweepTime = elapsed
	m.metrics.mu.Unlock()

	// Mark sweep as complete
	m.sweepComplete.Store(true)

	// Record audit log for startup sweep completion
	logging.Audit(sweepCtx, "startup_sweep_completed",
		"duration", elapsed.String())

	m.logger.Info("startup sweep completed",
		slog.Duration("duration", elapsed))

	return nil
}

// GracefulShutdown destroys all active sessions before shutdown.
// This method blocks until all sessions are destroyed or the context is cancelled.
func (m *StartupShutdownManager) GracefulShutdown(ctx context.Context) error {
	m.logger.Info("starting graceful shutdown",
		slog.Duration("timeout", m.shutdownTimeout))

	start := time.Now()
	m.metrics.mu.Lock()
	m.metrics.ShutdownRun = true
	m.metrics.mu.Unlock()

	// Create a context with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, m.shutdownTimeout)
	defer cancel()

	// Record audit log for shutdown
	logging.Audit(shutdownCtx, "graceful_shutdown_started",
		"timeout", m.shutdownTimeout.String())

	// Get all active sessions
	sessions, err := m.store.GetActiveSessions(shutdownCtx)
	if err != nil {
		m.logger.Error("failed to get active sessions for shutdown",
			slog.String("error", err.Error()))
		return err
	}

	if len(sessions) == 0 {
		m.logger.Info("no active sessions to destroy during shutdown")
		m.metrics.mu.Lock()
		m.metrics.ShutdownSuccess = true
		m.metrics.ShutdownTime = time.Since(start)
		m.metrics.mu.Unlock()
		return nil
	}

	m.logger.Info("destroying active sessions during shutdown",
		slog.Int("count", len(sessions)))

	// Destroy sessions concurrently with a semaphore to limit parallelism
	var wg sync.WaitGroup
	sem := make(chan struct{}, MaxParallelDestroys)
	var destroyedCount, failedCount atomic.Int64

	for _, session := range sessions {
		wg.Add(1)
		go func(s *models.Session) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-shutdownCtx.Done():
				m.logger.Warn("shutdown context cancelled, skipping session destroy",
					slog.String("session_id", s.ID))
				failedCount.Add(1)
				return
			}

			if err := m.destroySession(shutdownCtx, s); err != nil {
				m.logger.Error("failed to destroy session during shutdown",
					slog.String("session_id", s.ID),
					slog.String("provider", s.Provider),
					slog.String("error", err.Error()))
				failedCount.Add(1)
			} else {
				m.logger.Info("destroyed session during shutdown",
					slog.String("session_id", s.ID),
					slog.String("provider", s.Provider))
				destroyedCount.Add(1)
			}
		}(session)
	}

	// Wait for all destroys to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All destroys completed
	case <-shutdownCtx.Done():
		m.logger.Warn("shutdown context timed out, some sessions may not be destroyed")
	}

	elapsed := time.Since(start)
	destroyed := destroyedCount.Load()
	failed := failedCount.Load()

	m.metrics.mu.Lock()
	m.metrics.ShutdownSuccess = failed == 0
	m.metrics.ShutdownTime = elapsed
	m.metrics.SessionsDestroyed = destroyed
	m.metrics.DestroyFailures = failed
	m.metrics.mu.Unlock()

	// Record audit log for shutdown completion
	logging.Audit(shutdownCtx, "graceful_shutdown_completed",
		"duration", elapsed.String(),
		"sessions_destroyed", destroyed,
		"destroy_failures", failed)

	m.logger.Info("graceful shutdown completed",
		slog.Duration("duration", elapsed),
		slog.Int64("sessions_destroyed", destroyed),
		slog.Int64("destroy_failures", failed))

	if failed > 0 {
		return &ShutdownError{
			TotalSessions:     len(sessions),
			DestroyedSessions: int(destroyed),
			FailedSessions:    int(failed),
		}
	}

	return nil
}

// destroySession destroys a single session using the provider
func (m *StartupShutdownManager) destroySession(ctx context.Context, session *models.Session) error {
	if session.ProviderID == "" {
		m.logger.Warn("session has no provider ID, cannot destroy instance",
			slog.String("session_id", session.ID))
		return nil // No instance to destroy
	}

	prov, err := m.providers.Get(session.Provider)
	if err != nil {
		return err
	}

	return prov.DestroyInstance(ctx, session.ProviderID)
}

// IsSweepComplete returns whether the startup sweep has completed
func (m *StartupShutdownManager) IsSweepComplete() bool {
	return m.sweepComplete.Load()
}

// GetMetrics returns current startup/shutdown metrics
func (m *StartupShutdownManager) GetMetrics() StartupMetrics {
	m.metrics.mu.RLock()
	defer m.metrics.mu.RUnlock()

	return StartupMetrics{
		StartupSweepRun:     m.metrics.StartupSweepRun,
		StartupSweepSuccess: m.metrics.StartupSweepSuccess,
		StartupSweepTime:    m.metrics.StartupSweepTime,
		ShutdownRun:         m.metrics.ShutdownRun,
		ShutdownSuccess:     m.metrics.ShutdownSuccess,
		ShutdownTime:        m.metrics.ShutdownTime,
		SessionsDestroyed:   m.metrics.SessionsDestroyed,
		DestroyFailures:     m.metrics.DestroyFailures,
	}
}

// ShutdownError indicates that some sessions could not be destroyed during shutdown
type ShutdownError struct {
	TotalSessions     int
	DestroyedSessions int
	FailedSessions    int
}

func (e *ShutdownError) Error() string {
	return "graceful shutdown incomplete: failed to destroy some sessions"
}
