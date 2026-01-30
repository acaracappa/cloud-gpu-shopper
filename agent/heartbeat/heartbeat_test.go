package heartbeat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// mockStatusProvider provides test status data
type mockStatusProvider struct {
	status       string
	idleSeconds  int
	gpuUtilPct   float64
	memoryUsedMB int
}

func (m *mockStatusProvider) GetStatus() (string, int, float64, int) {
	return m.status, m.idleSeconds, m.gpuUtilPct, m.memoryUsedMB
}

func TestSender_SendsHeartbeat(t *testing.T) {
	receivedHeartbeats := make(chan Request, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/test-session/heartbeat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		receivedHeartbeats <- req

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(50*time.Millisecond),
		WithStatusProvider(&mockStatusProvider{
			status:       "running",
			idleSeconds:  120,
			gpuUtilPct:   85.5,
			memoryUsedMB: 4096,
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	sender.Start(ctx)

	// Wait for at least 2 heartbeats
	select {
	case hb := <-receivedHeartbeats:
		if hb.SessionID != "test-session" {
			t.Errorf("expected session ID 'test-session', got '%s'", hb.SessionID)
		}
		if hb.AgentToken != "test-token" {
			t.Errorf("expected agent token 'test-token', got '%s'", hb.AgentToken)
		}
		if hb.Status != "running" {
			t.Errorf("expected status 'running', got '%s'", hb.Status)
		}
		if hb.IdleSeconds != 120 {
			t.Errorf("expected idle seconds 120, got %d", hb.IdleSeconds)
		}
		if hb.GPUUtilPct != 85.5 {
			t.Errorf("expected GPU util 85.5, got %f", hb.GPUUtilPct)
		}
		if hb.MemoryUsedMB != 4096 {
			t.Errorf("expected memory 4096, got %d", hb.MemoryUsedMB)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for heartbeat")
	}

	<-ctx.Done()
}

func TestSender_IncrementsFailureCount(t *testing.T) {
	failureCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failureCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	sender.Start(ctx)
	<-ctx.Done()

	if sender.GetFailureCount() == 0 {
		t.Error("expected failure count to be > 0")
	}
}

func TestSender_ResetsFailureCountOnSuccess(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		// Fail first 2 calls, then succeed
		if count <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	sender.Start(ctx)
	<-ctx.Done()

	// After success, failure count should be reset to 0
	if sender.GetFailureCount() != 0 {
		t.Errorf("expected failure count to be 0 after success, got %d", sender.GetFailureCount())
	}
}

func TestSender_TriggersFailsafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	failsafeTriggered := make(chan struct{}, 1)

	// Create sender using New() to ensure logger is set
	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(10*time.Millisecond),
		WithFailsafeHandler(func() {
			select {
			case failsafeTriggered <- struct{}{}:
			default:
			}
		}),
	)

	// Simulate enough failures to trigger failsafe
	for i := 0; i < DefaultUnreachableThreshold; i++ {
		sender.sendHeartbeat()
	}

	select {
	case <-failsafeTriggered:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("failsafe was not triggered")
	}
}

func TestSender_HandlesUnreachableServer(t *testing.T) {
	// Use an invalid URL that will fail to connect
	sender := New(
		"http://127.0.0.1:1", // Port 1 should always fail
		"test-session",
		"test-token",
		WithInterval(20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	sender.Start(ctx)
	<-ctx.Done()

	if sender.GetFailureCount() == 0 {
		t.Error("expected failure count to be > 0 for unreachable server")
	}
}

func TestSender_IsRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(50*time.Millisecond),
	)

	if sender.IsRunning() {
		t.Error("sender should not be running before Start()")
	}

	ctx, cancel := context.WithCancel(context.Background())
	sender.Start(ctx)

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	if !sender.IsRunning() {
		t.Error("sender should be running after Start()")
	}

	cancel()
	// Give it time to stop
	time.Sleep(100 * time.Millisecond)

	if sender.IsRunning() {
		t.Error("sender should not be running after context cancel")
	}
}

func TestSender_OnlyStartsOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(100*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start multiple times
	sender.Start(ctx)
	sender.Start(ctx)
	sender.Start(ctx)

	// Should still only be running once
	time.Sleep(50 * time.Millisecond)
	if !sender.IsRunning() {
		t.Error("sender should be running")
	}
}

func TestSender_CustomUnreachableThreshold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	failsafeTriggered := make(chan struct{}, 1)
	customThreshold := 5

	sender := New(
		server.URL,
		"test-session",
		"test-token",
		WithInterval(10*time.Millisecond),
		WithUnreachableThreshold(customThreshold),
		WithFailsafeHandler(func() {
			select {
			case failsafeTriggered <- struct{}{}:
			default:
			}
		}),
	)

	// Verify threshold was set
	if sender.GetUnreachableThreshold() != customThreshold {
		t.Errorf("expected threshold %d, got %d", customThreshold, sender.GetUnreachableThreshold())
	}

	// Trigger failures up to threshold
	for i := 0; i < customThreshold; i++ {
		sender.sendHeartbeat()
	}

	select {
	case <-failsafeTriggered:
		// Success - failsafe triggered at custom threshold
	case <-time.After(100 * time.Millisecond):
		t.Error("failsafe was not triggered at custom threshold")
	}
}
