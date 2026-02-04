package cmd

// CLI Test Suite - Global State Management
//
// This test file manages global state carefully to prevent test pollution.
// The CLI package uses package-level variables for cobra flags, which creates
// shared mutable state between tests.
//
// Design decisions:
//
// 1. Global State Protection:
//    - testMu mutex ensures only one test modifies global state at a time
//    - setupTestWithCleanup() must be called at the start of tests that modify state
//    - State is saved before modification and restored via t.Cleanup()
//
// 2. Cleanup Order (LIFO via t.Cleanup):
//    a. Close mock HTTP server (if any)
//    b. Restore saved global state
//    c. Release mutex
//
// 3. Parallel Tests:
//    - Tests that modify global state CANNOT use t.Parallel()
//    - Pure function tests (TestTruncateString, TestParseSessionPath) CAN use t.Parallel()
//    - Table-driven subtests of pure functions can also use t.Parallel()
//
// 4. Environment Variables:
//    - GPU_SHOPPER_URL is saved/restored along with other global state
//    - Other env vars (VASTAI_API_KEY, etc.) are not modified by these tests

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// testMu protects global state during tests that cannot run in parallel.
// All tests that modify package-level variables must hold this mutex.
var testMu sync.Mutex

// globalStateSnapshot holds a snapshot of all global state variables for save/restore.
// This is used to ensure tests can restore state after modification.
type globalStateSnapshot struct {
	serverURL    string
	outputFormat string

	// inventory flags
	inventoryProvider    string
	inventoryGPUType     string
	inventoryMaxPrice    float64
	inventoryMinVRAM     int
	inventoryMinGPUCount int

	// provision flags
	provisionConsumerID  string
	provisionOfferID     string
	provisionWorkload    string
	provisionHours       int
	provisionIdleTimeout int
	provisionStorage     string
	provisionSaveKey     string
	provisionGPUType     string

	// sessions flags
	sessionsConsumerID string
	sessionsStatus     string
	extendHours        int

	// costs flags
	costsConsumerID string
	costsSessionID  string
	costsPeriod     string
	costsStartDate  string
	costsEndDate    string

	// shutdown flags
	shutdownForce bool

	// cleanup flags
	cleanupExecute  bool
	cleanupForce    bool
	cleanupProvider string

	// transfer flags
	transferKeyFile string
	transferTimeout time.Duration

	// environment variables that might be set
	envGPUShopperURL string
}

// saveGlobalState captures all current global state into a snapshot.
// This must be called while holding testMu.
func saveGlobalState() globalStateSnapshot {
	return globalStateSnapshot{
		serverURL:            serverURL,
		outputFormat:         outputFormat,
		inventoryProvider:    inventoryProvider,
		inventoryGPUType:     inventoryGPUType,
		inventoryMaxPrice:    inventoryMaxPrice,
		inventoryMinVRAM:     inventoryMinVRAM,
		inventoryMinGPUCount: inventoryMinGPUCount,
		provisionConsumerID:  provisionConsumerID,
		provisionOfferID:     provisionOfferID,
		provisionWorkload:    provisionWorkload,
		provisionHours:       provisionHours,
		provisionIdleTimeout: provisionIdleTimeout,
		provisionStorage:     provisionStorage,
		provisionSaveKey:     provisionSaveKey,
		provisionGPUType:     provisionGPUType,
		sessionsConsumerID:   sessionsConsumerID,
		sessionsStatus:       sessionsStatus,
		extendHours:          extendHours,
		costsConsumerID:      costsConsumerID,
		costsSessionID:       costsSessionID,
		costsPeriod:          costsPeriod,
		costsStartDate:       costsStartDate,
		costsEndDate:         costsEndDate,
		shutdownForce:        shutdownForce,
		cleanupExecute:       cleanupExecute,
		cleanupForce:         cleanupForce,
		cleanupProvider:      cleanupProvider,
		transferKeyFile:      transferKeyFile,
		transferTimeout:      transferTimeout,
		envGPUShopperURL:     os.Getenv("GPU_SHOPPER_URL"),
	}
}

// restoreGlobalState restores all global state from a snapshot.
// This must be called while still holding testMu (before unlock).
func restoreGlobalState(saved globalStateSnapshot) {
	serverURL = saved.serverURL
	outputFormat = saved.outputFormat
	inventoryProvider = saved.inventoryProvider
	inventoryGPUType = saved.inventoryGPUType
	inventoryMaxPrice = saved.inventoryMaxPrice
	inventoryMinVRAM = saved.inventoryMinVRAM
	inventoryMinGPUCount = saved.inventoryMinGPUCount
	provisionConsumerID = saved.provisionConsumerID
	provisionOfferID = saved.provisionOfferID
	provisionWorkload = saved.provisionWorkload
	provisionHours = saved.provisionHours
	provisionIdleTimeout = saved.provisionIdleTimeout
	provisionStorage = saved.provisionStorage
	provisionSaveKey = saved.provisionSaveKey
	provisionGPUType = saved.provisionGPUType
	sessionsConsumerID = saved.sessionsConsumerID
	sessionsStatus = saved.sessionsStatus
	extendHours = saved.extendHours
	costsConsumerID = saved.costsConsumerID
	costsSessionID = saved.costsSessionID
	costsPeriod = saved.costsPeriod
	costsStartDate = saved.costsStartDate
	costsEndDate = saved.costsEndDate
	shutdownForce = saved.shutdownForce
	cleanupExecute = saved.cleanupExecute
	cleanupForce = saved.cleanupForce
	cleanupProvider = saved.cleanupProvider
	transferKeyFile = saved.transferKeyFile
	transferTimeout = saved.transferTimeout

	// Restore environment variable
	if saved.envGPUShopperURL != "" {
		os.Setenv("GPU_SHOPPER_URL", saved.envGPUShopperURL)
	} else {
		os.Unsetenv("GPU_SHOPPER_URL")
	}
}

// resetGlobalStateToDefaults resets all global state to safe test defaults.
// This must be called while holding testMu.
func resetGlobalStateToDefaults() {
	serverURL = "http://localhost:8080"
	outputFormat = "table"
	inventoryProvider = ""
	inventoryGPUType = ""
	inventoryMaxPrice = 0
	inventoryMinVRAM = 0
	inventoryMinGPUCount = 0
	provisionConsumerID = ""
	provisionOfferID = ""
	provisionWorkload = "llm"
	provisionHours = 2
	provisionIdleTimeout = 0
	provisionStorage = "destroy"
	provisionSaveKey = ""
	provisionGPUType = ""
	sessionsConsumerID = ""
	sessionsStatus = ""
	extendHours = 1
	costsConsumerID = ""
	costsSessionID = ""
	costsPeriod = ""
	costsStartDate = ""
	costsEndDate = ""
	shutdownForce = false
	cleanupExecute = false
	cleanupForce = false
	cleanupProvider = ""
	transferKeyFile = ""
	transferTimeout = 5 * time.Minute
}

// setupTestWithCleanup sets up a test with proper global state management.
// It acquires the mutex, saves current state, resets to defaults, and registers
// cleanup to restore state and release the mutex in LIFO order.
//
// Note: Tests using this helper CANNOT run in parallel (t.Parallel()) because
// they share package-level global state. The mutex ensures safe sequential access.
//
// Cleanup order (LIFO via t.Cleanup):
// 1. Restore saved global state
// 2. Release mutex
func setupTestWithCleanup(t *testing.T) {
	t.Helper()

	testMu.Lock()
	saved := saveGlobalState()
	resetGlobalStateToDefaults()

	// Register cleanup in LIFO order - restore state first, then unlock
	// t.Cleanup runs registered functions in LIFO order
	t.Cleanup(func() {
		restoreGlobalState(saved)
		testMu.Unlock()
	})
}

// setupMockServer sets up a mock HTTP server and configures the serverURL global.
// Must be called after setupTestWithCleanup to ensure proper state management.
// The server is automatically closed via t.Cleanup (LIFO order ensures this
// runs before state restoration).
func setupMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(handler)
	// Register cleanup - will run before the cleanup registered by setupTestWithCleanup
	// due to LIFO ordering, which is correct (close server before restoring state)
	t.Cleanup(func() {
		server.Close()
	})
	serverURL = server.URL
	return server
}

// captureOutput captures stdout during function execution
func captureOutput(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// Sample mock data
var mockOffer = map[string]interface{}{
	"id":             "offer-456",
	"provider":       "vastai",
	"provider_id":    "v-12345",
	"gpu_type":       "RTX4090",
	"gpu_count":      1,
	"vram_gb":        24,
	"price_per_hour": 0.45,
	"location":       "US",
	"reliability":    0.95,
	"available":      true,
}

var mockSession = map[string]interface{}{
	"id":             "sess-123",
	"consumer_id":    "consumer-1",
	"provider":       "vastai",
	"gpu_type":       "RTX4090",
	"gpu_count":      1,
	"status":         "running",
	"workload_type":  "llm",
	"price_per_hour": 0.50,
	"created_at":     "2024-01-30T10:00:00Z",
	"expires_at":     "2024-01-30T12:00:00Z",
	"ssh_host":       "192.168.1.100",
	"ssh_port":       22,
	"ssh_user":       "root",
}

// TestInventoryCommand tests the inventory command with sample offers
func TestInventoryCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/inventory" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		response := map[string]interface{}{
			"offers": []interface{}{mockOffer},
			"count":  1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	output := captureOutput(func() {
		err := runInventory(nil, nil)
		if err != nil {
			t.Errorf("runInventory returned error: %v", err)
		}
	})

	// Verify output contains expected data
	if !strings.Contains(output, "offer-456") {
		t.Errorf("expected output to contain offer ID, got: %s", output)
	}
	if !strings.Contains(output, "RTX4090") {
		t.Errorf("expected output to contain GPU type, got: %s", output)
	}
	if !strings.Contains(output, "vastai") {
		t.Errorf("expected output to contain provider, got: %s", output)
	}
	if !strings.Contains(output, "Total: 1 offers") {
		t.Errorf("expected output to contain count, got: %s", output)
	}
}

// TestInventoryCommand_WithFilters tests the inventory command with provider and GPU filters
func TestInventoryCommand_WithFilters(t *testing.T) {
	setupTestWithCleanup(t)
	var capturedQuery string
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery

		response := map[string]interface{}{
			"offers": []interface{}{mockOffer},
			"count":  1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Set filters
	inventoryProvider = "tensordock"
	inventoryGPUType = "A100"
	inventoryMaxPrice = 1.50
	inventoryMinVRAM = 40
	inventoryMinGPUCount = 2

	output := captureOutput(func() {
		err := runInventory(nil, nil)
		if err != nil {
			t.Errorf("runInventory returned error: %v", err)
		}
	})

	// Verify query parameters were sent
	if !strings.Contains(capturedQuery, "provider=tensordock") {
		t.Errorf("expected provider filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "gpu_type=A100") {
		t.Errorf("expected gpu_type filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "max_price=1.50") {
		t.Errorf("expected max_price filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "min_vram=40") {
		t.Errorf("expected min_vram filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "min_gpu_count=2") {
		t.Errorf("expected min_gpu_count filter in query, got: %s", capturedQuery)
	}

	if output == "" {
		t.Error("expected non-empty output")
	}
}

// TestInventoryCommand_Empty tests the inventory command when no offers are found
func TestInventoryCommand_Empty(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"offers": []interface{}{},
			"count":  0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	output := captureOutput(func() {
		err := runInventory(nil, nil)
		if err != nil {
			t.Errorf("runInventory returned error: %v", err)
		}
	})

	if !strings.Contains(output, "No offers found") {
		t.Errorf("expected 'No offers found' message, got: %s", output)
	}
}

// TestInventoryCommand_JSON tests the inventory command with JSON output
func TestInventoryCommand_JSON(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"offers": []interface{}{mockOffer},
			"count":  1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	outputFormat = "json"

	output := captureOutput(func() {
		err := runInventory(nil, nil)
		if err != nil {
			t.Errorf("runInventory returned error: %v", err)
		}
	})

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Errorf("expected valid JSON output, got error: %v", err)
	}
}

// TestProvisionCommand_WithOffer tests the provision command with a specific offer
func TestProvisionCommand_WithOffer(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		// Verify request body
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody["offer_id"] != "offer-456" {
			t.Errorf("expected offer_id 'offer-456', got: %v", reqBody["offer_id"])
		}
		if reqBody["consumer_id"] != "test-consumer" {
			t.Errorf("expected consumer_id 'test-consumer', got: %v", reqBody["consumer_id"])
		}

		response := SessionResponse{
			Session: Session{
				ID:           "sess-123",
				ConsumerID:   "test-consumer",
				Provider:     "vastai",
				GPUType:      "RTX4090",
				GPUCount:     1,
				Status:       "provisioning",
				WorkloadType: "llm",
				PricePerHour: 0.45,
				CreatedAt:    "2024-01-30T10:00:00Z",
				ExpiresAt:    "2024-01-30T12:00:00Z",
			},
			SSHPrivateKey: "-----BEGIN RSA PRIVATE KEY-----\ntest-key\n-----END RSA PRIVATE KEY-----",
		}
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Set up provision flags
	provisionConsumerID = "test-consumer"
	provisionOfferID = "offer-456"

	output := captureOutput(func() {
		err := runProvision(nil, nil)
		if err != nil {
			t.Errorf("runProvision returned error: %v", err)
		}
	})

	// Verify output contains expected data
	if !strings.Contains(output, "Session provisioned successfully") {
		t.Errorf("expected success message, got: %s", output)
	}
	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "RTX4090") {
		t.Errorf("expected GPU type in output, got: %s", output)
	}
}

// TestProvisionCommand_WithGPU tests the provision command with GPU auto-selection
func TestProvisionCommand_WithGPU(t *testing.T) {
	setupTestWithCleanup(t)
	callCount := 0
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if r.URL.Path == "/api/v1/inventory" {
			// Inventory request for auto-select
			response := map[string]interface{}{
				"offers": []interface{}{mockOffer},
				"count":  1,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		if r.URL.Path == "/api/v1/sessions" {
			// Session creation request
			response := SessionResponse{
				Session: Session{
					ID:           "sess-auto",
					ConsumerID:   "test-consumer",
					Provider:     "vastai",
					GPUType:      "RTX4090",
					GPUCount:     1,
					Status:       "provisioning",
					WorkloadType: "llm",
					PricePerHour: 0.45,
					CreatedAt:    "2024-01-30T10:00:00Z",
					ExpiresAt:    "2024-01-30T12:00:00Z",
				},
			}
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		t.Errorf("unexpected path: %s", r.URL.Path)
	})

	// Set up provision flags with GPU type instead of offer ID
	provisionConsumerID = "test-consumer"
	provisionGPUType = "RTX4090"

	output := captureOutput(func() {
		err := runProvision(nil, nil)
		if err != nil {
			t.Errorf("runProvision returned error: %v", err)
		}
	})

	// Should have made 2 calls: inventory + sessions
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got: %d", callCount)
	}

	// Verify auto-selection message
	if !strings.Contains(output, "Auto-selected offer") {
		t.Errorf("expected auto-selection message, got: %s", output)
	}
}

// TestProvisionCommand_InvalidWorkload tests that invalid workload types are rejected
func TestProvisionCommand_InvalidWorkload(t *testing.T) {
	setupTestWithCleanup(t)
	// No server needed - validation happens before request

	provisionConsumerID = "test-consumer"
	provisionOfferID = "offer-456"
	provisionWorkload = "invalid-workload"

	err := runProvision(nil, nil)
	if err == nil {
		t.Error("expected error for invalid workload type")
	}
	if !strings.Contains(err.Error(), "invalid workload type") {
		t.Errorf("expected 'invalid workload type' error, got: %v", err)
	}
}

// TestProvisionCommand_NoOfferOrGPU tests that provision fails without offer or GPU
func TestProvisionCommand_NoOfferOrGPU(t *testing.T) {
	setupTestWithCleanup(t)

	provisionConsumerID = "test-consumer"

	err := runProvision(nil, nil)
	if err == nil {
		t.Error("expected error when neither offer nor GPU provided")
	}
	if !strings.Contains(err.Error(), "either --offer or --gpu must be provided") {
		t.Errorf("expected '--offer or --gpu' error, got: %v", err)
	}
}

// TestSessionsListCommand tests the sessions list command
func TestSessionsListCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		response := map[string]interface{}{
			"sessions": []interface{}{mockSession},
			"count":    1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	output := captureOutput(func() {
		err := runSessionsList(nil, nil)
		if err != nil {
			t.Errorf("runSessionsList returned error: %v", err)
		}
	})

	// Verify output
	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "consumer-1") {
		t.Errorf("expected consumer ID in output, got: %s", output)
	}
	if !strings.Contains(output, "running") {
		t.Errorf("expected status in output, got: %s", output)
	}
}

// TestSessionsListCommand_WithFilters tests sessions list with consumer and status filters
func TestSessionsListCommand_WithFilters(t *testing.T) {
	setupTestWithCleanup(t)
	var capturedQuery string
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery

		response := map[string]interface{}{
			"sessions": []interface{}{mockSession},
			"count":    1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	sessionsConsumerID = "consumer-1"
	sessionsStatus = "running"

	captureOutput(func() {
		err := runSessionsList(nil, nil)
		if err != nil {
			t.Errorf("runSessionsList returned error: %v", err)
		}
	})

	// Verify query parameters
	if !strings.Contains(capturedQuery, "consumer_id=consumer-1") {
		t.Errorf("expected consumer_id filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "status=running") {
		t.Errorf("expected status filter in query, got: %s", capturedQuery)
	}
}

// TestSessionsListCommand_Empty tests sessions list when no sessions exist
func TestSessionsListCommand_Empty(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"sessions": []interface{}{},
			"count":    0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	output := captureOutput(func() {
		err := runSessionsList(nil, nil)
		if err != nil {
			t.Errorf("runSessionsList returned error: %v", err)
		}
	})

	if !strings.Contains(output, "No sessions found") {
		t.Errorf("expected 'No sessions found' message, got: %s", output)
	}
}

// TestSessionsGetCommand tests getting a specific session
func TestSessionsGetCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/sess-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockSession)
	})

	output := captureOutput(func() {
		err := runSessionsGet(nil, []string{"sess-123"})
		if err != nil {
			t.Errorf("runSessionsGet returned error: %v", err)
		}
	})

	// Verify output
	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "consumer-1") {
		t.Errorf("expected consumer ID in output, got: %s", output)
	}
	if !strings.Contains(output, "running") {
		t.Errorf("expected status in output, got: %s", output)
	}
	if !strings.Contains(output, "SSH Connection") {
		t.Errorf("expected SSH connection info in output, got: %s", output)
	}
}

// TestSessionsGetCommand_NotFound tests getting a non-existent session
func TestSessionsGetCommand_NotFound(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "session not found"}`))
	})

	err := runSessionsGet(nil, []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for non-existent session")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Errorf("expected 'session not found' error, got: %v", err)
	}
}

// TestSessionsGetCommand_JSON tests getting a session with JSON output
func TestSessionsGetCommand_JSON(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockSession)
	})

	outputFormat = "json"

	output := captureOutput(func() {
		err := runSessionsGet(nil, []string{"sess-123"})
		if err != nil {
			t.Errorf("runSessionsGet returned error: %v", err)
		}
	})

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Errorf("expected valid JSON output, got error: %v", err)
	}
}

// TestSessionsDoneCommand tests signaling session completion
func TestSessionsDoneCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/sess-123/done" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "shutting_down"}`))
	})

	output := captureOutput(func() {
		err := runSessionsDone(nil, []string{"sess-123"})
		if err != nil {
			t.Errorf("runSessionsDone returned error: %v", err)
		}
	})

	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "shutdown initiated") {
		t.Errorf("expected 'shutdown initiated' message, got: %s", output)
	}
}

// TestSessionsDoneCommand_Error tests session done when request fails
func TestSessionsDoneCommand_Error(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "session already terminated"}`))
	})

	err := runSessionsDone(nil, []string{"sess-123"})
	if err == nil {
		t.Error("expected error for failed done request")
	}
	if !strings.Contains(err.Error(), "failed to signal done") {
		t.Errorf("expected 'failed to signal done' error, got: %v", err)
	}
}

// TestSessionsDeleteCommand tests force-deleting a session
func TestSessionsDeleteCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/sess-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "destroyed"}`))
	})

	output := captureOutput(func() {
		err := runSessionsDelete(nil, []string{"sess-123"})
		if err != nil {
			t.Errorf("runSessionsDelete returned error: %v", err)
		}
	})

	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "destroyed") {
		t.Errorf("expected 'destroyed' message, got: %s", output)
	}
}

// TestSessionsDeleteCommand_Error tests session delete when request fails
func TestSessionsDeleteCommand_Error(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "failed to destroy instance"}`))
	})

	err := runSessionsDelete(nil, []string{"sess-123"})
	if err == nil {
		t.Error("expected error for failed delete request")
	}
	if !strings.Contains(err.Error(), "failed to delete session") {
		t.Errorf("expected 'failed to delete session' error, got: %v", err)
	}
}

// TestSessionsExtendCommand tests extending a session
func TestSessionsExtendCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/sess-123/extend" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		// Verify request body
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody["additional_hours"] != float64(2) {
			t.Errorf("expected additional_hours 2, got: %v", reqBody["additional_hours"])
		}

		response := map[string]interface{}{
			"status":         "extended",
			"new_expires_at": "2024-01-30T14:00:00Z",
		}
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	extendHours = 2

	output := captureOutput(func() {
		err := runSessionsExtend(nil, []string{"sess-123"})
		if err != nil {
			t.Errorf("runSessionsExtend returned error: %v", err)
		}
	})

	if !strings.Contains(output, "sess-123") {
		t.Errorf("expected session ID in output, got: %s", output)
	}
	if !strings.Contains(output, "extended by 2 hours") {
		t.Errorf("expected 'extended by 2 hours' message, got: %s", output)
	}
	if !strings.Contains(output, "New expiration") {
		t.Errorf("expected 'New expiration' in output, got: %s", output)
	}
}

// TestCostsCommand tests the costs command
func TestCostsCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/costs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		response := CostSummary{
			TotalCost:    150.50,
			SessionCount: 10,
			HoursUsed:    45.5,
			ByProvider: map[string]float64{
				"vastai":     100.00,
				"tensordock": 50.50,
			},
			ByGPUType: map[string]float64{
				"RTX4090": 80.00,
				"A100":    70.50,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	output := captureOutput(func() {
		err := runCosts(nil, nil)
		if err != nil {
			t.Errorf("runCosts returned error: %v", err)
		}
	})

	// Verify output
	if !strings.Contains(output, "Cost Summary") {
		t.Errorf("expected 'Cost Summary' header, got: %s", output)
	}
	if !strings.Contains(output, "$150.50") {
		t.Errorf("expected total cost in output, got: %s", output)
	}
	if !strings.Contains(output, "10") {
		t.Errorf("expected session count in output, got: %s", output)
	}
}

// TestCostsCommand_WithFilters tests the costs command with filters
func TestCostsCommand_WithFilters(t *testing.T) {
	setupTestWithCleanup(t)
	var capturedQuery string
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery

		response := CostSummary{
			ConsumerID:   "consumer-1",
			TotalCost:    50.00,
			SessionCount: 5,
			HoursUsed:    20.0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	costsConsumerID = "consumer-1"
	costsSessionID = "sess-123"

	captureOutput(func() {
		err := runCosts(nil, nil)
		if err != nil {
			t.Errorf("runCosts returned error: %v", err)
		}
	})

	// Verify query parameters
	if !strings.Contains(capturedQuery, "consumer_id=consumer-1") {
		t.Errorf("expected consumer_id filter in query, got: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "session_id=sess-123") {
		t.Errorf("expected session_id filter in query, got: %s", capturedQuery)
	}
}

// TestCostsSummaryCommand tests the costs summary command
func TestCostsSummaryCommand(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/costs/summary" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		response := CostSummary{
			TotalCost:    500.00,
			SessionCount: 50,
			HoursUsed:    200.0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	output := captureOutput(func() {
		err := runCostsSummary(nil, nil)
		if err != nil {
			t.Errorf("runCostsSummary returned error: %v", err)
		}
	})

	if !strings.Contains(output, "$500.00") {
		t.Errorf("expected total cost in output, got: %s", output)
	}
}

// TestServerConnectionError tests handling when server is unreachable
func TestServerConnectionError(t *testing.T) {
	setupTestWithCleanup(t)
	// Point to non-existent server
	serverURL = "http://localhost:1"

	err := runInventory(nil, nil)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "failed to connect to server") {
		t.Errorf("expected 'failed to connect to server' error, got: %v", err)
	}
}

// TestServerErrorResponse tests handling of non-200 server responses
func TestServerErrorResponse(t *testing.T) {
	setupTestWithCleanup(t)
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	})

	err := runInventory(nil, nil)
	if err == nil {
		t.Error("expected error for server error response")
	}
	if !strings.Contains(err.Error(), "server error") {
		t.Errorf("expected 'server error' in error message, got: %v", err)
	}
}

// =============================================================================
// Parallel-safe tests for pure functions
// These tests can run in parallel because they don't modify any global state
// =============================================================================

// TestTruncateString tests the truncateString utility function.
// This is a pure function test that can run in parallel.
func TestTruncateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "string shorter than max",
			input:    "hello",
			maxLen:   10,
			expected: "hello",
		},
		{
			name:     "string equal to max",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "string longer than max",
			input:    "hello world",
			maxLen:   8,
			expected: "hello...",
		},
		{
			name:     "very short maxLen",
			input:    "hello",
			maxLen:   3,
			expected: "hel",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   5,
			expected: "",
		},
		{
			name:     "zero maxLen",
			input:    "hello",
			maxLen:   0,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := truncateString(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

// TestParseSessionPath tests the parseSessionPath utility function.
// This is a pure function test that can run in parallel.
func TestParseSessionPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantSession string
		wantPath    string
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid session path",
			input:       "session-123:/home/user/file.txt",
			wantSession: "session-123",
			wantPath:    "/home/user/file.txt",
			wantErr:     false,
		},
		{
			name:        "valid with spaces trimmed",
			input:       "  session-123  :  /path/to/file  ",
			wantSession: "session-123",
			wantPath:    "/path/to/file",
			wantErr:     false,
		},
		{
			name:        "missing colon",
			input:       "session-123/home/user/file.txt",
			wantErr:     true,
			errContains: "invalid format",
		},
		{
			name:        "empty session ID",
			input:       ":/home/user/file.txt",
			wantErr:     true,
			errContains: "session ID cannot be empty",
		},
		{
			name:        "empty path",
			input:       "session-123:",
			wantErr:     true,
			errContains: "path cannot be empty",
		},
		{
			name:        "session ID with whitespace only",
			input:       "   :/path",
			wantErr:     true,
			errContains: "session ID cannot be empty",
		},
		{
			name:        "path with whitespace only",
			input:       "session:   ",
			wantErr:     true,
			errContains: "path cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotSession, gotPath, err := parseSessionPath(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSessionPath(%q) expected error containing %q, got nil", tt.input, tt.errContains)
					return
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("parseSessionPath(%q) error = %v, want error containing %q", tt.input, err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("parseSessionPath(%q) unexpected error: %v", tt.input, err)
				return
			}

			if gotSession != tt.wantSession {
				t.Errorf("parseSessionPath(%q) session = %q, want %q", tt.input, gotSession, tt.wantSession)
			}
			if gotPath != tt.wantPath {
				t.Errorf("parseSessionPath(%q) path = %q, want %q", tt.input, gotPath, tt.wantPath)
			}
		})
	}
}
