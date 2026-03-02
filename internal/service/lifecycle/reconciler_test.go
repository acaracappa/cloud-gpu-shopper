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

// mockProvider implements provider.Provider for reconciler testing
type mockReconcileProvider struct {
	name      string
	instances []provider.ProviderInstance
	err       error

	mu           sync.Mutex
	destroyCalls []string
	statusFn     func(id string) (*provider.InstanceStatus, error)
}

func newMockReconcileProvider(name string) *mockReconcileProvider {
	return &mockReconcileProvider{
		name:         name,
		instances:    []provider.ProviderInstance{},
		destroyCalls: []string{},
	}
}

func (m *mockReconcileProvider) Name() string {
	return m.name
}

func (m *mockReconcileProvider) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	return nil, nil
}

func (m *mockReconcileProvider) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.instances, nil
}

func (m *mockReconcileProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	return nil, nil
}

func (m *mockReconcileProvider) DestroyInstance(ctx context.Context, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destroyCalls = append(m.destroyCalls, instanceID)
	return nil
}

func (m *mockReconcileProvider) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	if m.statusFn != nil {
		return m.statusFn(instanceID)
	}
	return nil, provider.ErrInstanceNotFound
}

func (m *mockReconcileProvider) SupportsFeature(feature provider.ProviderFeature) bool {
	return false
}

func (m *mockReconcileProvider) getDestroyCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.destroyCalls))
	copy(result, m.destroyCalls)
	return result
}

// mockProviderRegistry implements ProviderRegistry for testing
type mockProviderRegistry struct {
	providers map[string]provider.Provider
}

func newMockProviderRegistry() *mockProviderRegistry {
	return &mockProviderRegistry{
		providers: make(map[string]provider.Provider),
	}
}

func (m *mockProviderRegistry) Add(p provider.Provider) {
	m.providers[p.Name()] = p
}

func (m *mockProviderRegistry) Get(name string) (provider.Provider, error) {
	p, ok := m.providers[name]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
}

func (m *mockProviderRegistry) List() []string {
	names := make([]string, 0, len(m.providers))
	for name := range m.providers {
		names = append(names, name)
	}
	return names
}

// mockReconcileStore implements ReconcileStore for testing
type mockReconcileStore struct {
	mu       sync.RWMutex
	sessions map[string]*models.Session
}

func newMockReconcileStore() *mockReconcileStore {
	return &mockReconcileStore{
		sessions: make(map[string]*models.Session),
	}
}

func (m *mockReconcileStore) add(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
}

func (m *mockReconcileStore) GetActiveSessionsByProvider(ctx context.Context, providerName string) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*models.Session
	for _, s := range m.sessions {
		if s.Provider == providerName && s.IsActive() {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockReconcileStore) GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error) {
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

func (m *mockReconcileStore) Get(ctx context.Context, id string) (*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, &SessionNotFoundError{ID: id}
	}
	copy := *s
	return &copy, nil
}

func (m *mockReconcileStore) Update(ctx context.Context, session *models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[session.ID]; !ok {
		return &SessionNotFoundError{ID: session.ID}
	}
	m.sessions[session.ID] = session
	return nil
}

// mockReconcileEventHandler implements ReconcileEventHandler for testing
type mockReconcileEventHandler struct {
	mu      sync.Mutex
	orphans []string
	ghosts  []*models.Session
	errors  []string
}

func newMockReconcileEventHandler() *mockReconcileEventHandler {
	return &mockReconcileEventHandler{}
}

func (m *mockReconcileEventHandler) OnOrphanFound(providerName, instanceID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphans = append(m.orphans, instanceID)
}

func (m *mockReconcileEventHandler) OnGhostFound(session *models.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ghosts = append(m.ghosts, session)
}

func (m *mockReconcileEventHandler) OnReconcileError(providerName string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, providerName)
}

func TestReconciler_New(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	r := NewReconciler(store, registry)

	assert.NotNil(t, r)
	assert.Equal(t, DefaultReconcileInterval, r.reconcileInterval)
	assert.True(t, r.autoDestroyOrphans)
	assert.False(t, r.IsRunning())
}

func TestReconciler_StartStop(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()),
		WithReconcileInterval(10*time.Millisecond))

	ctx := context.Background()

	err := r.Start(ctx)
	require.NoError(t, err)
	assert.True(t, r.IsRunning())

	time.Sleep(50 * time.Millisecond)

	r.Stop()
	assert.False(t, r.IsRunning())

	metrics := r.GetMetrics()
	assert.Greater(t, metrics.ReconciliationsRun, int64(0))
}

func TestReconciler_DetectsOrphan(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()
	handler := newMockReconcileEventHandler()

	// Provider has an instance that's not in our DB
	prov := newMockReconcileProvider("vastai")
	prov.instances = []provider.ProviderInstance{
		{
			ID:     "orphan-instance",
			Name:   "shopper-orphan-session",
			Status: "running",
			Tags: models.InstanceTags{
				ShopperSessionID:    "orphan-session",
				ShopperDeploymentID: "test-deploy",
			},
		},
	}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()),
		WithDeploymentID("test-deploy"),
		WithReconcileEventHandler(handler),
		WithAutoDestroyOrphans(true))

	ctx := context.Background()
	r.RunReconciliation(ctx)

	// Orphan should be detected and destroyed
	assert.Len(t, handler.orphans, 1)
	assert.Equal(t, "orphan-instance", handler.orphans[0])
	assert.Equal(t, []string{"orphan-instance"}, prov.getDestroyCalls())

	metrics := r.GetMetrics()
	assert.Equal(t, int64(1), metrics.OrphansFound)
	assert.Equal(t, int64(1), metrics.OrphansDestroyed)
}

func TestReconciler_DetectsGhost(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()
	handler := newMockReconcileEventHandler()

	// DB has a session but provider doesn't have the instance
	ghostSession := &models.Session{
		ID:         "ghost-session",
		Provider:   "vastai",
		ProviderID: "missing-instance",
		Status:     models.StatusRunning,
	}
	store.add(ghostSession)

	// Provider has no instances
	prov := newMockReconcileProvider("vastai")
	prov.instances = []provider.ProviderInstance{}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()),
		WithReconcileEventHandler(handler))

	ctx := context.Background()
	r.RunReconciliation(ctx)

	// Ghost should be detected
	assert.Len(t, handler.ghosts, 1)
	assert.Equal(t, "ghost-session", handler.ghosts[0].ID)

	// Session should be marked as stopped
	updated, _ := store.Get(ctx, "ghost-session")
	assert.Equal(t, models.StatusStopped, updated.Status)
	assert.Contains(t, updated.Error, "not found on provider")

	metrics := r.GetMetrics()
	assert.Equal(t, int64(1), metrics.GhostsFound)
	assert.Equal(t, int64(1), metrics.GhostsFixed)
}

func TestReconciler_MatchingStateNoAction(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// DB and provider have matching state
	session := &models.Session{
		ID:         "matching-session",
		Provider:   "vastai",
		ProviderID: "matching-instance",
		Status:     models.StatusRunning,
	}
	store.add(session)

	prov := newMockReconcileProvider("vastai")
	prov.instances = []provider.ProviderInstance{
		{
			ID:     "matching-instance",
			Status: "running",
			Tags: models.InstanceTags{
				ShopperSessionID: "matching-session",
			},
		},
	}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()))

	ctx := context.Background()
	r.RunReconciliation(ctx)

	// No orphans or ghosts should be detected
	metrics := r.GetMetrics()
	assert.Equal(t, int64(0), metrics.OrphansFound)
	assert.Equal(t, int64(0), metrics.GhostsFound)
}

func TestReconciler_FiltersOtherDeployments(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// Provider has instance from different deployment
	prov := newMockReconcileProvider("vastai")
	prov.instances = []provider.ProviderInstance{
		{
			ID:     "other-instance",
			Status: "running",
			Tags: models.InstanceTags{
				ShopperSessionID:    "other-session",
				ShopperDeploymentID: "other-deploy", // Different deployment
			},
		},
	}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()),
		WithDeploymentID("our-deploy")) // Our deployment ID

	ctx := context.Background()
	r.RunReconciliation(ctx)

	// Should not detect orphan from other deployment
	metrics := r.GetMetrics()
	assert.Equal(t, int64(0), metrics.OrphansFound)
}

func TestReconciler_NoAutoDestroyOrphans(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()
	handler := newMockReconcileEventHandler()

	prov := newMockReconcileProvider("vastai")
	prov.instances = []provider.ProviderInstance{
		{
			ID:     "orphan-instance",
			Status: "running",
			Tags: models.InstanceTags{
				ShopperSessionID:    "orphan-session",
				ShopperDeploymentID: "test-deploy",
			},
		},
	}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()),
		WithDeploymentID("test-deploy"),
		WithReconcileEventHandler(handler),
		WithAutoDestroyOrphans(false)) // Disabled

	ctx := context.Background()
	r.RunReconciliation(ctx)

	// Orphan detected but not destroyed
	assert.Len(t, handler.orphans, 1)
	assert.Empty(t, prov.getDestroyCalls())

	metrics := r.GetMetrics()
	assert.Equal(t, int64(1), metrics.OrphansFound)
	assert.Equal(t, int64(0), metrics.OrphansDestroyed)
}

func TestReconciler_ProviderError(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()
	handler := newMockReconcileEventHandler()

	prov := newMockReconcileProvider("vastai")
	prov.err = errors.New("provider unavailable")
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()),
		WithReconcileEventHandler(handler))

	ctx := context.Background()
	r.RunReconciliation(ctx)

	assert.Len(t, handler.errors, 1)
	assert.Equal(t, "vastai", handler.errors[0])

	metrics := r.GetMetrics()
	assert.Equal(t, int64(1), metrics.Errors)
}

func TestReconciler_IgnoresNonActiveGhosts(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// Stopped session shouldn't be flagged as ghost
	stoppedSession := &models.Session{
		ID:         "stopped-session",
		Provider:   "vastai",
		ProviderID: "old-instance",
		Status:     models.StatusStopped,
	}
	store.add(stoppedSession)

	prov := newMockReconcileProvider("vastai")
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()))

	ctx := context.Background()
	r.RunReconciliation(ctx)

	metrics := r.GetMetrics()
	assert.Equal(t, int64(0), metrics.GhostsFound)
}

func TestReconciler_RecoverStuckProvisioning(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// Session stuck in provisioning with no provider instance
	stuckSession := &models.Session{
		ID:         "stuck-session",
		Provider:   "vastai",
		ProviderID: "",
		Status:     models.StatusProvisioning,
	}
	store.add(stuckSession)

	prov := newMockReconcileProvider("vastai")
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()))

	ctx := context.Background()
	err := r.RecoverStuckSessions(ctx)
	require.NoError(t, err)

	// Session should be marked as failed
	updated, _ := store.Get(ctx, "stuck-session")
	assert.Equal(t, models.StatusFailed, updated.Status)
	assert.Contains(t, updated.Error, "no provider instance ID")
}

func TestReconciler_RecoverStuckProvisioningWithInstance(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// Session stuck in provisioning but instance is running
	stuckSession := &models.Session{
		ID:         "stuck-session",
		Provider:   "vastai",
		ProviderID: "running-instance",
		Status:     models.StatusProvisioning,
	}
	store.add(stuckSession)

	prov := newMockReconcileProvider("vastai")
	prov.statusFn = func(id string) (*provider.InstanceStatus, error) {
		return &provider.InstanceStatus{
			Status:  "running",
			Running: true,
		}, nil
	}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()))

	ctx := context.Background()
	err := r.RecoverStuckSessions(ctx)
	require.NoError(t, err)

	// Session should be marked as running
	updated, _ := store.Get(ctx, "stuck-session")
	assert.Equal(t, models.StatusRunning, updated.Status)
}

func TestReconciler_RecoverStuckStopping(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// Session stuck in stopping but instance is still running
	stuckSession := &models.Session{
		ID:         "stuck-session",
		Provider:   "vastai",
		ProviderID: "still-running",
		Status:     models.StatusStopping,
	}
	store.add(stuckSession)

	prov := newMockReconcileProvider("vastai")
	prov.statusFn = func(id string) (*provider.InstanceStatus, error) {
		return &provider.InstanceStatus{
			Status:  "running",
			Running: true,
		}, nil
	}
	registry.Add(prov)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()))

	ctx := context.Background()
	err := r.RecoverStuckSessions(ctx)
	require.NoError(t, err)

	// Destroy should be retried
	assert.Equal(t, []string{"still-running"}, prov.getDestroyCalls())
}

func TestReconciler_MultipleProviders(t *testing.T) {
	store := newMockReconcileStore()
	registry := newMockProviderRegistry()

	// Add sessions for both providers
	store.add(&models.Session{
		ID:         "vastai-session",
		Provider:   "vastai",
		ProviderID: "vastai-instance",
		Status:     models.StatusRunning,
	})
	store.add(&models.Session{
		ID:         "tensordock-session",
		Provider:   "tensordock",
		ProviderID: "tensordock-instance",
		Status:     models.StatusRunning,
	})

	// Both providers have matching instances
	vastai := newMockReconcileProvider("vastai")
	vastai.instances = []provider.ProviderInstance{
		{ID: "vastai-instance", Status: "running"},
	}
	registry.Add(vastai)

	tensordock := newMockReconcileProvider("tensordock")
	tensordock.instances = []provider.ProviderInstance{
		{ID: "tensordock-instance", Status: "running"},
	}
	registry.Add(tensordock)

	r := NewReconciler(store, registry,
		WithReconcileLogger(newTestLogger()))

	ctx := context.Background()
	r.RunReconciliation(ctx)

	metrics := r.GetMetrics()
	assert.Equal(t, int64(0), metrics.OrphansFound)
	assert.Equal(t, int64(0), metrics.GhostsFound)
}
