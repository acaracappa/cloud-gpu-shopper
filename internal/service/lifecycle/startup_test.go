package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStartupStore implements StartupStore for testing
type mockStartupStore struct {
	mu       sync.RWMutex
	sessions []*models.Session
	err      error
}

func (m *mockStartupStore) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, m.err
	}
	return m.sessions, nil
}

// mockStartupProvider implements provider.Provider for startup testing with configurable destroy behavior
type mockStartupProvider struct {
	name          string
	destroyErr    error
	destroyDelay  time.Duration
	destroyCalled int
	mu            sync.Mutex
}

func (m *mockStartupProvider) Name() string { return m.name }

func (m *mockStartupProvider) DestroyInstance(ctx context.Context, instanceID string) error {
	if m.destroyDelay > 0 {
		select {
		case <-time.After(m.destroyDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.mu.Lock()
	m.destroyCalled++
	m.mu.Unlock()
	return m.destroyErr
}

func (m *mockStartupProvider) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	return nil, nil
}

func (m *mockStartupProvider) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	return nil, nil
}

func (m *mockStartupProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	return nil, nil
}

func (m *mockStartupProvider) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	return nil, nil
}

func (m *mockStartupProvider) SupportsFeature(feature provider.ProviderFeature) bool {
	return false
}

func (m *mockStartupProvider) getDestroyCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.destroyCalled
}

func TestStartupShutdownManager_New(t *testing.T) {
	store := &mockStartupStore{}
	reconciler := NewReconciler(nil, nil)
	registry := newMockProviderRegistry()

	manager := NewStartupShutdownManager(store, reconciler, registry)

	assert.NotNil(t, manager)
	assert.False(t, manager.IsSweepComplete())
}

func TestStartupShutdownManager_RunStartupSweep(t *testing.T) {
	store := &mockStartupStore{}
	mockProv := &mockStartupProvider{name: "vastai"}
	registry := newMockProviderRegistry()
	registry.providers["vastai"] = mockProv

	reconcileStore := newMockReconcileStore()
	reconciler := NewReconciler(reconcileStore, registry)

	manager := NewStartupShutdownManager(
		store,
		reconciler,
		registry,
		WithStartupSweepTimeout(5*time.Second),
	)

	ctx := context.Background()
	err := manager.RunStartupSweep(ctx)

	assert.NoError(t, err)
	assert.True(t, manager.IsSweepComplete())

	metrics := manager.GetMetrics()
	assert.True(t, metrics.StartupSweepRun)
	assert.True(t, metrics.StartupSweepSuccess)
	assert.Greater(t, metrics.StartupSweepTime, time.Duration(0))
}

func TestStartupShutdownManager_GracefulShutdown_NoSessions(t *testing.T) {
	store := &mockStartupStore{sessions: []*models.Session{}}
	reconciler := NewReconciler(nil, nil)
	registry := newMockProviderRegistry()

	manager := NewStartupShutdownManager(
		store,
		reconciler,
		registry,
		WithShutdownTimeout(5*time.Second),
	)

	ctx := context.Background()
	err := manager.GracefulShutdown(ctx)

	assert.NoError(t, err)

	metrics := manager.GetMetrics()
	assert.True(t, metrics.ShutdownRun)
	assert.True(t, metrics.ShutdownSuccess)
	assert.Equal(t, int64(0), metrics.SessionsDestroyed)
}

func TestStartupShutdownManager_GracefulShutdown_WithSessions(t *testing.T) {
	mockProv := &mockStartupProvider{name: "vastai"}
	store := &mockStartupStore{
		sessions: []*models.Session{
			{
				ID:         "session-1",
				Provider:   "vastai",
				ProviderID: "instance-1",
				Status:     models.StatusRunning,
			},
			{
				ID:         "session-2",
				Provider:   "vastai",
				ProviderID: "instance-2",
				Status:     models.StatusRunning,
			},
		},
	}
	registry := newMockProviderRegistry()
	registry.providers["vastai"] = mockProv
	reconciler := NewReconciler(nil, nil)

	manager := NewStartupShutdownManager(
		store,
		reconciler,
		registry,
		WithShutdownTimeout(5*time.Second),
	)

	ctx := context.Background()
	err := manager.GracefulShutdown(ctx)

	assert.NoError(t, err)

	metrics := manager.GetMetrics()
	assert.True(t, metrics.ShutdownRun)
	assert.True(t, metrics.ShutdownSuccess)
	assert.Equal(t, int64(2), metrics.SessionsDestroyed)
	assert.Equal(t, int64(0), metrics.DestroyFailures)

	// Verify destroy was called for each session
	assert.Equal(t, 2, mockProv.getDestroyCalls())
}

func TestStartupShutdownManager_GracefulShutdown_WithDestroyFailures(t *testing.T) {
	mockProv := &mockStartupProvider{
		name:       "vastai",
		destroyErr: errors.New("destroy failed"),
	}
	store := &mockStartupStore{
		sessions: []*models.Session{
			{
				ID:         "session-1",
				Provider:   "vastai",
				ProviderID: "instance-1",
				Status:     models.StatusRunning,
			},
		},
	}
	registry := newMockProviderRegistry()
	registry.providers["vastai"] = mockProv
	reconciler := NewReconciler(nil, nil)

	manager := NewStartupShutdownManager(
		store,
		reconciler,
		registry,
		WithShutdownTimeout(5*time.Second),
	)

	ctx := context.Background()
	err := manager.GracefulShutdown(ctx)

	assert.Error(t, err)
	var shutdownErr *ShutdownError
	assert.True(t, errors.As(err, &shutdownErr))
	assert.Equal(t, 1, shutdownErr.TotalSessions)
	assert.Equal(t, 0, shutdownErr.DestroyedSessions)
	assert.Equal(t, 1, shutdownErr.FailedSessions)

	metrics := manager.GetMetrics()
	assert.True(t, metrics.ShutdownRun)
	assert.False(t, metrics.ShutdownSuccess)
	assert.Equal(t, int64(0), metrics.SessionsDestroyed)
	assert.Equal(t, int64(1), metrics.DestroyFailures)
}

func TestStartupShutdownManager_GracefulShutdown_SessionWithoutProviderID(t *testing.T) {
	mockProv := &mockStartupProvider{name: "vastai"}
	store := &mockStartupStore{
		sessions: []*models.Session{
			{
				ID:         "session-1",
				Provider:   "vastai",
				ProviderID: "", // No provider ID
				Status:     models.StatusRunning,
			},
		},
	}
	registry := newMockProviderRegistry()
	registry.providers["vastai"] = mockProv
	reconciler := NewReconciler(nil, nil)

	manager := NewStartupShutdownManager(
		store,
		reconciler,
		registry,
		WithShutdownTimeout(5*time.Second),
	)

	ctx := context.Background()
	err := manager.GracefulShutdown(ctx)

	// Should succeed - session without provider ID is skipped
	assert.NoError(t, err)

	// Verify destroy was not called (no provider ID)
	assert.Equal(t, 0, mockProv.getDestroyCalls())
}

func TestStartupShutdownManager_GracefulShutdown_RespectsConcurrencyLimit(t *testing.T) {
	mockProv := &mockStartupProvider{
		name:         "vastai",
		destroyDelay: 50 * time.Millisecond,
	}

	// Create more sessions than MaxParallelDestroys
	sessions := make([]*models.Session, MaxParallelDestroys+3)
	for i := range sessions {
		sessions[i] = &models.Session{
			ID:         "session-" + string(rune('a'+i)),
			Provider:   "vastai",
			ProviderID: "instance-" + string(rune('a'+i)),
			Status:     models.StatusRunning,
		}
	}

	store := &mockStartupStore{sessions: sessions}
	registry := newMockProviderRegistry()
	registry.providers["vastai"] = mockProv
	reconciler := NewReconciler(nil, nil)

	manager := NewStartupShutdownManager(
		store,
		reconciler,
		registry,
		WithShutdownTimeout(5*time.Second),
	)

	ctx := context.Background()
	start := time.Now()
	err := manager.GracefulShutdown(ctx)
	elapsed := time.Since(start)

	assert.NoError(t, err)

	// With concurrency limit, it should take at least 2 batches
	// (MaxParallelDestroys + remaining sessions)
	// Each batch takes ~50ms
	expectedMinTime := 2 * 50 * time.Millisecond
	assert.GreaterOrEqual(t, elapsed, expectedMinTime,
		"should respect concurrency limit, expected at least %v but took %v", expectedMinTime, elapsed)

	// Verify all destroys completed
	assert.Equal(t, len(sessions), mockProv.getDestroyCalls())
}

func TestStartupShutdownManager_IsSweepComplete(t *testing.T) {
	store := &mockStartupStore{}
	reconcileStore := newMockReconcileStore()
	registry := newMockProviderRegistry()
	reconciler := NewReconciler(reconcileStore, registry)

	manager := NewStartupShutdownManager(store, reconciler, registry)

	// Initially false
	assert.False(t, manager.IsSweepComplete())

	// After sweep, true
	ctx := context.Background()
	err := manager.RunStartupSweep(ctx)
	require.NoError(t, err)

	assert.True(t, manager.IsSweepComplete())
}

func TestStartupShutdownManager_GetMetrics(t *testing.T) {
	store := &mockStartupStore{}
	reconciler := NewReconciler(nil, nil)
	registry := newMockProviderRegistry()

	manager := NewStartupShutdownManager(store, reconciler, registry)

	metrics := manager.GetMetrics()

	// Initial state
	assert.False(t, metrics.StartupSweepRun)
	assert.False(t, metrics.StartupSweepSuccess)
	assert.Equal(t, time.Duration(0), metrics.StartupSweepTime)
	assert.False(t, metrics.ShutdownRun)
	assert.False(t, metrics.ShutdownSuccess)
	assert.Equal(t, time.Duration(0), metrics.ShutdownTime)
	assert.Equal(t, int64(0), metrics.SessionsDestroyed)
	assert.Equal(t, int64(0), metrics.DestroyFailures)
}
