package lifecycle

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultCheckInterval is how often to run lifecycle checks
	DefaultCheckInterval = 1 * time.Minute

	// DefaultHardMaxHours is the maximum session duration without override
	DefaultHardMaxHours = 12

	// DefaultOrphanGracePeriod is how long past reservation before marking as orphan
	DefaultOrphanGracePeriod = 15 * time.Minute
)

// SessionStore defines the interface for session persistence
type SessionStore interface {
	GetActiveSessions(ctx context.Context) ([]*models.Session, error)
	GetExpiredSessions(ctx context.Context) ([]*models.Session, error)
	GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error)
	Get(ctx context.Context, id string) (*models.Session, error)
	Update(ctx context.Context, session *models.Session) error
}

// SessionDestroyer handles session destruction
type SessionDestroyer interface {
	DestroySession(ctx context.Context, sessionID string) error
}

// EventHandler receives lifecycle events
type EventHandler interface {
	OnSessionExpired(session *models.Session)
	OnHardMaxReached(session *models.Session)
	OnOrphanDetected(session *models.Session)
}

// noopEventHandler is a default handler that does nothing
type noopEventHandler struct{}

func (n *noopEventHandler) OnSessionExpired(session *models.Session) {}
func (n *noopEventHandler) OnHardMaxReached(session *models.Session) {}
func (n *noopEventHandler) OnOrphanDetected(session *models.Session) {}

// Manager handles session lifecycle operations
type Manager struct {
	store     SessionStore
	destroyer SessionDestroyer
	handler   EventHandler
	logger    *slog.Logger

	// Configuration
	checkInterval     time.Duration
	hardMaxHours      int
	orphanGracePeriod time.Duration

	// For time mocking in tests
	now func() time.Time

	// Shutdown coordination
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// Metrics
	metrics *Metrics
}

// Metrics tracks lifecycle manager statistics
type Metrics struct {
	mu               sync.RWMutex
	ChecksRun        int64
	SessionsExpired  int64
	HardMaxEnforced  int64
	OrphansDetected  int64
	DestroySuccesses int64
	DestroyFailures  int64
}

// Option configures the lifecycle manager
type Option func(*Manager)

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(m *Manager) {
		m.logger = logger
	}
}

// WithCheckInterval sets how often to run lifecycle checks
func WithCheckInterval(d time.Duration) Option {
	return func(m *Manager) {
		m.checkInterval = d
	}
}

// WithHardMaxHours sets the maximum session duration
func WithHardMaxHours(hours int) Option {
	return func(m *Manager) {
		m.hardMaxHours = hours
	}
}

// WithOrphanGracePeriod sets how long past reservation before marking as orphan
func WithOrphanGracePeriod(d time.Duration) Option {
	return func(m *Manager) {
		m.orphanGracePeriod = d
	}
}

// WithEventHandler sets a custom event handler
func WithEventHandler(handler EventHandler) Option {
	return func(m *Manager) {
		m.handler = handler
	}
}

// WithTimeFunc sets a custom time function (for testing)
func WithTimeFunc(fn func() time.Time) Option {
	return func(m *Manager) {
		m.now = fn
	}
}

// New creates a new lifecycle manager
func New(store SessionStore, destroyer SessionDestroyer, opts ...Option) *Manager {
	m := &Manager{
		store:             store,
		destroyer:         destroyer,
		handler:           &noopEventHandler{},
		logger:            slog.Default(),
		checkInterval:     DefaultCheckInterval,
		hardMaxHours:      DefaultHardMaxHours,
		orphanGracePeriod: DefaultOrphanGracePeriod,
		now:               time.Now,
		stopCh:            make(chan struct{}),
		doneCh:            make(chan struct{}),
		metrics:           &Metrics{},
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Start begins the lifecycle check loop
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	m.mu.Unlock()

	m.logger.Info("lifecycle manager starting",
		slog.Duration("check_interval", m.checkInterval),
		slog.Int("hard_max_hours", m.hardMaxHours))

	go m.run(ctx)
	return nil
}

// Stop gracefully stops the lifecycle manager
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	m.logger.Info("lifecycle manager stopping")
	close(m.stopCh)
	<-m.doneCh

	m.mu.Lock()
	m.running = false
	m.mu.Unlock()

	m.logger.Info("lifecycle manager stopped")
}

// run is the main lifecycle check loop
func (m *Manager) run(ctx context.Context) {
	defer close(m.doneCh)

	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	// Run initial check
	m.runChecks(ctx)

	for {
		select {
		case <-ticker.C:
			m.runChecks(ctx)
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// runChecks executes all lifecycle checks
func (m *Manager) runChecks(ctx context.Context) {
	m.logger.Debug("running lifecycle checks")

	m.metrics.mu.Lock()
	m.metrics.ChecksRun++
	m.metrics.mu.Unlock()

	// Run checks in order of priority
	m.checkHardMax(ctx)
	m.checkReservationExpiry(ctx)
	m.checkOrphans(ctx)
}

// checkHardMax enforces the 12-hour maximum session duration
func (m *Manager) checkHardMax(ctx context.Context) {
	sessions, err := m.store.GetActiveSessions(ctx)
	if err != nil {
		m.logger.Error("failed to get active sessions for hard max check",
			slog.String("error", err.Error()))
		return
	}

	now := m.now()
	hardMaxDuration := time.Duration(m.hardMaxHours) * time.Hour

	for _, session := range sessions {
		// Skip sessions with override
		if session.HardMaxOverride {
			continue
		}

		// Skip non-running sessions
		if session.Status != models.StatusRunning {
			continue
		}

		// Check if session has exceeded hard max
		if now.Sub(session.CreatedAt) > hardMaxDuration {
			m.logger.Warn("session exceeded hard max duration",
				slog.String("session_id", session.ID),
				slog.Duration("age", now.Sub(session.CreatedAt)),
				slog.Duration("hard_max", hardMaxDuration))

			m.metrics.mu.Lock()
			m.metrics.HardMaxEnforced++
			m.metrics.mu.Unlock()

			// Record audit log and metrics
			logging.Audit(ctx, "hard_max_enforced",
				"session_id", session.ID,
				"consumer_id", session.ConsumerID,
				"provider", session.Provider,
				"age_hours", now.Sub(session.CreatedAt).Hours(),
				"hard_max_hours", m.hardMaxHours)
			metrics.RecordHardMaxEnforced()
			metrics.RecordSessionDestroyed(session.Provider, "hard_max")

			m.handler.OnHardMaxReached(session)
			m.destroySession(ctx, session, "hard max duration exceeded")
		}
	}
}

// checkReservationExpiry handles sessions past their reservation time
func (m *Manager) checkReservationExpiry(ctx context.Context) {
	sessions, err := m.store.GetExpiredSessions(ctx)
	if err != nil {
		m.logger.Error("failed to get expired sessions",
			slog.String("error", err.Error()))
		return
	}

	for _, session := range sessions {
		m.logger.Info("session reservation expired",
			slog.String("session_id", session.ID),
			slog.Time("expires_at", session.ExpiresAt))

		m.metrics.mu.Lock()
		m.metrics.SessionsExpired++
		m.metrics.mu.Unlock()

		// Record audit log and metrics
		logging.Audit(ctx, "session_expired",
			"session_id", session.ID,
			"consumer_id", session.ConsumerID,
			"provider", session.Provider,
			"expires_at", session.ExpiresAt)
		metrics.RecordSessionDestroyed(session.Provider, "expired")

		m.handler.OnSessionExpired(session)
		m.destroySession(ctx, session, "reservation expired")
	}
}

// checkOrphans detects sessions running past reservation without extension
func (m *Manager) checkOrphans(ctx context.Context) {
	sessions, err := m.store.GetSessionsByStatus(ctx, models.StatusRunning)
	if err != nil {
		m.logger.Error("failed to get running sessions for orphan check",
			slog.String("error", err.Error()))
		return
	}

	now := m.now()

	for _, session := range sessions {
		// Check if past reservation + grace period
		orphanThreshold := session.ExpiresAt.Add(m.orphanGracePeriod)
		if now.After(orphanThreshold) {
			m.logger.Warn("orphan session detected",
				slog.String("session_id", session.ID),
				slog.Time("expires_at", session.ExpiresAt),
				slog.Duration("grace_period", m.orphanGracePeriod))

			m.metrics.mu.Lock()
			m.metrics.OrphansDetected++
			m.metrics.mu.Unlock()

			m.handler.OnOrphanDetected(session)
			// Note: We don't auto-destroy orphans here - that's handled by reconciliation
			// This is just for alerting. The actual orphan detection compares provider state.
		}
	}
}

// destroySession attempts to destroy a session
func (m *Manager) destroySession(ctx context.Context, session *models.Session, reason string) {
	m.logger.Info("destroying session",
		slog.String("session_id", session.ID),
		slog.String("reason", reason))

	if err := m.destroyer.DestroySession(ctx, session.ID); err != nil {
		m.logger.Error("failed to destroy session",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))

		m.metrics.mu.Lock()
		m.metrics.DestroyFailures++
		m.metrics.mu.Unlock()

		// Update session with error
		session.Error = reason + ": destroy failed: " + err.Error()
		if updateErr := m.store.Update(ctx, session); updateErr != nil {
			m.logger.Error("failed to update session error",
				slog.String("session_id", session.ID),
				slog.String("error", updateErr.Error()))
		}
		return
	}

	m.metrics.mu.Lock()
	m.metrics.DestroySuccesses++
	m.metrics.mu.Unlock()
}

// SignalDone signals that a session has completed its work
func (m *Manager) SignalDone(ctx context.Context, sessionID string) error {
	session, err := m.store.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	if session.IsTerminal() {
		return nil // Already done
	}

	m.logger.Info("session signaled done",
		slog.String("session_id", sessionID))

	return m.destroyer.DestroySession(ctx, sessionID)
}

// ExtendSession extends a session's reservation
func (m *Manager) ExtendSession(ctx context.Context, sessionID string, additionalHours int) error {
	session, err := m.store.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	if session.IsTerminal() {
		return &SessionTerminalError{ID: sessionID, Status: session.Status}
	}

	// Extend expiration
	session.ExpiresAt = session.ExpiresAt.Add(time.Duration(additionalHours) * time.Hour)
	session.ReservationHrs += additionalHours

	m.logger.Info("session extended",
		slog.String("session_id", sessionID),
		slog.Int("additional_hours", additionalHours),
		slog.Time("new_expires_at", session.ExpiresAt))

	return m.store.Update(ctx, session)
}

// SetHardMaxOverride enables or disables hard max override for a session
func (m *Manager) SetHardMaxOverride(ctx context.Context, sessionID string, override bool) error {
	session, err := m.store.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	if session.IsTerminal() {
		return &SessionTerminalError{ID: sessionID, Status: session.Status}
	}

	session.HardMaxOverride = override

	m.logger.Info("session hard max override changed",
		slog.String("session_id", sessionID),
		slog.Bool("override", override))

	return m.store.Update(ctx, session)
}

// GetMetrics returns current lifecycle metrics
func (m *Manager) GetMetrics() Metrics {
	m.metrics.mu.RLock()
	defer m.metrics.mu.RUnlock()

	return Metrics{
		ChecksRun:        m.metrics.ChecksRun,
		SessionsExpired:  m.metrics.SessionsExpired,
		HardMaxEnforced:  m.metrics.HardMaxEnforced,
		OrphansDetected:  m.metrics.OrphansDetected,
		DestroySuccesses: m.metrics.DestroySuccesses,
		DestroyFailures:  m.metrics.DestroyFailures,
	}
}

// IsRunning returns whether the manager is currently running
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}
