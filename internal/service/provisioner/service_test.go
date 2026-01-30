package provisioner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
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

func (m *mockSessionStore) Create(ctx context.Context, session *models.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func (m *mockSessionStore) Get(ctx context.Context, id string) (*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, &SessionNotFoundError{ID: id}
	}
	// Return a copy
	copy := *session
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

func (m *mockSessionStore) UpdateHeartbeat(ctx context.Context, id string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return &SessionNotFoundError{ID: id}
	}
	session.LastHeartbeat = t
	return nil
}

func (m *mockSessionStore) UpdateHeartbeatWithIdle(ctx context.Context, id string, t time.Time, idleSeconds int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return &SessionNotFoundError{ID: id}
	}
	session.LastHeartbeat = t
	session.LastIdleSeconds = idleSeconds
	return nil
}

func (m *mockSessionStore) GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, session := range m.sessions {
		if session.ConsumerID == consumerID && session.OfferID == offerID {
			if session.Status == models.StatusPending ||
				session.Status == models.StatusProvisioning ||
				session.Status == models.StatusRunning {
				copy := *session
				return &copy, nil
			}
		}
	}
	return nil, ErrNotFound
}

// mockProvider implements provider.Provider for testing
type mockProvider struct {
	name              string
	createInstanceFn  func(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error)
	destroyInstanceFn func(ctx context.Context, instanceID string) error
	getStatusFn       func(ctx context.Context, instanceID string) (*provider.InstanceStatus, error)

	mu                sync.Mutex
	createCalls       int
	destroyCalls      int
	statusCalls       int
	lastCreateRequest provider.CreateInstanceRequest
}

func newMockProvider(name string) *mockProvider {
	return &mockProvider{
		name: name,
		createInstanceFn: func(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
			return &provider.InstanceInfo{
				ProviderInstanceID: "mock-instance-123",
				SSHHost:            "192.168.1.100",
				SSHPort:            22,
				SSHUser:            "root",
				Status:             "running",
			}, nil
		},
		destroyInstanceFn: func(ctx context.Context, instanceID string) error {
			return nil
		},
		getStatusFn: func(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
			return nil, provider.ErrInstanceNotFound
		},
	}
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	return nil, nil
}

func (m *mockProvider) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	return nil, nil
}

func (m *mockProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	m.mu.Lock()
	m.createCalls++
	m.lastCreateRequest = req
	m.mu.Unlock()
	return m.createInstanceFn(ctx, req)
}

func (m *mockProvider) DestroyInstance(ctx context.Context, instanceID string) error {
	m.mu.Lock()
	m.destroyCalls++
	m.mu.Unlock()
	return m.destroyInstanceFn(ctx, instanceID)
}

func (m *mockProvider) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	m.mu.Lock()
	m.statusCalls++
	m.mu.Unlock()
	return m.getStatusFn(ctx, instanceID)
}

func (m *mockProvider) SupportsFeature(feature provider.ProviderFeature) bool {
	return false
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestService_New(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry)

	assert.NotEmpty(t, svc.GetDeploymentID())
}

func TestService_CreateSession_Success(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry,
		WithLogger(newTestLogger()),
		WithDeploymentID("test-deployment"))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	offer := &models.GPUOffer{
		ID:           "offer-123",
		Provider:     "vastai",
		ProviderID:   "provider-offer-123",
		GPUType:      "RTX4090",
		GPUCount:     1,
		PricePerHour: 0.50,
	}

	session, err := svc.CreateSession(ctx, req, offer)

	require.NoError(t, err)
	assert.NotEmpty(t, session.ID)
	assert.Equal(t, "consumer-001", session.ConsumerID)
	assert.Equal(t, "vastai", session.Provider)
	assert.Equal(t, "RTX4090", session.GPUType)
	assert.Equal(t, 1, session.GPUCount)
	assert.Equal(t, models.StatusProvisioning, session.Status)
	assert.Equal(t, "mock-instance-123", session.ProviderID)
	assert.Equal(t, "192.168.1.100", session.SSHHost)
	assert.Equal(t, 22, session.SSHPort)
	assert.Equal(t, "root", session.SSHUser)
	assert.NotEmpty(t, session.SSHPrivateKey)
	assert.NotEmpty(t, session.SSHPublicKey)
	assert.NotEmpty(t, session.AgentToken)
	assert.Equal(t, 0.50, session.PricePerHour)
	assert.Equal(t, 2, session.ReservationHrs)
	assert.Equal(t, models.StorageDestroy, session.StoragePolicy)

	// Verify expiration time
	expectedExpiry := session.CreatedAt.Add(2 * time.Hour)
	assert.WithinDuration(t, expectedExpiry, session.ExpiresAt, time.Second)

	// Verify provider was called
	assert.Equal(t, 1, prov.createCalls)
	assert.Equal(t, "provider-offer-123", prov.lastCreateRequest.OfferID)
	assert.Equal(t, session.ID, prov.lastCreateRequest.SessionID)
	assert.NotEmpty(t, prov.lastCreateRequest.SSHPublicKey)
}

func TestService_CreateSession_GeneratesSSHKeys(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	// Verify SSH private key format
	assert.True(t, strings.HasPrefix(session.SSHPrivateKey, "-----BEGIN RSA PRIVATE KEY-----"))
	assert.True(t, strings.HasSuffix(strings.TrimSpace(session.SSHPrivateKey), "-----END RSA PRIVATE KEY-----"))

	// Verify SSH public key format
	assert.True(t, strings.HasPrefix(session.SSHPublicKey, "ssh-rsa "))
}

func TestService_CreateSession_SetsInstanceTags(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry,
		WithLogger(newTestLogger()),
		WithDeploymentID("test-deploy-123"))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	// Verify tags were set correctly
	tags := prov.lastCreateRequest.Tags
	assert.Equal(t, session.ID, tags.ShopperSessionID)
	assert.Equal(t, "test-deploy-123", tags.ShopperDeploymentID)
	assert.Equal(t, "consumer-001", tags.ShopperConsumerID)
	assert.WithinDuration(t, session.ExpiresAt, tags.ShopperExpiresAt, time.Second)
}

func TestService_CreateSession_SetsAgentEnv(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry,
		WithLogger(newTestLogger()),
		WithDeploymentID("test-deploy"),
		WithAgentPort(9000))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	env := prov.lastCreateRequest.EnvVars
	assert.Equal(t, session.ID, env["SHOPPER_SESSION_ID"])
	assert.Equal(t, "test-deploy", env["SHOPPER_DEPLOYMENT_ID"])
	assert.Equal(t, "consumer-001", env["SHOPPER_CONSUMER_ID"])
	assert.Equal(t, session.AgentToken, env["SHOPPER_AGENT_TOKEN"])
	assert.Equal(t, "9000", env["SHOPPER_AGENT_PORT"])
	assert.NotEmpty(t, env["SHOPPER_EXPIRES_AT"])
}

func TestService_CreateSession_ProviderError(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	prov.createInstanceFn = func(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
		return nil, errors.New("provider unavailable")
	}
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	_, err := svc.CreateSession(ctx, req, offer)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create instance")

	// Session should be marked as failed
	// Find the session in the store
	var failedSession *models.Session
	for _, s := range store.sessions {
		failedSession = s
		break
	}
	require.NotNil(t, failedSession)
	assert.Equal(t, models.StatusFailed, failedSession.Status)
	assert.Contains(t, failedSession.Error, "provider create failed")
}

func TestService_CreateSession_ProviderNotFound(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{}) // No providers

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	_, err := svc.CreateSession(ctx, req, offer)

	require.Error(t, err)
	var notFound *ProviderNotFoundError
	assert.True(t, errors.As(err, &notFound))
}

func TestService_DestroySession_Success(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Pre-create a session
	session := &models.Session{
		ID:         "sess-001",
		Provider:   "vastai",
		ProviderID: "instance-123",
		Status:     models.StatusRunning,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.DestroySession(ctx, "sess-001")

	require.NoError(t, err)

	// Verify session status
	updated, _ := store.Get(ctx, "sess-001")
	assert.Equal(t, models.StatusStopped, updated.Status)
	assert.False(t, updated.StoppedAt.IsZero())

	// Verify provider was called
	assert.Equal(t, 1, prov.destroyCalls)
	assert.Equal(t, 1, prov.statusCalls)
}

func TestService_DestroySession_AlreadyStopped(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Pre-create a stopped session
	session := &models.Session{
		ID:         "sess-001",
		Provider:   "vastai",
		ProviderID: "instance-123",
		Status:     models.StatusStopped,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.DestroySession(ctx, "sess-001")

	require.NoError(t, err)

	// Provider should not be called
	assert.Equal(t, 0, prov.destroyCalls)
}

func TestService_DestroySession_VerificationRetries(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")

	// Instance stays running for a few checks then disappears
	statusCallCount := 0
	prov.getStatusFn = func(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
		statusCallCount++
		if statusCallCount < 3 {
			return &provider.InstanceStatus{Status: "running", Running: true}, nil
		}
		return nil, provider.ErrInstanceNotFound
	}

	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	session := &models.Session{
		ID:         "sess-001",
		Provider:   "vastai",
		ProviderID: "instance-123",
		Status:     models.StatusRunning,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.DestroySession(ctx, "sess-001")

	require.NoError(t, err)
	assert.Equal(t, 3, prov.statusCalls)
	assert.GreaterOrEqual(t, prov.destroyCalls, 1)
}

func TestService_DestroySession_VerificationFails(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")

	// Instance never goes away
	prov.getStatusFn = func(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
		return &provider.InstanceStatus{Status: "running", Running: true}, nil
	}

	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	session := &models.Session{
		ID:         "sess-001",
		Provider:   "vastai",
		ProviderID: "instance-123",
		Status:     models.StatusRunning,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry,
		WithLogger(newTestLogger()),
		WithDestroyRetries(3))

	ctx := context.Background()
	err := svc.DestroySession(ctx, "sess-001")

	require.Error(t, err)
	var verifyErr *DestroyVerificationError
	assert.True(t, errors.As(err, &verifyErr))
	assert.Equal(t, "sess-001", verifyErr.SessionID)
	assert.Equal(t, 3, verifyErr.Attempts)
}

func TestService_DestroySession_NotFound(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.DestroySession(ctx, "nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestService_DestroySession_NoProviderID(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Session without provider ID (never provisioned)
	session := &models.Session{
		ID:         "sess-001",
		Provider:   "vastai",
		ProviderID: "", // No provider ID
		Status:     models.StatusPending,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.DestroySession(ctx, "sess-001")

	require.NoError(t, err)
	assert.Equal(t, 0, prov.destroyCalls) // No destroy call needed

	updated, _ := store.Get(ctx, "sess-001")
	assert.Equal(t, models.StatusStopped, updated.Status)
}

func TestService_RecordHeartbeat(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{})

	session := &models.Session{
		ID:     "sess-001",
		Status: models.StatusProvisioning,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.RecordHeartbeat(ctx, "sess-001", 120)

	require.NoError(t, err)

	updated, _ := store.Get(ctx, "sess-001")
	assert.False(t, updated.LastHeartbeat.IsZero())
	assert.Equal(t, 120, updated.LastIdleSeconds)
}

func TestService_RecordHeartbeat_NotFound(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	err := svc.RecordHeartbeat(ctx, "nonexistent", 0)

	require.Error(t, err)
}

func TestService_GetSession(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{})

	session := &models.Session{
		ID:       "sess-001",
		Provider: "vastai",
		GPUType:  "RTX4090",
		Status:   models.StatusRunning,
	}
	store.sessions[session.ID] = session

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	result, err := svc.GetSession(ctx, "sess-001")

	require.NoError(t, err)
	assert.Equal(t, "sess-001", result.ID)
	assert.Equal(t, "RTX4090", result.GPUType)
}

func TestService_GetSession_NotFound(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	_, err := svc.GetSession(ctx, "nonexistent")

	require.Error(t, err)
}

func TestSimpleProviderRegistry(t *testing.T) {
	p1 := newMockProvider("vastai")
	p2 := newMockProvider("tensordock")

	registry := NewSimpleProviderRegistry([]provider.Provider{p1, p2})

	// Get existing provider
	prov, err := registry.Get("vastai")
	require.NoError(t, err)
	assert.Equal(t, "vastai", prov.Name())

	// Get non-existing provider
	_, err = registry.Get("nonexistent")
	require.Error(t, err)
	var notFound *ProviderNotFoundError
	assert.True(t, errors.As(err, &notFound))

	// List providers
	names := registry.List()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "vastai")
	assert.Contains(t, names, "tensordock")
}

func TestGenerateSSHKeyPair(t *testing.T) {
	store := newMockSessionStore()
	registry := NewSimpleProviderRegistry([]provider.Provider{})

	svc := New(store, registry)

	privateKey, publicKey, err := svc.generateSSHKeyPair()

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(privateKey, "-----BEGIN RSA PRIVATE KEY-----"))
	assert.True(t, strings.HasPrefix(publicKey, "ssh-rsa "))
}

func TestService_CreateSession_WithCustomStoragePolicy(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadTraining,
		ReservationHrs: 4,
		StoragePolicy:  models.StoragePreserve,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	session, err := svc.CreateSession(ctx, req, offer)

	require.NoError(t, err)
	assert.Equal(t, models.StoragePreserve, session.StoragePolicy)
}

func TestService_CreateSession_WithIdleThreshold(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
		IdleThreshold:  30, // 30 minutes
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	session, err := svc.CreateSession(ctx, req, offer)

	require.NoError(t, err)
	assert.Equal(t, 30, session.IdleThreshold)
}

func TestService_CreateSession_DuplicatePrevention(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	// First session should succeed
	session1, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)
	assert.NotEmpty(t, session1.ID)

	// Second session with same consumer and offer should fail
	_, err = svc.CreateSession(ctx, req, offer)
	require.Error(t, err)

	var dupErr *DuplicateSessionError
	assert.True(t, errors.As(err, &dupErr))
	assert.Equal(t, "consumer-001", dupErr.ConsumerID)
	assert.Equal(t, "offer-123", dupErr.OfferID)
	assert.Equal(t, session1.ID, dupErr.SessionID)
}

func TestService_CreateSession_AllowsAfterStopped(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()

	// Create a stopped session manually
	stoppedSession := &models.Session{
		ID:         "old-sess",
		ConsumerID: "consumer-001",
		OfferID:    "offer-123",
		Provider:   "vastai",
		Status:     models.StatusStopped,
	}
	store.sessions[stoppedSession.ID] = stoppedSession

	// New session with same consumer and offer should succeed (old one is stopped)
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)
	assert.NotEmpty(t, session.ID)
	assert.NotEqual(t, "old-sess", session.ID) // Should be a new session
}

func TestService_CreateSession_AllowsDifferentConsumer(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()
	offer := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}

	// First consumer creates a session
	req1 := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	session1, err := svc.CreateSession(ctx, req1, offer)
	require.NoError(t, err)

	// Different consumer with same offer should succeed
	req2 := models.CreateSessionRequest{
		ConsumerID:     "consumer-002",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	session2, err := svc.CreateSession(ctx, req2, offer)
	require.NoError(t, err)

	assert.NotEqual(t, session1.ID, session2.ID)
}

func TestService_CreateSession_AllowsDifferentOffer(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	svc := New(store, registry, WithLogger(newTestLogger()))

	ctx := context.Background()

	// Same consumer creates session for offer-123
	req1 := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	offer1 := &models.GPUOffer{Provider: "vastai", ProviderID: "123"}
	session1, err := svc.CreateSession(ctx, req1, offer1)
	require.NoError(t, err)

	// Same consumer with different offer should succeed
	req2 := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-456",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 2,
	}
	offer2 := &models.GPUOffer{Provider: "vastai", ProviderID: "456"}
	session2, err := svc.CreateSession(ctx, req2, offer2)
	require.NoError(t, err)

	assert.NotEqual(t, session1.ID, session2.ID)
}
