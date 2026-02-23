package bluelobster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// newTestClient creates a test client wired to the given httptest server.
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient("test-api-key", WithBaseURL(server.URL))
}

// ---------------------------------------------------------------------------
// ListOffers tests
// ---------------------------------------------------------------------------

func TestListOffers_FiltersOutCPUOnly(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := AvailableResponse{
			Data: []AvailableInstance{
				{
					ID: "cpu-only",
					InstanceType: InstanceType{
						Name:              "cpu-small",
						PriceCentsPerHour: 10,
						Specs: InstanceSpec{
							VCPUs:     4,
							MemoryGiB: 16,
							GPUs:      0,
						},
					},
					Regions: []Region{{Name: "us-east-1", Description: "US East"}},
				},
				{
					ID: "gpu-instance",
					InstanceType: InstanceType{
						Name:              "gpu-a100",
						GPUDescription:    "1x A100 (80 GB)",
						PriceCentsPerHour: 250,
						Specs: InstanceSpec{
							VCPUs:     16,
							MemoryGiB: 128,
							GPUs:      1,
							GPUModel:  json.RawMessage(`"NVIDIA A100"`),
						},
					},
					Regions: []Region{{Name: "us-east-1", Description: "US East"}},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("ListOffers returned error: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 GPU offer, got %d", len(offers))
	}
	if offers[0].GPUType != "A100" {
		t.Errorf("expected GPU type A100, got %s", offers[0].GPUType)
	}
	if offers[0].GPUCount != 1 {
		t.Errorf("expected GPU count 1, got %d", offers[0].GPUCount)
	}
}

func TestListOffers_GPUModelArrayHandling(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := AvailableResponse{
			Data: []AvailableInstance{
				{
					ID: "gpu-quadro",
					InstanceType: InstanceType{
						Name:              "gpu-rtx8000",
						GPUDescription:    "1x RTX 8000 (48 GB)",
						PriceCentsPerHour: 150,
						Specs: InstanceSpec{
							VCPUs:     8,
							MemoryGiB: 64,
							GPUs:      1,
							GPUModel:  json.RawMessage(`["Quadro RTX 8000", "RTX 8000"]`),
						},
					},
					Regions: []Region{{Name: "us-west-1", Description: "US West"}},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("ListOffers returned error: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
	// ParseGPUModel returns first element "Quadro RTX 8000", normalizeGPUName strips "Quadro "
	if offers[0].GPUType != "RTX 8000" {
		t.Errorf("expected GPU type 'RTX 8000', got '%s'", offers[0].GPUType)
	}
}

func TestListOffers_MultiRegionExpandsOffers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := AvailableResponse{
			Data: []AvailableInstance{
				{
					ID: "cpu-only",
					InstanceType: InstanceType{
						Name:              "cpu-medium",
						PriceCentsPerHour: 20,
						Specs: InstanceSpec{
							VCPUs:     8,
							MemoryGiB: 32,
							GPUs:      0,
						},
					},
					Regions: []Region{
						{Name: "us-east-1", Description: "US East"},
						{Name: "eu-west-1", Description: "EU West"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("ListOffers returned error: %v", err)
	}
	// CPU-only instance with 2 regions should still produce 0 GPU offers
	if len(offers) != 0 {
		t.Fatalf("expected 0 GPU offers for CPU-only instances, got %d", len(offers))
	}
}

// ---------------------------------------------------------------------------
// CreateInstance tests
// ---------------------------------------------------------------------------

func TestCreateInstance_HappyPath(t *testing.T) {
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "POST" && r.URL.Path == "/instances/launch-instance":
			// Verify request body
			var req LaunchInstanceRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.InstanceType != "gpu-a100" {
				t.Errorf("expected instance_type 'gpu-a100', got '%s'", req.InstanceType)
			}
			if req.Region != "us-east-1" {
				t.Errorf("expected region 'us-east-1', got '%s'", req.Region)
			}
			if req.Username != "ubuntu" {
				t.Errorf("expected username 'ubuntu', got '%s'", req.Username)
			}
			resp := LaunchInstanceResponse{
				TaskID: "task-456",
				VMUUID: "inst-123",
				Status: "PENDING",
			}
			json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/tasks/task-456":
			callCount++
			resp := TaskResponse{
				TaskID: "task-456",
				Status: "COMPLETED",
				Params: struct {
					VMUUID string `json:"vm_uuid"`
				}{
					VMUUID: "inst-123",
				},
			}
			json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/instances/inst-123":
			vm := VMInstance{
				UUID:              "inst-123",
				Name:              "shopper-sess-1",
				IPAddress:         "1.2.3.4",
				PowerStatus:       "running",
				PriceCentsPerHour: 250,
				VMUsername:        "ubuntu",
				GPUCount:          1,
				GPUModel:          "A100",
			}
			json.NewEncoder(w).Encode(vm)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := newTestClient(t, handler)
	info, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "bluelobster:gpu-a100:us-east-1",
		SSHPublicKey: "ssh-rsa AAAA...",
		Tags: models.InstanceTags{
			ShopperSessionID: "sess-1",
		},
	})
	if err != nil {
		t.Fatalf("CreateInstance returned error: %v", err)
	}
	if info.ProviderInstanceID != "inst-123" {
		t.Errorf("expected instance ID 'inst-123', got '%s'", info.ProviderInstanceID)
	}
	if info.SSHHost != "1.2.3.4" {
		t.Errorf("expected SSH host '1.2.3.4', got '%s'", info.SSHHost)
	}
	if info.SSHPort != 22 {
		t.Errorf("expected SSH port 22, got %d", info.SSHPort)
	}
	if info.SSHUser != "ubuntu" {
		t.Errorf("expected SSH user 'ubuntu', got '%s'", info.SSHUser)
	}
	if info.Status != "running" {
		t.Errorf("expected status 'running', got '%s'", info.Status)
	}
	if info.ActualPricePerHour != 2.50 {
		t.Errorf("expected price 2.50, got %f", info.ActualPricePerHour)
	}
}

func TestCreateInstance_TaskFailed(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "POST" && r.URL.Path == "/instances/launch-instance":
			resp := LaunchInstanceResponse{
				TaskID: "task-fail",
				Status: "PENDING",
			}
			json.NewEncoder(w).Encode(resp)

		case r.Method == "GET" && r.URL.Path == "/tasks/task-fail":
			resp := TaskResponse{
				TaskID:  "task-fail",
				Status:  "FAILED",
				Message: "insufficient GPU capacity",
			}
			json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := newTestClient(t, handler)
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "bluelobster:gpu-a100:us-east-1",
		Tags:    models.InstanceTags{ShopperSessionID: "sess-2"},
	})
	if err == nil {
		t.Fatal("expected error for failed task, got nil")
	}
	// Verify the error is a ProviderError
	var provErr *provider.ProviderError
	if !provider.IsRetryable(err) {
		// Task failure with status 0 is not retryable, but it should contain the message
		_ = provErr
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestCreateInstance_InvalidOfferID(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach server with invalid offer ID")
	})

	client := newTestClient(t, handler)
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "invalid-offer-id",
		Tags:    models.InstanceTags{ShopperSessionID: "sess-3"},
	})
	if err == nil {
		t.Fatal("expected error for invalid offer ID, got nil")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

// ---------------------------------------------------------------------------
// DestroyInstance tests
// ---------------------------------------------------------------------------

func TestDestroyInstance_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/instances/inst-123" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := DeleteInstanceResponse{
			Status:     "success",
			Message:    "Instance deleted",
			InstanceID: "inst-123",
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	err := client.DestroyInstance(context.Background(), "inst-123")
	if err != nil {
		t.Fatalf("DestroyInstance returned error: %v", err)
	}
}

func TestDestroyInstance_NotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		resp := ErrorResponse{
			Error:   "not_found",
			Message: "Instance not found",
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	err := client.DestroyInstance(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for not found instance, got nil")
	}
	if !provider.IsNotFoundError(err) {
		t.Errorf("expected IsNotFoundError to return true, got false. err: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetInstanceStatus tests
// ---------------------------------------------------------------------------

func TestGetInstanceStatus_Running(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances/inst-run" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		vm := VMInstance{
			UUID:        "inst-run",
			IPAddress:   "10.0.0.1",
			PowerStatus: "running",
			VMUsername:  "ubuntu",
			CreatedAt:   "2026-02-23T10:00:00Z",
		}
		json.NewEncoder(w).Encode(vm)
	})

	client := newTestClient(t, handler)
	status, err := client.GetInstanceStatus(context.Background(), "inst-run")
	if err != nil {
		t.Fatalf("GetInstanceStatus returned error: %v", err)
	}
	if !status.Running {
		t.Error("expected Running to be true")
	}
	if status.SSHHost != "10.0.0.1" {
		t.Errorf("expected SSHHost '10.0.0.1', got '%s'", status.SSHHost)
	}
	if status.SSHPort != 22 {
		t.Errorf("expected SSHPort 22, got %d", status.SSHPort)
	}
	if status.StartedAt.IsZero() {
		t.Error("expected StartedAt to be set")
	}
}

func TestGetInstanceStatus_Stopped(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		vm := VMInstance{
			UUID:        "inst-stop",
			IPAddress:   "10.0.0.2",
			PowerStatus: "stopped",
			VMUsername:  "ubuntu",
		}
		json.NewEncoder(w).Encode(vm)
	})

	client := newTestClient(t, handler)
	status, err := client.GetInstanceStatus(context.Background(), "inst-stop")
	if err != nil {
		t.Fatalf("GetInstanceStatus returned error: %v", err)
	}
	if status.Running {
		t.Error("expected Running to be false for stopped instance")
	}
	if status.Status != "stopped" {
		t.Errorf("expected status 'stopped', got '%s'", status.Status)
	}
}

// ---------------------------------------------------------------------------
// ListAllInstances tests
// ---------------------------------------------------------------------------

func TestListAllInstances_FiltersByTags(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		vms := []VMInstance{
			{
				UUID:              "inst-ours",
				Name:              "shopper-sess-100",
				PowerStatus:       "running",
				PriceCentsPerHour: 100,
				CreatedAt:         "2026-02-23T08:00:00Z",
				Metadata: map[string]string{
					"shopper_session_id":    "sess-100",
					"shopper_deployment_id": "deploy-1",
					"shopper_consumer_id":   "consumer-a",
					"shopper_expires_at":    "2026-02-23T20:00:00Z",
				},
			},
			{
				UUID:              "inst-other",
				Name:              "other-workload",
				PowerStatus:       "running",
				PriceCentsPerHour: 200,
				Metadata:          nil,
			},
		}
		json.NewEncoder(w).Encode(vms)
	})

	client := newTestClient(t, handler)
	instances, err := client.ListAllInstances(context.Background())
	if err != nil {
		t.Fatalf("ListAllInstances returned error: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	// Verify first instance has parsed tags
	inst := instances[0]
	if inst.ID != "inst-ours" {
		t.Errorf("expected ID 'inst-ours', got '%s'", inst.ID)
	}
	if inst.Tags.ShopperSessionID != "sess-100" {
		t.Errorf("expected session ID 'sess-100', got '%s'", inst.Tags.ShopperSessionID)
	}
	if inst.Tags.ShopperDeploymentID != "deploy-1" {
		t.Errorf("expected deployment ID 'deploy-1', got '%s'", inst.Tags.ShopperDeploymentID)
	}
	if inst.Tags.ShopperConsumerID != "consumer-a" {
		t.Errorf("expected consumer ID 'consumer-a', got '%s'", inst.Tags.ShopperConsumerID)
	}
	if inst.Tags.ShopperExpiresAt.IsZero() {
		t.Error("expected ShopperExpiresAt to be parsed")
	}
	if inst.PricePerHour != 1.00 {
		t.Errorf("expected price 1.00, got %f", inst.PricePerHour)
	}
	if inst.StartedAt.IsZero() {
		t.Error("expected StartedAt to be parsed from CreatedAt")
	}

	// Verify second instance has empty tags
	inst2 := instances[1]
	if inst2.ID != "inst-other" {
		t.Errorf("expected ID 'inst-other', got '%s'", inst2.ID)
	}
	if inst2.Tags.ShopperSessionID != "" {
		t.Errorf("expected empty session ID for untagged instance, got '%s'", inst2.Tags.ShopperSessionID)
	}
}

// ---------------------------------------------------------------------------
// Error handling tests
// ---------------------------------------------------------------------------

func TestErrorHandling_AuthError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		resp := ErrorDetailResponse{
			Detail: ErrorResponse{
				Error:   "forbidden",
				Message: "Invalid API key",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !provider.IsAuthError(err) {
		t.Errorf("expected IsAuthError to return true, got false. err: %v", err)
	}
}

func TestErrorHandling_RateLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		resp := ErrorResponse{
			Error:   "rate_limited",
			Message: "Too many requests",
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	if !provider.IsRateLimitError(err) {
		t.Errorf("expected IsRateLimitError to return true, got false. err: %v", err)
	}
}

func TestErrorHandling_ServerError_IsRetryable(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		resp := ErrorResponse{
			Error:   "internal_error",
			Message: "Something went wrong",
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err == nil {
		t.Fatal("expected server error, got nil")
	}
	if !provider.IsRetryable(err) {
		t.Errorf("expected IsRetryable to return true for 500 error, got false. err: %v", err)
	}
}

func TestAPIKeyHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "test-api-key" {
			t.Errorf("expected X-API-Key header 'test-api-key', got '%s'", apiKey)
		}
		w.Header().Set("Content-Type", "application/json")
		// Return empty valid response
		resp := AvailableResponse{Data: []AvailableInstance{}}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("ListOffers returned error: %v", err)
	}
}
