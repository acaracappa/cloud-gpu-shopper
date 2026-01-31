package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/cost"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/lifecycle"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations

type mockProvider struct {
	name   string
	offers []models.GPUOffer
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	return m.offers, nil
}
func (m *mockProvider) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	return nil, nil
}
func (m *mockProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	return &provider.InstanceInfo{
		ProviderInstanceID: "test-instance",
		SSHHost:            "192.168.1.1",
		SSHPort:            22,
		SSHUser:            "root",
	}, nil
}
func (m *mockProvider) DestroyInstance(ctx context.Context, instanceID string) error {
	return nil
}
func (m *mockProvider) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	return nil, provider.ErrInstanceNotFound
}
func (m *mockProvider) SupportsFeature(feature provider.ProviderFeature) bool {
	return false
}

type mockSessionStore struct {
	sessions map[string]*models.Session
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{sessions: make(map[string]*models.Session)}
}

func (m *mockSessionStore) Create(ctx context.Context, session *models.Session) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *mockSessionStore) Get(ctx context.Context, id string) (*models.Session, error) {
	s, ok := m.sessions[id]
	if !ok {
		return nil, &lifecycle.SessionNotFoundError{ID: id}
	}
	return s, nil
}

func (m *mockSessionStore) Update(ctx context.Context, session *models.Session) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *mockSessionStore) GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error) {
	for _, session := range m.sessions {
		if session.ConsumerID == consumerID && session.OfferID == offerID {
			if session.Status == models.StatusPending ||
				session.Status == models.StatusProvisioning ||
				session.Status == models.StatusRunning {
				return session, nil
			}
		}
	}
	return nil, provisioner.ErrNotFound
}

func (m *mockSessionStore) GetActiveSessions(ctx context.Context) ([]*models.Session, error) {
	var result []*models.Session
	for _, s := range m.sessions {
		if s.IsActive() {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSessionStore) GetExpiredSessions(ctx context.Context) ([]*models.Session, error) {
	return nil, nil
}

func (m *mockSessionStore) GetSessionsByStatus(ctx context.Context, statuses ...models.SessionStatus) ([]*models.Session, error) {
	return nil, nil
}

type mockCostStore struct {
	records []*models.CostRecord
}

func newMockCostStore() *mockCostStore {
	return &mockCostStore{records: make([]*models.CostRecord, 0)}
}

func (m *mockCostStore) Record(ctx context.Context, record *models.CostRecord) error {
	m.records = append(m.records, record)
	return nil
}

func (m *mockCostStore) RecordHourlyForSession(ctx context.Context, session *models.Session) error {
	return nil
}

func (m *mockCostStore) GetSessionCost(ctx context.Context, sessionID string) (float64, error) {
	var total float64
	for _, r := range m.records {
		if r.SessionID == sessionID {
			total += r.Amount
		}
	}
	return total, nil
}

func (m *mockCostStore) GetConsumerCost(ctx context.Context, consumerID string, start, end time.Time) (float64, error) {
	return 0, nil
}

func (m *mockCostStore) GetSummary(ctx context.Context, query models.CostQuery) (*models.CostSummary, error) {
	return &models.CostSummary{
		TotalCost:    100.0,
		SessionCount: 5,
		HoursUsed:    50,
		ByProvider:   map[string]float64{"vastai": 100.0},
		ByGPUType:    map[string]float64{"RTX4090": 100.0},
	}, nil
}

type mockDestroyer struct{}

func (m *mockDestroyer) DestroySession(ctx context.Context, sessionID string) error {
	return nil
}

func setupTestServer() *Server {
	// Create mock provider
	mockProv := &mockProvider{
		name: "vastai",
		offers: []models.GPUOffer{
			{
				ID:           "offer-1",
				Provider:     "vastai",
				ProviderID:   "prov-1",
				GPUType:      "RTX4090",
				GPUCount:     1,
				VRAM:         24,
				PricePerHour: 0.50,
				Available:    true,
			},
			{
				ID:           "offer-2",
				Provider:     "vastai",
				ProviderID:   "prov-2",
				GPUType:      "A100",
				GPUCount:     1,
				VRAM:         80,
				PricePerHour: 1.50,
				Available:    true,
			},
		},
	}

	// Create services
	inv := inventory.New([]provider.Provider{mockProv})

	sessionStore := newMockSessionStore()
	registry := provisioner.NewSimpleProviderRegistry([]provider.Provider{mockProv})
	prov := provisioner.New(sessionStore, registry)

	destroyer := &mockDestroyer{}
	lm := lifecycle.New(sessionStore, destroyer)

	costStore := newMockCostStore()
	ct := cost.New(costStore, sessionStore, nil)

	server := New(inv, prov, lm, ct)
	// Set server as ready by default in tests
	server.SetReady(true)
	return server
}

func TestHealth(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response.Status)
	assert.Equal(t, "true", response.Services["ready"])
}

func TestHealthNotReady(t *testing.T) {
	server := setupTestServer()
	server.SetReady(false)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", response.Status)
	assert.Equal(t, "false", response.Services["ready"])
}

func TestReadyEndpoint(t *testing.T) {
	server := setupTestServer()

	// Server is ready by default in test setup
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ReadyResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response.Ready)
}

func TestReadyEndpointNotReady(t *testing.T) {
	server := setupTestServer()
	server.SetReady(false)

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response ReadyResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.False(t, response.Ready)
}

func TestListInventory(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/api/v1/inventory", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	count := int(response["count"].(float64))
	assert.Equal(t, 2, count)
}

func TestListInventoryWithFilter(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/api/v1/inventory?gpu_type=RTX4090", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	count := int(response["count"].(float64))
	assert.Equal(t, 1, count)
}

func TestListInventoryWithMaxPrice(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/api/v1/inventory?max_price=1.00", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	count := int(response["count"].(float64))
	assert.Equal(t, 1, count) // Only RTX4090 at $0.50
}

func TestGetOffer(t *testing.T) {
	server := setupTestServer()

	// First populate cache
	req := httptest.NewRequest("GET", "/api/v1/inventory", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	// Now get specific offer
	req = httptest.NewRequest("GET", "/api/v1/inventory/offer-1", nil)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var offer models.GPUOffer
	err := json.Unmarshal(w.Body.Bytes(), &offer)
	require.NoError(t, err)
	assert.Equal(t, "offer-1", offer.ID)
	assert.Equal(t, "RTX4090", offer.GPUType)
}

func TestGetOfferNotFound(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/api/v1/inventory/nonexistent", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreateSession(t *testing.T) {
	server := setupTestServer()

	// First populate inventory cache
	req := httptest.NewRequest("GET", "/api/v1/inventory", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	// Create session
	body := `{
		"consumer_id": "consumer-001",
		"offer_id": "offer-1",
		"workload_type": "llm",
		"reservation_hours": 2
	}`
	req = httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var response CreateSessionResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.NotEmpty(t, response.Session.ID)
	assert.Equal(t, "consumer-001", response.Session.ConsumerID)
	assert.NotEmpty(t, response.SSHPrivateKey)
}

func TestCreateSessionBadRequest(t *testing.T) {
	server := setupTestServer()

	// Missing required fields
	body := `{"consumer_id": "consumer-001"}`
	req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateSessionOfferNotFound(t *testing.T) {
	server := setupTestServer()

	body := `{
		"consumer_id": "consumer-001",
		"offer_id": "nonexistent",
		"workload_type": "llm",
		"reservation_hours": 2
	}`
	req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetCosts(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/api/v1/costs?period=monthly", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var summary models.CostSummary
	err := json.Unmarshal(w.Body.Bytes(), &summary)
	require.NoError(t, err)
	assert.Equal(t, 100.0, summary.TotalCost)
}

func TestGetCostSummary(t *testing.T) {
	server := setupTestServer()

	req := httptest.NewRequest("GET", "/api/v1/costs/summary", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequestIDMiddleware(t *testing.T) {
	server := setupTestServer()

	// Without X-Request-ID header
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))

	// With X-Request-ID header
	req = httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("X-Request-ID", "custom-request-id")
	w = httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	assert.Equal(t, "custom-request-id", w.Header().Get("X-Request-ID"))
}

func TestSessionDone(t *testing.T) {
	server := setupTestServer()

	// First populate inventory and create a session
	req := httptest.NewRequest("GET", "/api/v1/inventory", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	body := `{
		"consumer_id": "consumer-001",
		"offer_id": "offer-1",
		"workload_type": "llm",
		"reservation_hours": 2
	}`
	req = httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	var createResp CreateSessionResponse
	json.Unmarshal(w.Body.Bytes(), &createResp)
	sessionID := createResp.Session.ID

	// Signal done
	req = httptest.NewRequest("POST", "/api/v1/sessions/"+sessionID+"/done", nil)
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestExtendSession(t *testing.T) {
	server := setupTestServer()

	// First populate inventory and create a session
	req := httptest.NewRequest("GET", "/api/v1/inventory", nil)
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	body := `{
		"consumer_id": "consumer-001",
		"offer_id": "offer-1",
		"workload_type": "llm",
		"reservation_hours": 2
	}`
	req = httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	var createResp CreateSessionResponse
	json.Unmarshal(w.Body.Bytes(), &createResp)
	sessionID := createResp.Session.ID

	// Extend session
	extendBody := `{"additional_hours": 4}`
	req = httptest.NewRequest("POST", "/api/v1/sessions/"+sessionID+"/extend", strings.NewReader(extendBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
