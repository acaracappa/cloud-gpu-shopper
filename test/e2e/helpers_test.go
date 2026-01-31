//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Environment variables for test configuration
const (
	EnvServerURL       = "SERVER_URL"
	EnvMockProviderURL = "MOCK_PROVIDER_URL"
	EnvTestTimeout     = "TEST_TIMEOUT"
)

// Default URLs for local testing
const (
	DefaultServerURL       = "http://localhost:8080"
	DefaultMockProviderURL = "http://localhost:8888"
	DefaultTestTimeout     = 60 * time.Second
)

// TestEnv holds the test environment configuration
type TestEnv struct {
	ServerURL       string
	MockProviderURL string
	TestTimeout     time.Duration
	HTTPClient      *http.Client
}

// NewTestEnv creates a new test environment from env vars or defaults
func NewTestEnv() *TestEnv {
	env := &TestEnv{
		ServerURL:       getEnvOrDefault(EnvServerURL, DefaultServerURL),
		MockProviderURL: getEnvOrDefault(EnvMockProviderURL, DefaultMockProviderURL),
		TestTimeout:     DefaultTestTimeout,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	if timeout := os.Getenv(EnvTestTimeout); timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			env.TestTimeout = d
		}
	}

	return env
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// WaitForServer waits for the server to be healthy
func (e *TestEnv) WaitForServer(t *testing.T, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Server did not become healthy within %v", timeout)
		case <-ticker.C:
			resp, err := e.HTTPClient.Get(e.ServerURL + "/health")
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

// WaitForMockProvider waits for the mock provider to be healthy
func (e *TestEnv) WaitForMockProvider(t *testing.T, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Mock provider did not become healthy within %v", timeout)
		case <-ticker.C:
			resp, err := e.HTTPClient.Get(e.MockProviderURL + "/health")
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

// ResetMockProvider resets the mock provider state
func (e *TestEnv) ResetMockProvider(t *testing.T) {
	t.Helper()

	resp, err := e.HTTPClient.Post(e.MockProviderURL+"/_test/reset", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ConfigureMockProvider configures the mock provider behavior
func (e *TestEnv) ConfigureMockProvider(t *testing.T, config MockProviderConfig) {
	t.Helper()

	body, err := json.Marshal(config)
	require.NoError(t, err)

	resp, err := e.HTTPClient.Post(e.MockProviderURL+"/_test/config", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// MockProviderConfig is the configuration for mock provider behavior
type MockProviderConfig struct {
	CreateDelayMs  int    `json:"create_delay_ms,omitempty"`
	DestroyDelayMs int    `json:"destroy_delay_ms,omitempty"`
	FailCreate     bool   `json:"fail_create,omitempty"`
	FailDestroy    bool   `json:"fail_destroy,omitempty"`
	FailCreateMsg  string `json:"fail_create_msg,omitempty"`
	FailDestroyMsg string `json:"fail_destroy_msg,omitempty"`
}

// API Request/Response types

// InventoryResponse is the response from listing inventory
type InventoryResponse struct {
	Offers []GPUOffer `json:"offers"`
	Count  int        `json:"count"`
}

// GPUOffer represents a GPU offer
type GPUOffer struct {
	ID           string  `json:"id"`
	Provider     string  `json:"provider"`
	ProviderID   string  `json:"provider_id"`
	GPUType      string  `json:"gpu_type"`
	GPUCount     int     `json:"gpu_count"`
	VRAM         int     `json:"vram"`
	PricePerHour float64 `json:"price_per_hour"`
	Available    bool    `json:"available"`
	Location     string  `json:"location"`
}

// CreateSessionRequest is the request to create a session
type CreateSessionRequest struct {
	ConsumerID     string `json:"consumer_id"`
	OfferID        string `json:"offer_id"`
	WorkloadType   string `json:"workload_type"`
	ReservationHrs int    `json:"reservation_hours"`
	IdleThreshold  int    `json:"idle_threshold_minutes,omitempty"`
	StoragePolicy  string `json:"storage_policy,omitempty"`
}

// CreateSessionResponse is the response from creating a session
type CreateSessionResponse struct {
	Session       SessionResponse `json:"session"`
	SSHPrivateKey string          `json:"ssh_private_key"`
}

// SessionResponse is the session data in responses
type SessionResponse struct {
	ID             string    `json:"id"`
	ConsumerID     string    `json:"consumer_id"`
	Provider       string    `json:"provider"`
	GPUType        string    `json:"gpu_type"`
	GPUCount       int       `json:"gpu_count"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	SSHHost        string    `json:"ssh_host,omitempty"`
	SSHPort        int       `json:"ssh_port,omitempty"`
	SSHUser        string    `json:"ssh_user,omitempty"`
	WorkloadType   string    `json:"workload_type"`
	ReservationHrs int       `json:"reservation_hours"`
	PricePerHour   float64   `json:"price_per_hour"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// ErrorResponse is the standard error response
type ErrorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

// API Methods

// ListInventory lists available GPU offers
func (e *TestEnv) ListInventory(t *testing.T) *InventoryResponse {
	t.Helper()

	resp, err := e.HTTPClient.Get(e.ServerURL + "/api/v1/inventory")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListInventory failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result InventoryResponse
	require.NoError(t, json.Unmarshal(body, &result))
	return &result
}

// CreateSession creates a new GPU session
func (e *TestEnv) CreateSession(t *testing.T, req CreateSessionRequest) *CreateSessionResponse {
	t.Helper()

	body, err := json.Marshal(req)
	require.NoError(t, err)

	resp, err := e.HTTPClient.Post(e.ServerURL+"/api/v1/sessions", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateSession failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var result CreateSessionResponse
	require.NoError(t, json.Unmarshal(respBody, &result))
	return &result
}

// GetSession gets a session by ID
func (e *TestEnv) GetSession(t *testing.T, sessionID string) *SessionResponse {
	t.Helper()

	resp, err := e.HTTPClient.Get(e.ServerURL + "/api/v1/sessions/" + sessionID)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetSession failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result SessionResponse
	require.NoError(t, json.Unmarshal(body, &result))
	return &result
}

// DeleteSession deletes a session
func (e *TestEnv) DeleteSession(t *testing.T, sessionID string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodDelete, e.ServerURL+"/api/v1/sessions/"+sessionID, nil)
	require.NoError(t, err)

	resp, err := e.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DeleteSession failed: status=%d body=%s", resp.StatusCode, string(body))
	}
}

// SignalDone signals that a session is done
func (e *TestEnv) SignalDone(t *testing.T, sessionID string) {
	t.Helper()

	resp, err := e.HTTPClient.Post(e.ServerURL+"/api/v1/sessions/"+sessionID+"/done", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SignalDone failed: status=%d body=%s", resp.StatusCode, string(body))
	}
}

// ExtendSession extends a session's reservation
func (e *TestEnv) ExtendSession(t *testing.T, sessionID string, additionalHours int) {
	t.Helper()

	reqBody := map[string]int{"additional_hours": additionalHours}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	resp, err := e.HTTPClient.Post(e.ServerURL+"/api/v1/sessions/"+sessionID+"/extend", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ExtendSession failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}
}

// WaitForStatus waits for a session to reach the expected status
func (e *TestEnv) WaitForStatus(t *testing.T, sessionID string, expectedStatus string, timeout time.Duration) *SessionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastSession *SessionResponse

	for {
		select {
		case <-ctx.Done():
			status := "unknown"
			if lastSession != nil {
				status = lastSession.Status
			}
			t.Fatalf("Session %s did not reach status %q within %v (last status: %s)", sessionID, expectedStatus, timeout, status)
		case <-ticker.C:
			session := e.GetSession(t, sessionID)
			lastSession = session
			if session.Status == expectedStatus {
				return session
			}
			// If session failed unexpectedly, fail fast
			if session.Status == "failed" && expectedStatus != "failed" {
				t.Fatalf("Session %s failed unexpectedly: %s", sessionID, session.Error)
			}
		}
	}
}

// WaitForStatusWithRetry waits for a session status, retrying on errors
func (e *TestEnv) WaitForStatusWithRetry(t *testing.T, sessionID string, expectedStatus string, timeout time.Duration) *SessionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastStatus string

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Session %s did not reach status %q within %v (last status: %s)", sessionID, expectedStatus, timeout, lastStatus)
		case <-ticker.C:
			resp, err := e.HTTPClient.Get(e.ServerURL + "/api/v1/sessions/" + sessionID)
			if err != nil {
				continue // Retry on network errors
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusNotFound && expectedStatus == "stopped" {
				// Session deleted, which is acceptable for stopped
				return &SessionResponse{ID: sessionID, Status: "stopped"}
			}

			if resp.StatusCode != http.StatusOK {
				continue // Retry on server errors
			}

			var session SessionResponse
			if err := json.Unmarshal(body, &session); err != nil {
				continue
			}

			lastStatus = session.Status
			if session.Status == expectedStatus {
				return &session
			}
		}
	}
}

// CreateOrphanInstance creates an orphan instance in the mock provider
func (e *TestEnv) CreateOrphanInstance(t *testing.T, label string) string {
	t.Helper()

	reqBody := map[string]string{"label": label}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	resp, err := e.HTTPClient.Post(e.MockProviderURL+"/_test/orphan", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	require.NoError(t, json.Unmarshal(respBody, &result))
	return result["instance_id"]
}

// AssertSessionStatus asserts the session has the expected status
func (e *TestEnv) AssertSessionStatus(t *testing.T, sessionID string, expectedStatus string) {
	t.Helper()

	session := e.GetSession(t, sessionID)
	if session.Status != expectedStatus {
		t.Errorf("Expected session %s status %q, got %q", sessionID, expectedStatus, session.Status)
	}
}

// Cleanup performs test cleanup
func (e *TestEnv) Cleanup(t *testing.T, sessionIDs ...string) {
	t.Helper()

	for _, sessionID := range sessionIDs {
		// Try to delete, ignore errors
		req, _ := http.NewRequest(http.MethodDelete, e.ServerURL+"/api/v1/sessions/"+sessionID, nil)
		resp, err := e.HTTPClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Reset mock provider
	e.ResetMockProvider(t)
}

// GenerateConsumerID generates a unique consumer ID for testing
func GenerateConsumerID() string {
	return fmt.Sprintf("e2e-consumer-%d", time.Now().UnixNano())
}

// GenerateSessionLabel generates a unique session label
func GenerateSessionLabel() string {
	return fmt.Sprintf("shopper-e2e-test-%d", time.Now().UnixNano())
}

// RunReconciliation triggers the reconciler to run
func (e *TestEnv) RunReconciliation(t *testing.T) {
	t.Helper()

	if testReconciler != nil {
		testReconciler.RunReconciliation(context.Background())
	}
}

// GetReconcileMetrics returns the current reconciliation metrics
func (e *TestEnv) GetReconcileMetrics(t *testing.T) (orphansFound, orphansDestroyed, ghostsFound, ghostsFixed int64) {
	t.Helper()

	if testReconciler != nil {
		metrics := testReconciler.GetMetrics()
		return metrics.OrphansFound, metrics.OrphansDestroyed, metrics.GhostsFound, metrics.GhostsFixed
	}
	return 0, 0, 0, 0
}

// DeleteInstanceFromProvider deletes an instance directly from the mock provider (bypassing API)
func (e *TestEnv) DeleteInstanceFromProvider(t *testing.T, instanceID string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodDelete, e.MockProviderURL+"/instances/"+instanceID+"/", nil)
	require.NoError(t, err)

	resp, err := e.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Failed to delete instance from provider")
}

// ListProviderInstances lists all instances from the mock provider
func (e *TestEnv) ListProviderInstances(t *testing.T) []string {
	t.Helper()

	resp, err := e.HTTPClient.Get(e.MockProviderURL + "/instances/")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		Instances []struct {
			ID int `json:"id"`
		} `json:"instances"`
	}

	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	ids := make([]string, len(result.Instances))
	for i, inst := range result.Instances {
		ids[i] = fmt.Sprintf("%d", inst.ID)
	}
	return ids
}
