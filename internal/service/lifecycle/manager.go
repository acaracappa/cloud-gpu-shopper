package lifecycle

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/ssh"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// DefaultCheckInterval is how often to run lifecycle checks
	DefaultCheckInterval = 1 * time.Minute

	// DefaultHardMaxHours is the maximum session duration without override
	DefaultHardMaxHours = 12

	// DefaultOrphanGracePeriod is how long past reservation before marking as orphan
	DefaultOrphanGracePeriod = 15 * time.Minute

	// DefaultSSHHealthCheckInterval is how often to run SSH health checks
	DefaultSSHHealthCheckInterval = 2 * time.Minute

	// DefaultStuckSessionTimeout is how long a session can be in a transitional state
	// (stopping, provisioning) before being marked as failed
	// Bug #103 fix: Prevent sessions from getting stuck indefinitely
	DefaultStuckSessionTimeout = 10 * time.Minute
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
	checkInterval       time.Duration
	hardMaxHours        int
	orphanGracePeriod   time.Duration
	stuckSessionTimeout time.Duration // Bug #103 fix: timeout for stuck sessions

	// SSH health check configuration (optional)
	sshExecutor            *ssh.Executor
	sshHealthCheckEnabled  bool
	sshHealthCheckInterval time.Duration
	lastSSHHealthCheckMu   sync.Mutex // Bug #17 fix: Protects lastSSHHealthCheck
	lastSSHHealthCheck     time.Time

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
	mu                   sync.RWMutex
	ChecksRun            int64
	SessionsExpired      int64
	HardMaxEnforced      int64
	OrphansDetected      int64
	DestroySuccesses     int64
	DestroyFailures      int64
	SSHHealthChecksRun   int64
	SSHHealthChecksFailed int64
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

// WithStuckSessionTimeout sets how long a session can be in a transitional state
func WithStuckSessionTimeout(d time.Duration) Option {
	return func(m *Manager) {
		m.stuckSessionTimeout = d
	}
}

// WithSSHExecutor sets the SSH executor for health checks
func WithSSHExecutor(executor *ssh.Executor) Option {
	return func(m *Manager) {
		m.sshExecutor = executor
	}
}

// WithSSHHealthCheck enables SSH-based health checking
func WithSSHHealthCheck(enabled bool) Option {
	return func(m *Manager) {
		m.sshHealthCheckEnabled = enabled
	}
}

// WithSSHHealthCheckInterval sets how often to run SSH health checks
func WithSSHHealthCheckInterval(d time.Duration) Option {
	return func(m *Manager) {
		m.sshHealthCheckInterval = d
	}
}

// New creates a new lifecycle manager
func New(store SessionStore, destroyer SessionDestroyer, opts ...Option) *Manager {
	m := &Manager{
		store:                  store,
		destroyer:              destroyer,
		handler:                &noopEventHandler{},
		logger:                 slog.Default(),
		checkInterval:          DefaultCheckInterval,
		hardMaxHours:           DefaultHardMaxHours,
		orphanGracePeriod:      DefaultOrphanGracePeriod,
		stuckSessionTimeout:    DefaultStuckSessionTimeout,
		sshHealthCheckInterval: DefaultSSHHealthCheckInterval,
		now:                    time.Now,
		stopCh:                 make(chan struct{}),
		doneCh:                 make(chan struct{}),
		metrics:                &Metrics{},
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
		slog.Int("hard_max_hours", m.hardMaxHours),
		slog.Bool("ssh_health_check_enabled", m.sshHealthCheckEnabled),
		slog.Duration("ssh_health_check_interval", m.sshHealthCheckInterval))

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
	// Capture channel references while holding lock to prevent race with Start()
	stopCh := m.stopCh
	doneCh := m.doneCh
	m.mu.Unlock()

	m.logger.Info("lifecycle manager stopping")
	close(stopCh)
	<-doneCh
	// Note: running flag is set to false by the run() goroutine before closing doneCh

	m.logger.Info("lifecycle manager stopped")
}

// run is the main lifecycle check loop
func (m *Manager) run(ctx context.Context) {
	defer func() {
		// Update running state when loop exits (whether via Stop() or context cancellation)
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
		close(m.doneCh)
	}()

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
	m.checkStuckSessions(ctx) // Bug #103 fix: Check for stuck sessions

	// Run SSH health check if enabled and interval has passed
	// Bug #17 fix: Protect lastSSHHealthCheck with mutex
	if m.sshHealthCheckEnabled && m.sshExecutor != nil {
		now := m.now()
		m.lastSSHHealthCheckMu.Lock()
		shouldRun := now.Sub(m.lastSSHHealthCheck) >= m.sshHealthCheckInterval
		if shouldRun {
			m.lastSSHHealthCheck = now
		}
		m.lastSSHHealthCheckMu.Unlock()

		if shouldRun {
			m.checkSSHHealth(ctx)
		}
	}
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

// checkStuckSessions handles sessions stuck in transitional states (stopping, provisioning)
// Bug #103 fix: Prevents sessions from getting stuck indefinitely
func (m *Manager) checkStuckSessions(ctx context.Context) {
	// Get sessions in transitional states
	stuckSessions, err := m.store.GetSessionsByStatus(ctx, models.StatusStopping, models.StatusProvisioning)
	if err != nil {
		m.logger.Error("failed to get sessions for stuck check",
			slog.String("error", err.Error()))
		return
	}

	now := m.now()

	for _, session := range stuckSessions {
		// Check if session has been stuck for too long
		stuckDuration := now.Sub(session.CreatedAt)

		// For stopping sessions, check from when they started (use CreatedAt as approximation)
		// In a more sophisticated implementation, we'd track when the session entered stopping state
		if stuckDuration > m.stuckSessionTimeout {
			m.logger.Warn("session stuck in transitional state",
				slog.String("session_id", session.ID),
				slog.String("status", string(session.Status)),
				slog.Duration("stuck_duration", stuckDuration),
				slog.Duration("timeout", m.stuckSessionTimeout))

			// Mark session as failed
			oldStatus := session.Status
			session.Status = models.StatusFailed
			// Bug fix: Use oldStatus (not session.Status which is now "failed") for error message
			if oldStatus == models.StatusStopping {
				session.Error = "Session stuck in stopping state - manual cleanup may be required"
			} else {
				session.Error = "Session stuck in provisioning state - provisioning timeout"
			}
			session.StoppedAt = now

			if err := m.store.Update(ctx, session); err != nil {
				m.logger.Error("failed to update stuck session",
					slog.String("session_id", session.ID),
					slog.String("error", err.Error()))
				continue
			}

			// Record audit log and metrics
			logging.Audit(ctx, "stuck_session_failed",
				"session_id", session.ID,
				"consumer_id", session.ConsumerID,
				"provider", session.Provider,
				"old_status", string(oldStatus),
				"stuck_duration_minutes", stuckDuration.Minutes())
			metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusFailed))
		}
	}
}

// checkSSHHealth performs SSH-based health checks on running sessions.
// Note: This is a placeholder implementation. Full SSH health checks require
// the session's private key, which is NOT stored in the database for security.
// In production, this would need the private key passed from the client or
// stored in a secure key management system.
func (m *Manager) checkSSHHealth(ctx context.Context) {
	m.logger.Debug("running SSH health checks")

	sessions, err := m.store.GetSessionsByStatus(ctx, models.StatusRunning)
	if err != nil {
		m.logger.Error("failed to get running sessions for SSH health check",
			slog.String("error", err.Error()))
		return
	}

	m.metrics.mu.Lock()
	m.metrics.SSHHealthChecksRun++
	m.metrics.mu.Unlock()

	for _, session := range sessions {
		// Skip sessions without SSH credentials
		if session.SSHHost == "" || session.SSHPort == 0 {
			continue
		}

		// Note: We cannot perform actual SSH health checks because the private key
		// is not stored in the database (security pattern). The private key is only
		// returned once at session creation and must be managed by the client.
		//
		// This is intentional: storing SSH private keys in the database would be
		// a security risk. Instead, clients should:
		// 1. Store their private keys securely
		// 2. Use client-side health monitoring
		// 3. Call the /api/v1/sessions/{id}/done endpoint when done
		//
		// For now, we log when health checks would run so operators can see
		// which sessions are being monitored.
		m.logger.Debug("SSH health check would run (private key not available)",
			slog.String("session_id", session.ID),
			slog.String("ssh_host", session.SSHHost),
			slog.Int("ssh_port", session.SSHPort),
			slog.String("ssh_user", session.SSHUser))

		// In a future implementation with a key management system, we could:
		// conn, err := m.sshExecutor.Connect(ctx, session.SSHHost, session.SSHPort, session.SSHUser, privateKey)
		// if err != nil {
		//     m.logger.Warn("SSH health check failed", ...)
		//     m.metrics.mu.Lock()
		//     m.metrics.SSHHealthChecksFailed++
		//     m.metrics.mu.Unlock()
		// }
		// defer conn.Close()
		// if err := m.sshExecutor.CheckHealth(ctx, conn); err != nil { ... }
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

	// Bug #70 fix: Return error/conflict when session is already terminal
	// Don't return success for stopped sessions
	if session.IsTerminal() {
		return &SessionTerminalError{ID: sessionID, Status: session.Status}
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

	// Bug #71 fix: Also reject sessions in "stopping" state
	if session.IsTerminal() || session.Status == models.StatusStopping {
		return &SessionTerminalError{ID: sessionID, Status: session.Status}
	}

	// Bug #7 fix: Check cumulative duration doesn't exceed hard max
	// Calculate total duration from creation to new expiration
	now := m.now()
	totalDuration := now.Sub(session.CreatedAt) + time.Duration(additionalHours)*time.Hour
	hardMaxDuration := time.Duration(m.hardMaxHours) * time.Hour

	if !session.HardMaxOverride && totalDuration > hardMaxDuration {
		return &HardMaxExceededError{
			SessionID:       sessionID,
			CurrentDuration: now.Sub(session.CreatedAt),
			RequestedHours:  additionalHours,
			HardMaxHours:    m.hardMaxHours,
		}
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
		ChecksRun:             m.metrics.ChecksRun,
		SessionsExpired:       m.metrics.SessionsExpired,
		HardMaxEnforced:       m.metrics.HardMaxEnforced,
		OrphansDetected:       m.metrics.OrphansDetected,
		DestroySuccesses:      m.metrics.DestroySuccesses,
		DestroyFailures:       m.metrics.DestroyFailures,
		SSHHealthChecksRun:    m.metrics.SSHHealthChecksRun,
		SSHHealthChecksFailed: m.metrics.SSHHealthChecksFailed,
	}
}

// IsRunning returns whether the manager is currently running
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}
