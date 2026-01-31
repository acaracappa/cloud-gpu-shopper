package vastai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Name(t *testing.T) {
	c := NewClient("test-key")
	assert.Equal(t, "vastai", c.Name())
}

func TestClient_SupportsFeature(t *testing.T) {
	c := NewClient("test-key")

	tests := []struct {
		feature  provider.ProviderFeature
		expected bool
	}{
		{provider.FeatureInstanceTags, true},
		{provider.FeatureSpotPricing, true},
		{provider.FeatureCustomImages, true},
		{provider.FeatureIdleDetection, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.feature), func(t *testing.T) {
			assert.Equal(t, tt.expected, c.SupportsFeature(tt.feature))
		})
	}
}

func TestClient_ListOffers(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bundles/", r.URL.Path)
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

		resp := BundlesResponse{
			Offers: []Bundle{
				{
					ID:          12345,
					GPUName:     "RTX 4090",
					GPURam:      24576, // 24GB in MB
					NumGPUs:     1,
					DphTotal:    0.45,
					Geolocation: "California, US",
					Reliability: 0.95,
					Rentable:    true,
					Rented:      false,
				},
				{
					ID:          12346,
					GPUName:     "A100",
					GPURam:      81920, // 80GB in MB
					NumGPUs:     1,
					DphTotal:    1.50,
					Geolocation: "Virginia, US",
					Reliability: 0.99,
					Rentable:    true,
					Rented:      false,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	assert.Len(t, offers, 2)

	// Check first offer
	assert.Equal(t, "vastai-12345", offers[0].ID)
	assert.Equal(t, "vastai", offers[0].Provider)
	assert.Equal(t, "RTX 4090", offers[0].GPUType)
	assert.Equal(t, 24, offers[0].VRAM)
	assert.Equal(t, 0.45, offers[0].PricePerHour)
	assert.True(t, offers[0].Available)
}

func TestClient_ListOffers_WithFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that query contains filter params
		q := r.URL.Query().Get("q")
		assert.Contains(t, q, "rentable")

		resp := BundlesResponse{
			Offers: []Bundle{
				{
					ID:          12345,
					GPUName:     "RTX 4090",
					GPURam:      24576,
					NumGPUs:     1,
					DphTotal:    0.45,
					Geolocation: "California, US",
					Reliability: 0.95,
					Rentable:    true,
				},
				{
					ID:          12346,
					GPUName:     "RTX 3080",
					GPURam:      10240, // 10GB - should be filtered out
					NumGPUs:     1,
					DphTotal:    0.25,
					Geolocation: "Texas, US",
					Reliability: 0.90,
					Rentable:    true,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	// Filter for >= 20GB VRAM
	filter := models.OfferFilter{
		MinVRAM: 20,
	}
	offers, err := client.ListOffers(context.Background(), filter)

	require.NoError(t, err)
	assert.Len(t, offers, 1)
	assert.Equal(t, "RTX 4090", offers[0].GPUType)
}

func TestClient_ListAllInstances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/instances/", r.URL.Path)

		resp := InstancesResponse{
			Instances: []Instance{
				{
					ID:           123,
					Label:        "shopper-abc123",
					ActualStatus: "running",
					DphTotal:     0.50,
					StartDate:    1706500000,
				},
				{
					ID:           124,
					Label:        "other-instance", // Not ours
					ActualStatus: "running",
					DphTotal:     0.30,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	// Should only return our instances (with shopper- prefix)
	assert.Len(t, instances, 1)
	assert.Equal(t, "123", instances[0].ID)
	assert.Equal(t, "abc123", instances[0].Tags.ShopperSessionID)
}

func TestClient_GetInstanceStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	_, err := client.GetInstanceStatus(context.Background(), "99999")

	require.Error(t, err)
	assert.ErrorIs(t, err, provider.ErrInstanceNotFound)
}

func TestClient_HandleError_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsRateLimitError(err))
}

// TestClient_AttachSSHKey verifies the SSH key attachment API call.
// LEARNING: Vast.ai requires a separate API call to register SSH keys after instance creation.
// The ssh_key parameter in the create request doesn't reliably register the key.
func TestClient_AttachSSHKey(t *testing.T) {
	var capturedInstanceID string
	var capturedSSHKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the endpoint format: POST /instances/{id}/ssh/
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/instances/")
		assert.True(t, strings.HasSuffix(r.URL.Path, "/ssh/"))

		// Extract instance ID from path
		parts := strings.Split(r.URL.Path, "/")
		for i, p := range parts {
			if p == "instances" && i+1 < len(parts) {
				capturedInstanceID = parts[i+1]
				break
			}
		}

		// Parse request body
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req map[string]string
		err = json.Unmarshal(body, &req)
		require.NoError(t, err)
		capturedSSHKey = req["ssh_key"]

		// Return success
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	err := client.AttachSSHKey(context.Background(), "12345", "ssh-rsa AAAAB3NzaC1yc2E... test@host")

	require.NoError(t, err)
	assert.Equal(t, "12345", capturedInstanceID)
	assert.Equal(t, "ssh-rsa AAAAB3NzaC1yc2E... test@host", capturedSSHKey)
}

// TestClient_CreateInstance_CallsAttachSSHKey verifies that CreateInstance
// calls AttachSSHKey after the instance is created.
// LEARNING: SSH key attachment is a two-step process:
// 1. Create the instance (ssh_key param in request is unreliable)
// 2. Call AttachSSHKey endpoint to properly register the key
// SSH key propagation requires ~15 seconds after attachment.
func TestClient_CreateInstance_CallsAttachSSHKey(t *testing.T) {
	var createCalled, attachCalled bool
	var attachInstanceID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/asks/") {
			// CreateInstance call
			createCalled = true
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(CreateInstanceResponse{
				Success:     true,
				NewContract: 67890,
			})
			return
		}

		if r.Method == "POST" && strings.Contains(r.URL.Path, "/ssh/") {
			// AttachSSHKey call
			attachCalled = true
			parts := strings.Split(r.URL.Path, "/")
			for i, p := range parts {
				if p == "instances" && i+1 < len(parts) {
					attachInstanceID = parts[i+1]
					break
				}
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}

		t.Fatalf("Unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	req := provider.CreateInstanceRequest{
		OfferID:      "12345",
		SessionID:    "sess-001",
		SSHPublicKey: "ssh-rsa AAAAB3NzaC1yc2E... test@host",
	}

	info, err := client.CreateInstance(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, createCalled, "CreateInstance should call the create endpoint")
	assert.True(t, attachCalled, "CreateInstance should call AttachSSHKey")
	assert.Equal(t, "67890", attachInstanceID, "AttachSSHKey should use the new instance ID")
	assert.Equal(t, "67890", info.ProviderInstanceID)
}

// TestClient_CreateInstance_AttachSSHKeyFailureNonFatal verifies that
// AttachSSHKey failures don't cause CreateInstance to fail.
// The instance is already created, so we log the error but return success.
// SSH verification will fail later if the key wasn't attached.
func TestClient_CreateInstance_AttachSSHKeyFailureNonFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/asks/") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(CreateInstanceResponse{
				Success:     true,
				NewContract: 67890,
			})
			return
		}

		if r.Method == "POST" && strings.Contains(r.URL.Path, "/ssh/") {
			// AttachSSHKey fails
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("SSH key attachment failed"))
			return
		}

		t.Fatalf("Unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	req := provider.CreateInstanceRequest{
		OfferID:      "12345",
		SessionID:    "sess-001",
		SSHPublicKey: "ssh-rsa AAAAB3NzaC1yc2E... test@host",
	}

	// CreateInstance should still succeed even if AttachSSHKey fails
	info, err := client.CreateInstance(context.Background(), req)

	require.NoError(t, err, "CreateInstance should succeed even if AttachSSHKey fails")
	assert.Equal(t, "67890", info.ProviderInstanceID)
}

func TestBundle_ToGPUOffer(t *testing.T) {
	bundle := Bundle{
		ID:          12345,
		GPUName:     "GeForce RTX 4090",
		GPURam:      24576,
		NumGPUs:     2,
		DphTotal:    0.90,
		Geolocation: "California, US",
		Reliability: 0.95,
		Rentable:    true,
		Rented:      false,
	}

	offer := bundle.ToGPUOffer()

	assert.Equal(t, "vastai-12345", offer.ID)
	assert.Equal(t, "vastai", offer.Provider)
	assert.Equal(t, "12345", offer.ProviderID)
	assert.Equal(t, "RTX 4090", offer.GPUType) // Normalized
	assert.Equal(t, 2, offer.GPUCount)
	assert.Equal(t, 24, offer.VRAM) // Converted from MB to GB
	assert.Equal(t, 0.90, offer.PricePerHour)
	assert.True(t, offer.Available)
}

func TestNormalizeGPUName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"RTX 4090", "RTX 4090"},
		{"GeForce RTX 4090", "RTX 4090"},
		{"NVIDIA A100", "A100"},
		{"Tesla V100", "V100"},
		{"RTX 5090", "RTX 5090"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGPUName(tt.input))
		})
	}
}

func TestBuildVLLMArgs(t *testing.T) {
	tests := []struct {
		name     string
		config   *provider.WorkloadConfig
		contains []string
		empty    bool
	}{
		{
			name:   "nil config",
			config: nil,
			empty:  true,
		},
		{
			name: "empty model ID",
			config: &provider.WorkloadConfig{
				Type:    provider.WorkloadTypeVLLM,
				ModelID: "",
			},
			empty: true,
		},
		{
			name: "basic config",
			config: &provider.WorkloadConfig{
				Type:    provider.WorkloadTypeVLLM,
				ModelID: "TinyLlama/TinyLlama-1.1B-Chat-v1.0",
			},
			contains: []string{
				"--model TinyLlama/TinyLlama-1.1B-Chat-v1.0",
				"--host 0.0.0.0",
				"--port 8000",
				"--gpu-memory-utilization 0.90",
			},
		},
		{
			name: "with quantization",
			config: &provider.WorkloadConfig{
				Type:         provider.WorkloadTypeVLLM,
				ModelID:      "TheBloke/Llama-2-7B-AWQ",
				Quantization: "awq",
			},
			contains: []string{
				"--quantization awq",
			},
		},
		{
			name: "with tensor parallelism",
			config: &provider.WorkloadConfig{
				Type:           provider.WorkloadTypeVLLM,
				ModelID:        "meta-llama/Llama-2-70b-hf",
				TensorParallel: 4,
			},
			contains: []string{
				"--tensor-parallel-size 4",
			},
		},
		{
			name: "with max model length",
			config: &provider.WorkloadConfig{
				Type:        provider.WorkloadTypeVLLM,
				ModelID:     "TinyLlama/TinyLlama-1.1B-Chat-v1.0",
				MaxModelLen: 4096,
			},
			contains: []string{
				"--max-model-len 4096",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildVLLMArgs(tt.config)
			if tt.empty {
				assert.Empty(t, result)
			} else {
				for _, expected := range tt.contains {
					assert.Contains(t, result, expected)
				}
			}
		})
	}
}

func TestBuildTGIArgs(t *testing.T) {
	tests := []struct {
		name     string
		config   *provider.WorkloadConfig
		contains []string
		empty    bool
	}{
		{
			name:   "nil config",
			config: nil,
			empty:  true,
		},
		{
			name: "basic config",
			config: &provider.WorkloadConfig{
				Type:    provider.WorkloadTypeTGI,
				ModelID: "TinyLlama/TinyLlama-1.1B-Chat-v1.0",
			},
			contains: []string{
				"--model-id TinyLlama/TinyLlama-1.1B-Chat-v1.0",
				"--hostname 0.0.0.0",
				"--port 80",
			},
		},
		{
			name: "with quantization",
			config: &provider.WorkloadConfig{
				Type:         provider.WorkloadTypeTGI,
				ModelID:      "TheBloke/Llama-2-7B-GPTQ",
				Quantization: "gptq",
			},
			contains: []string{
				"--quantize gptq",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildTGIArgs(tt.config)
			if tt.empty {
				assert.Empty(t, result)
			} else {
				for _, expected := range tt.contains {
					assert.Contains(t, result, expected)
				}
			}
		})
	}
}

func TestGetImageForWorkload(t *testing.T) {
	assert.Equal(t, ImageVLLM, GetImageForWorkload(provider.WorkloadTypeVLLM))
	assert.Equal(t, ImageTGI, GetImageForWorkload(provider.WorkloadTypeTGI))
	assert.Equal(t, ImageSSHBase, GetImageForWorkload(provider.WorkloadTypeCustom))
	assert.Equal(t, ImageSSHBase, GetImageForWorkload("unknown"))
}

func TestGetPortForWorkload(t *testing.T) {
	assert.Equal(t, DefaultVLLMPort, GetPortForWorkload(provider.WorkloadTypeVLLM))
	assert.Equal(t, DefaultTGIPort, GetPortForWorkload(provider.WorkloadTypeTGI))
	assert.Equal(t, 0, GetPortForWorkload(provider.WorkloadTypeCustom))
}

func TestFormatPortsString(t *testing.T) {
	assert.Equal(t, "", FormatPortsString(nil))
	assert.Equal(t, "", FormatPortsString([]int{}))
	assert.Equal(t, "8000/http", FormatPortsString([]int{8000}))
	assert.Equal(t, "8000/http,8080/http", FormatPortsString([]int{8000, 8080}))
}
