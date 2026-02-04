package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSessionStore implements SessionStore for testing
type mockSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*models.Session
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{
		sessions: make(map[string]*models.Session),
	}
}

func (m *mockSessionStore) add(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

func (m *mockSessionStore) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*models.Session
	for _, s := range m.sessions {
		if s.Status == models.StatusPending ||
			s.Status == models.StatusProvisioning ||
			s.Status == models.StatusRunning {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStore) GetExpiredSessions(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Note: This mock uses time.Now() which may cause non-determinism in tests.
	// For tests that need deterministic time, use mockSessionStoreWithExpiry instead,
	// which accepts a custom now time.
	now := time.Now()
	var result []*models.Session
	for _, s := range m.sessions {
		if s.Status == models.StatusRunning && now.After(s.ExpiresAt) {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStore) GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statusSet := make(map[models.SessionStatus]bool)
	for _, s := range statuses {
		statusSet[s] = true
	}

	var result []*models.Session
	for _, s := range m.sessions {
		if statusSet[s.Status] {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStore) Get(ctx context.Context, id string) (*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, &SessionNotFoundError{ID: id}
	}
	copy := *s
	return &copy, nil
}

func (m *mockSessionStore) Update(ctx context.Context, session *models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[session.ID]; !ok {
		return &SessionNotFoundError{ID: session.ID}
	}
	m.sessions[session.ID] = session
	return nil
}

// mockDestroyer implements SessionDestroyer for testing
type mockDestroyer struct {
	mu           sync.Mutex
	destroyCalls []string
	err          error
}

func newMockDestroyer() *mockDestroyer {
	return &mockDestroyer{
		destroyCalls: make([]string, 0),
	}
}

func (m *mockDestroyer) DestroySession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyCalls = append(m.destroyCalls, sessionID)
	return m.err
}

func (m *mockDestroyer) getDestroyCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.destroyCalls))
	copy(result, m.destroyCalls)
	return result
}

// mockEventHandler implements EventHandler for testing
type mockEventHandler struct {
	mu              sync.Mutex
	expiredSessions []*models.Session
	hardMaxSessions []*models.Session
	orphanSessions  []*models.Session
}

func newMockEventHandler() *mockEventHandler {
	return &mockEventHandler{}
}

func (m *mockEventHandler) OnSessionExpired(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expiredSessions = append(m.expiredSessions, session)
}

func (m *mockEventHandler) OnHardMaxReached(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hardMaxSessions = append(m.hardMaxSessions, session)
}

func (m *mockEventHandler) OnOrphanDetected(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphanSessions = append(m.orphanSessions, session)
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestManager_New(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	m := New(store, destroyer)

	assert.NotNil(t, m)
	assert.Equal(t, DefaultCheckInterval, m.checkInterval)
	assert.Equal(t, DefaultHardMaxHours, m.hardMaxHours)
	assert.False(t, m.IsRunning())
}

func TestManager_StartStop(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithCheckInterval(10*time.Millisecond))

	ctx := context.Background()

	err := m.Start(ctx)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	// Wait for at least one check using Eventually instead of Sleep
	require.Eventually(t, func() bool {
		return m.GetMetrics().ChecksRun > 0
	}, 5*time.Second, 10*time.Millisecond, "expected at least one check to run")

	m.Stop()
	assert.False(t, m.IsRunning())

	metrics := m.GetMetrics()
	assert.Greater(t, metrics.ChecksRun, int64(0))
}

func TestManager_CheckHardMax(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	now := time.Now()

	// Create a session that exceeds hard max (13 hours old)
	oldSession := &models.Session{
		ID:        "sess-old",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-13 * time.Hour),
		ExpiresAt: now.Add(1 * time.Hour),
	}
	store.add(oldSession)

	// Create a session within hard max (6 hours old)
	newSession := &models.Session{
		ID:        "sess-new",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-6 * time.Hour),
		ExpiresAt: now.Add(6 * time.Hour),
	}
	store.add(newSession)

	// Create a session with hard max override (15 hours old)
	overrideSession := &models.Session{
		ID:              "sess-override",
		Status:          models.StatusRunning,
		CreatedAt:       now.Add(-15 * time.Hour),
		ExpiresAt:       now.Add(1 * time.Hour),
		HardMaxOverride: true,
	}
	store.add(overrideSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithHardMaxHours(12),
		WithEventHandler(handler),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkHardMax(ctx)

	// Only old session should be destroyed
	assert.Equal(t, []string{"sess-old"}, destroyer.getDestroyCalls())
	assert.Len(t, handler.hardMaxSessions, 1)
	assert.Equal(t, "sess-old", handler.hardMaxSessions[0].ID)
}

func TestManager_CheckReservationExpiry(t *testing.T) {
	now := time.Now()

	store := &mockSessionStoreWithExpiry{
		sessions: make(map[string]*models.Session),
		now:      now,
	}
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	// Create an expired session
	expiredSession := &models.Session{
		ID:        "sess-expired",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-3 * time.Hour),
		ExpiresAt: now.Add(-1 * time.Hour), // Expired 1 hour ago
	}
	store.add(expiredSession)

	// Create a non-expired session
	validSession := &models.Session{
		ID:        "sess-valid",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-1 * time.Hour),
		ExpiresAt: now.Add(1 * time.Hour), // Still valid
	}
	store.add(validSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithEventHandler(handler))

	ctx := context.Background()
	m.checkReservationExpiry(ctx)

	assert.Equal(t, []string{"sess-expired"}, destroyer.getDestroyCalls())
	assert.Len(t, handler.expiredSessions, 1)
}

// mockSessionStoreWithExpiry is like mockSessionStore but uses custom now for expiry check
type mockSessionStoreWithExpiry struct {
	mu       sync.RWMutex
	sessions map[string]*models.Session
	now      time.Time
}

func (m *mockSessionStoreWithExpiry) add(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

func (m *mockSessionStoreWithExpiry) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*models.Session
	for _, s := range m.sessions {
		if s.IsActive() {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStoreWithExpiry) GetExpiredSessions(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*models.Session
	for _, s := range m.sessions {
		if s.Status == models.StatusRunning && m.now.After(s.ExpiresAt) {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStoreWithExpiry) GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statusSet := make(map[models.SessionStatus]bool)
	for _, s := range statuses {
		statusSet[s] = true
	}
	var result []*models.Session
	for _, s := range m.sessions {
		if statusSet[s.Status] {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockSessionStoreWithExpiry) Get(ctx context.Context, id string) (*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, &SessionNotFoundError{ID: id}
	}
	copy := *s
	return &copy, nil
}

func (m *mockSessionStoreWithExpiry) Update(ctx context.Context, session *models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func TestManager_CheckOrphans(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	now := time.Now()

	// Create orphan session (expired + grace period exceeded)
	orphanSession := &models.Session{
		ID:        "sess-orphan",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-3 * time.Hour),
		ExpiresAt: now.Add(-30 * time.Minute), // Expired 30 min ago, grace is 15 min
	}
	store.add(orphanSession)

	// Create session within grace period
	graceSession := &models.Session{
		ID:        "sess-grace",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-5 * time.Minute), // Expired 5 min ago, still in grace
	}
	store.add(graceSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithOrphanGracePeriod(15*time.Minute),
		WithEventHandler(handler),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkOrphans(ctx)

	// Orphan detection doesn't auto-destroy (that's reconciliation's job)
	assert.Empty(t, destroyer.getDestroyCalls())
	// But it should fire the event
	assert.Len(t, handler.orphanSessions, 1)
	assert.Equal(t, "sess-orphan", handler.orphanSessions[0].ID)
}

func TestManager_SignalDone(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	session := &models.Session{
		ID:     "sess-done",
		Status: models.StatusRunning,
	}
	store.add(session)

	m := New(store, destroyer, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := m.SignalDone(ctx, "sess-done")

	require.NoError(t, err)
	assert.Equal(t, []string{"sess-done"}, destroyer.getDestroyCalls())
}

func TestManager_SignalDone_AlreadyStopped(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	session := &models.Session{
		ID:     "sess-stopped",
		Status: models.StatusStopped,
	}
	store.add(session)

	m := New(store, destroyer, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := m.SignalDone(ctx, "sess-stopped")

	// Bug #70: SignalDone now returns SessionTerminalError for terminal sessions
	require.Error(t, err)
	var terminalErr *SessionTerminalError
	require.ErrorAs(t, err, &terminalErr)
	assert.Equal(t, "sess-stopped", terminalErr.ID)
	assert.Equal(t, models.StatusStopped, terminalErr.Status)
	assert.Empty(t, destroyer.getDestroyCalls())
}

func TestManager_ExtendSession(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	now := time.Now()
	session := &models.Session{
		ID:             "sess-extend",
		Status:         models.StatusRunning,
		ReservationHrs: 2,
		ExpiresAt:      now.Add(1 * time.Hour),
	}
	store.add(session)

	m := New(store, destroyer, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := m.ExtendSession(ctx, "sess-extend", 4)

	require.NoError(t, err)

	updated, _ := store.Get(ctx, "sess-extend")
	assert.Equal(t, 6, updated.ReservationHrs)
	assert.WithinDuration(t, now.Add(5*time.Hour), updated.ExpiresAt, time.Minute)
}

func TestManager_ExtendSession_Terminal(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	session := &models.Session{
		ID:     "sess-stopped",
		Status: models.StatusStopped,
	}
	store.add(session)

	m := New(store, destroyer, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := m.ExtendSession(ctx, "sess-stopped", 4)

	require.Error(t, err)
	var termErr *SessionTerminalError
	assert.True(t, errors.As(err, &termErr))
}

func TestManager_SetHardMaxOverride(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	session := &models.Session{
		ID:              "sess-override",
		Status:          models.StatusRunning,
		HardMaxOverride: false,
	}
	store.add(session)

	m := New(store, destroyer, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := m.SetHardMaxOverride(ctx, "sess-override", true)

	require.NoError(t, err)

	updated, _ := store.Get(ctx, "sess-override")
	assert.True(t, updated.HardMaxOverride)
}

func TestManager_DestroyFailure(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	destroyer.err = errors.New("destroy failed")

	now := time.Now()
	session := &models.Session{
		ID:        "sess-fail",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-15 * time.Hour), // Exceeds hard max
		ExpiresAt: now.Add(1 * time.Hour),
	}
	store.add(session)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithHardMaxHours(12),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkHardMax(ctx)

	metrics := m.GetMetrics()
	assert.Equal(t, int64(1), metrics.DestroyFailures)

	// Session should have error recorded
	updated, _ := store.Get(ctx, "sess-fail")
	assert.Contains(t, updated.Error, "destroy failed")
}

func TestManager_GetMetrics(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	m := New(store, destroyer, WithLogger(newTestLogger()))

	metrics := m.GetMetrics()
	assert.Equal(t, int64(0), metrics.ChecksRun)
	assert.Equal(t, int64(0), metrics.SessionsExpired)
	assert.Equal(t, int64(0), metrics.HardMaxEnforced)
}

func TestManager_MultipleStarts(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithCheckInterval(100*time.Millisecond))

	ctx := context.Background()

	// First start
	err := m.Start(ctx)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	// Second start should be no-op
	err = m.Start(ctx)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	m.Stop()
	assert.False(t, m.IsRunning())
}

func TestManager_ContextCancellation(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithCheckInterval(1*time.Second))

	ctx, cancel := context.WithCancel(context.Background())

	err := m.Start(ctx)
	require.NoError(t, err)
	assert.True(t, m.IsRunning())

	// Cancel context
	cancel()

	// Wait for manager to stop using Eventually instead of Sleep
	require.Eventually(t, func() bool {
		return !m.IsRunning()
	}, 5*time.Second, 10*time.Millisecond, "manager should stop after context cancellation")

	assert.False(t, m.IsRunning())
}

func TestManager_RunsAllChecks(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithCheckInterval(10*time.Millisecond))

	ctx := context.Background()
	err := m.Start(ctx)
	require.NoError(t, err)

	// Wait for multiple check cycles using Eventually instead of Sleep
	require.Eventually(t, func() bool {
		return m.GetMetrics().ChecksRun >= 2
	}, 5*time.Second, 10*time.Millisecond, "expected at least 2 checks to run")

	m.Stop()

	metrics := m.GetMetrics()
	assert.GreaterOrEqual(t, metrics.ChecksRun, int64(2))
}

// TestManager_TimeInjection_Deterministic demonstrates that time injection enables
// deterministic testing of time-dependent expiration logic. This test simulates
// time progression without relying on actual clock time, making tests fast and reliable.
func TestManager_TimeInjection_Deterministic(t *testing.T) {
	// Use a fixed base time for complete determinism
	baseTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	currentTime := baseTime

	// Create a controllable time function
	timeFunc := func() time.Time {
		return currentTime
	}

	// Use the mockSessionStoreWithExpiry which also uses controlled time
	store := &mockSessionStoreWithExpiry{
		sessions: make(map[string]*models.Session),
		now:      baseTime,
	}
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	// Create a session that will expire in 2 hours
	session := &models.Session{
		ID:        "sess-time-test",
		Status:    models.StatusRunning,
		CreatedAt: baseTime,
		ExpiresAt: baseTime.Add(2 * time.Hour), // Expires at 14:00
	}
	store.add(session)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithHardMaxHours(12),
		WithOrphanGracePeriod(15*time.Minute),
		WithEventHandler(handler),
		WithTimeFunc(timeFunc))

	ctx := context.Background()

	// T+0: Session should not be expired (created at 12:00, expires at 14:00)
	m.checkHardMax(ctx)
	m.checkOrphans(ctx)
	assert.Empty(t, destroyer.getDestroyCalls(), "session should not be destroyed at T+0")
	assert.Empty(t, handler.orphanSessions, "no orphans at T+0")

	// T+1h: Session still valid (13:00, expires at 14:00)
	currentTime = baseTime.Add(1 * time.Hour)
	store.now = currentTime
	m.checkReservationExpiry(ctx)
	m.checkOrphans(ctx)
	assert.Empty(t, destroyer.getDestroyCalls(), "session should not be destroyed at T+1h")
	assert.Empty(t, handler.orphanSessions, "no orphans at T+1h")

	// T+2h 5m: Session expired but within grace period (14:05, grace ends at 14:15)
	currentTime = baseTime.Add(2*time.Hour + 5*time.Minute)
	store.now = currentTime
	m.checkReservationExpiry(ctx)
	assert.Equal(t, []string{"sess-time-test"}, destroyer.getDestroyCalls(), "expired session should be destroyed")
	m.checkOrphans(ctx)
	assert.Empty(t, handler.orphanSessions, "still within grace period at T+2h5m")

	// Reset for hard max test
	destroyer.destroyCalls = nil
	handler.orphanSessions = nil

	// Create a new session for hard max testing
	hardMaxSession := &models.Session{
		ID:        "sess-hard-max",
		Status:    models.StatusRunning,
		CreatedAt: baseTime,
		ExpiresAt: baseTime.Add(24 * time.Hour), // Long reservation
	}
	store.sessions = map[string]*models.Session{hardMaxSession.ID: hardMaxSession}

	// T+11h: Session still within hard max (12 hour limit)
	currentTime = baseTime.Add(11 * time.Hour)
	m.checkHardMax(ctx)
	assert.Empty(t, destroyer.getDestroyCalls(), "session should not hit hard max at T+11h")

	// T+13h: Session exceeds hard max
	currentTime = baseTime.Add(13 * time.Hour)
	m.checkHardMax(ctx)
	assert.Equal(t, []string{"sess-hard-max"}, destroyer.getDestroyCalls(), "session should hit hard max at T+13h")
	assert.Len(t, handler.hardMaxSessions, 1, "hard max event should be fired")
}

// TestManager_TimeInjection_OrphanGracePeriod demonstrates time injection for
// orphan detection grace period testing.
func TestManager_TimeInjection_OrphanGracePeriod(t *testing.T) {
	baseTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	currentTime := baseTime

	timeFunc := func() time.Time {
		return currentTime
	}

	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	// Create a session that expires at 14:00, grace period is 15 minutes
	// So orphan detection should trigger after 14:15
	session := &models.Session{
		ID:        "sess-orphan-grace",
		Status:    models.StatusRunning,
		CreatedAt: baseTime,
		ExpiresAt: baseTime.Add(2 * time.Hour), // Expires at 14:00
	}
	store.add(session)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithOrphanGracePeriod(15*time.Minute),
		WithEventHandler(handler),
		WithTimeFunc(timeFunc))

	ctx := context.Background()

	// T+2h: Expired but within grace period (14:00 - 14:00, grace ends at 14:15)
	currentTime = baseTime.Add(2 * time.Hour)
	m.checkOrphans(ctx)
	assert.Empty(t, handler.orphanSessions, "no orphan yet at exact expiry")

	// T+2h10m: Still within grace period (14:10, grace ends at 14:15)
	currentTime = baseTime.Add(2*time.Hour + 10*time.Minute)
	m.checkOrphans(ctx)
	assert.Empty(t, handler.orphanSessions, "no orphan at T+2h10m (within grace)")

	// T+2h16m: Past grace period (14:16, grace ended at 14:15)
	currentTime = baseTime.Add(2*time.Hour + 16*time.Minute)
	m.checkOrphans(ctx)
	assert.Len(t, handler.orphanSessions, 1, "orphan detected after grace period")
	assert.Equal(t, "sess-orphan-grace", handler.orphanSessions[0].ID)
}
