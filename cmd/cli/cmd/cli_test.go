package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Helper to set up mock server and reset serverURL
func setupMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	serverURL = server.URL // Override the package-level serverURL
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
	"id":            "offer-456",
	"provider":      "vastai",
	"provider_id":   "v-12345",
	"gpu_type":      "RTX4090",
	"gpu_count":     1,
	"vram_gb":       24,
	"price_per_hour": 0.45,
	"location":      "US",
	"reliability":   0.95,
	"available":     true,
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

	// Reset flags to defaults
	outputFormat = "table"
	inventoryProvider = ""
	inventoryGPUType = ""
	inventoryMaxPrice = 0
	inventoryMinVRAM = 0
	inventoryMinGPUCount = 0

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
	outputFormat = "table"

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

	// Reset filters
	inventoryProvider = ""
	inventoryGPUType = ""
	inventoryMaxPrice = 0
	inventoryMinVRAM = 0
	inventoryMinGPUCount = 0

	if output == "" {
		t.Error("expected non-empty output")
	}
}

// TestInventoryCommand_Empty tests the inventory command when no offers are found
func TestInventoryCommand_Empty(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"offers": []interface{}{},
			"count":  0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	outputFormat = "table"
	inventoryProvider = ""
	inventoryGPUType = ""

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
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"offers": []interface{}{mockOffer},
			"count":  1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	outputFormat = "json"
	inventoryProvider = ""
	inventoryGPUType = ""

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

	// Reset
	outputFormat = "table"
}

// TestProvisionCommand_WithOffer tests the provision command with a specific offer
func TestProvisionCommand_WithOffer(t *testing.T) {
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
	provisionGPUType = ""
	provisionWorkload = "llm"
	provisionHours = 2
	provisionIdleTimeout = 0
	provisionStorage = "destroy"
	provisionSaveKey = ""
	outputFormat = "table"

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
	provisionOfferID = ""
	provisionGPUType = "RTX4090"
	provisionWorkload = "llm"
	provisionHours = 2
	outputFormat = "table"

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
	// No server needed - validation happens before request

	provisionConsumerID = "test-consumer"
	provisionOfferID = "offer-456"
	provisionWorkload = "invalid-workload"
	outputFormat = "table"

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
	provisionConsumerID = "test-consumer"
	provisionOfferID = ""
	provisionGPUType = ""
	provisionWorkload = "llm"
	outputFormat = "table"

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

	outputFormat = "table"
	sessionsConsumerID = ""
	sessionsStatus = ""

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

	outputFormat = "table"
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

	// Reset
	sessionsConsumerID = ""
	sessionsStatus = ""
}

// TestSessionsListCommand_Empty tests sessions list when no sessions exist
func TestSessionsListCommand_Empty(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"sessions": []interface{}{},
			"count":    0,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	outputFormat = "table"
	sessionsConsumerID = ""
	sessionsStatus = ""

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

	outputFormat = "table"

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
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "session not found"}`))
	})

	outputFormat = "table"

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

	// Reset
	outputFormat = "table"
}

// TestSessionsDoneCommand tests signaling session completion
func TestSessionsDoneCommand(t *testing.T) {
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

	outputFormat = "table"
	costsConsumerID = ""
	costsSessionID = ""

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

	outputFormat = "table"
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

	// Reset
	costsConsumerID = ""
	costsSessionID = ""
}

// TestCostsSummaryCommand tests the costs summary command
func TestCostsSummaryCommand(t *testing.T) {
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

	outputFormat = "table"
	costsConsumerID = ""

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
	// Point to non-existent server
	serverURL = "http://localhost:1"
	outputFormat = "table"

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
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	})

	outputFormat = "table"

	err := runInventory(nil, nil)
	if err == nil {
		t.Error("expected error for server error response")
	}
	if !strings.Contains(err.Error(), "server error") {
		t.Errorf("expected 'server error' in error message, got: %v", err)
	}
}
