package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockStatusProvider implements StatusProvider for testing
type mockStatusProvider struct {
	sessionID         string
	status            string
	idleSeconds       int
	gpuUtil           float64
	memUsed           int
	uptime            time.Duration
	heartbeatFailures int
	shopperReachable  bool
}

func (m *mockStatusProvider) GetSessionID() string       { return m.sessionID }
func (m *mockStatusProvider) GetStatus() string          { return m.status }
func (m *mockStatusProvider) GetIdleSeconds() int        { return m.idleSeconds }
func (m *mockStatusProvider) GetGPUUtilization() float64 { return m.gpuUtil }
func (m *mockStatusProvider) GetMemoryUsedMB() int       { return m.memUsed }
func (m *mockStatusProvider) GetUptime() time.Duration   { return m.uptime }
func (m *mockStatusProvider) GetHeartbeatFailures() int  { return m.heartbeatFailures }
func (m *mockStatusProvider) IsShopperReachable() bool   { return m.shopperReachable }

func TestServer_HealthEndpoint(t *testing.T) {
	server := New("test-session-123",
		WithStatusProvider(&mockStatusProvider{
			sessionID: "test-session-123",
			uptime:    5 * time.Minute,
		}),
	)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp.Status)
	}

	if resp.SessionID != "test-session-123" {
		t.Errorf("expected session ID 'test-session-123', got '%s'", resp.SessionID)
	}

	if resp.Uptime == "" {
		t.Error("expected non-empty uptime")
	}

	if resp.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestServer_StatusEndpoint(t *testing.T) {
	server := New("test-session",
		WithStatusProvider(&mockStatusProvider{
			sessionID:         "test-session",
			status:            "running",
			idleSeconds:       300,
			gpuUtil:           75.5,
			memUsed:           8192,
			uptime:            10 * time.Minute,
			heartbeatFailures: 2,
			shopperReachable:  true,
		}),
	)

	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.SessionID != "test-session" {
		t.Errorf("expected session ID 'test-session', got '%s'", resp.SessionID)
	}

	if resp.Status != "running" {
		t.Errorf("expected status 'running', got '%s'", resp.Status)
	}

	if resp.IdleSeconds != 300 {
		t.Errorf("expected idle seconds 300, got %d", resp.IdleSeconds)
	}

	if resp.GPUUtilization != 75.5 {
		t.Errorf("expected GPU util 75.5, got %f", resp.GPUUtilization)
	}

	if resp.MemoryUsedMB != 8192 {
		t.Errorf("expected memory 8192, got %d", resp.MemoryUsedMB)
	}

	if resp.HeartbeatFailures != 2 {
		t.Errorf("expected heartbeat failures 2, got %d", resp.HeartbeatFailures)
	}

	if !resp.ShopperReachable {
		t.Error("expected shopper reachable to be true")
	}
}

func TestServer_HealthEndpoint_MethodNotAllowed(t *testing.T) {
	server := New("test-session")

	req := httptest.NewRequest("POST", "/health", nil)
	rec := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestServer_StatusEndpoint_MethodNotAllowed(t *testing.T) {
	server := New("test-session")

	req := httptest.NewRequest("PUT", "/status", nil)
	rec := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestServer_NotFound(t *testing.T) {
	server := New("test-session")

	req := httptest.NewRequest("GET", "/unknown", nil)
	rec := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

func TestServer_Shutdown(t *testing.T) {
	server := New("test-session", WithPort(0)) // Use port 0 for random available port

	// Start server in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("shutdown error: %v", err)
	}

	// Check that Start() returned
	select {
	case err := <-errChan:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not stop")
	}
}

func TestServer_DefaultStatusProvider(t *testing.T) {
	// Test that server works with default status provider
	server := New("default-session")

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	server.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.SessionID != "default-session" {
		t.Errorf("expected session ID 'default-session', got '%s'", resp.SessionID)
	}
}

func TestServer_ContentType(t *testing.T) {
	server := New("test-session")

	endpoints := []string{"/health", "/status"}

	for _, endpoint := range endpoints {
		req := httptest.NewRequest("GET", endpoint, nil)
		rec := httptest.NewRecorder()

		server.server.Handler.ServeHTTP(rec, req)

		contentType := rec.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("expected Content-Type 'application/json' for %s, got '%s'", endpoint, contentType)
		}
	}
}
