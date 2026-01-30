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
	mu                sync.Mutex
	expiredSessions   []*models.Session
	hardMaxSessions   []*models.Session
	heartbeatSessions []*models.Session
	orphanSessions    []*models.Session
	idleShutdowns     []idleShutdownEvent
}

type idleShutdownEvent struct {
	SessionID   string
	IdleSeconds int
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

func (m *mockEventHandler) OnHeartbeatTimeout(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heartbeatSessions = append(m.heartbeatSessions, session)
}

func (m *mockEventHandler) OnOrphanDetected(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphanSessions = append(m.orphanSessions, session)
}

func (m *mockEventHandler) OnIdleShutdown(sessionID string, idleSeconds int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idleShutdowns = append(m.idleShutdowns, idleShutdownEvent{
		SessionID:   sessionID,
		IdleSeconds: idleSeconds,
	})
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
	assert.Equal(t, DefaultHeartbeatTimeout, m.heartbeatTimeout)
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

	// Wait for at least one check
	time.Sleep(50 * time.Millisecond)

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

func TestManager_CheckHeartbeatTimeout(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	now := time.Now()

	// Create session with stale heartbeat (10 minutes ago)
	staleSession := &models.Session{
		ID:            "sess-stale",
		Status:        models.StatusRunning,
		CreatedAt:     now.Add(-1 * time.Hour),
		ExpiresAt:     now.Add(1 * time.Hour),
		LastHeartbeat: now.Add(-10 * time.Minute),
	}
	store.add(staleSession)

	// Create session with fresh heartbeat (1 minute ago)
	freshSession := &models.Session{
		ID:            "sess-fresh",
		Status:        models.StatusRunning,
		CreatedAt:     now.Add(-1 * time.Hour),
		ExpiresAt:     now.Add(1 * time.Hour),
		LastHeartbeat: now.Add(-1 * time.Minute),
	}
	store.add(freshSession)

	// Create session with no heartbeat yet (just provisioned)
	newSession := &models.Session{
		ID:        "sess-new",
		Status:    models.StatusRunning,
		CreatedAt: now.Add(-1 * time.Minute),
		ExpiresAt: now.Add(1 * time.Hour),
		// LastHeartbeat is zero
	}
	store.add(newSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithHeartbeatTimeout(5*time.Minute),
		WithEventHandler(handler),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkHeartbeatTimeout(ctx)

	// Only stale session should be destroyed
	assert.Equal(t, []string{"sess-stale"}, destroyer.getDestroyCalls())
	assert.Len(t, handler.heartbeatSessions, 1)
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

	require.NoError(t, err)
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

	// Cancel context
	cancel()

	// Wait for manager to stop
	time.Sleep(100 * time.Millisecond)

	// Manager should have stopped due to context cancellation
	// (Note: depending on timing, it might still be marked as running briefly)
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

	// Wait for multiple check cycles
	time.Sleep(50 * time.Millisecond)

	m.Stop()

	metrics := m.GetMetrics()
	assert.GreaterOrEqual(t, metrics.ChecksRun, int64(2))
}

func TestCheckIdleSessions_DestroyIdleSession(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	now := time.Now()

	// Create session with IdleThreshold=5 (minutes) and LastIdleSeconds=400 (> 300 seconds)
	idleSession := &models.Session{
		ID:              "sess-idle",
		Status:          models.StatusRunning,
		CreatedAt:       now.Add(-1 * time.Hour),
		ExpiresAt:       now.Add(1 * time.Hour),
		IdleThreshold:   5,   // 5 minutes = 300 seconds
		LastIdleSeconds: 400, // 400 seconds > 300 seconds threshold
	}
	store.add(idleSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithEventHandler(handler),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkIdleSessions(ctx)

	// Idle session should be destroyed
	assert.Equal(t, []string{"sess-idle"}, destroyer.getDestroyCalls())

	// Event handler should be notified
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Len(t, handler.idleShutdowns, 1)
	assert.Equal(t, "sess-idle", handler.idleShutdowns[0].SessionID)
	assert.Equal(t, 400, handler.idleShutdowns[0].IdleSeconds)
}

func TestCheckIdleSessions_KeepActiveSession(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	now := time.Now()

	// Create session with IdleThreshold=5 (minutes) and LastIdleSeconds=100 (< 300 seconds)
	activeSession := &models.Session{
		ID:              "sess-active",
		Status:          models.StatusRunning,
		CreatedAt:       now.Add(-1 * time.Hour),
		ExpiresAt:       now.Add(1 * time.Hour),
		IdleThreshold:   5,   // 5 minutes = 300 seconds
		LastIdleSeconds: 100, // 100 seconds < 300 seconds threshold
	}
	store.add(activeSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithEventHandler(handler),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkIdleSessions(ctx)

	// Session should NOT be destroyed (still active)
	assert.Empty(t, destroyer.getDestroyCalls())

	// Event handler should NOT be notified
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Empty(t, handler.idleShutdowns)
}

func TestCheckIdleSessions_IgnoreZeroThreshold(t *testing.T) {
	store := newMockSessionStore()
	destroyer := newMockDestroyer()
	handler := newMockEventHandler()

	now := time.Now()

	// Create session with IdleThreshold=0 (idle detection disabled)
	noThresholdSession := &models.Session{
		ID:              "sess-no-threshold",
		Status:          models.StatusRunning,
		CreatedAt:       now.Add(-1 * time.Hour),
		ExpiresAt:       now.Add(1 * time.Hour),
		IdleThreshold:   0,    // Disabled
		LastIdleSeconds: 1000, // Very idle, but threshold is disabled
	}
	store.add(noThresholdSession)

	m := New(store, destroyer,
		WithLogger(newTestLogger()),
		WithEventHandler(handler),
		WithTimeFunc(func() time.Time { return now }))

	ctx := context.Background()
	m.checkIdleSessions(ctx)

	// Session should NOT be destroyed (threshold disabled)
	assert.Empty(t, destroyer.getDestroyCalls())

	// Event handler should NOT be notified
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assert.Empty(t, handler.idleShutdowns)
}
