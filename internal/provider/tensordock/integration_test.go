// Package tensordock integration tests verify that the TensorDock provider
// correctly integrates with the rest of the cloud-gpu-shopper system.
//
// These tests verify:
// - Provider interface compliance
// - Session lifecycle integration
// - Provisioner service integration
// - Metrics and logging integration
// - Error propagation through the system
package tensordock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// =============================================================================
// Provider Interface Compliance Tests
// =============================================================================

// TestProviderInterfaceCompliance verifies that Client implements provider.Provider
func TestProviderInterfaceCompliance(t *testing.T) {
	// Compile-time check that Client implements Provider interface
	var _ provider.Provider = (*Client)(nil)

	client := NewClient("test-key", "test-token")

	// Verify all interface methods exist and are callable
	t.Run("Name", func(t *testing.T) {
		name := client.Name()
		assert.Equal(t, "tensordock", name)
		assert.NotEmpty(t, name)
	})

	t.Run("SupportsFeature", func(t *testing.T) {
		// TensorDock should support custom images
		assert.True(t, client.SupportsFeature(provider.FeatureCustomImages))
		// TensorDock doesn't support these features
		assert.False(t, client.SupportsFeature(provider.FeatureIdleDetection))
		assert.False(t, client.SupportsFeature(provider.FeatureInstanceTags))
		assert.False(t, client.SupportsFeature(provider.FeatureSpotPricing))
	})
}

// TestProviderInterfaceMethodSignatures verifies method signatures match interface
func TestProviderInterfaceMethodSignatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"locations": []interface{}{}}})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	ctx := context.Background()

	// Test that each method accepts correct parameter types
	t.Run("ListOffers accepts OfferFilter", func(t *testing.T) {
		filter := models.OfferFilter{
			MinVRAM:     24,
			MaxPrice:    1.0,
			GPUType:     "RTX 4090",
			MinGPUCount: 1,
			Provider:    "tensordock",
		}
		_, _ = client.ListOffers(ctx, filter)
		// Method exists and accepts the correct type
	})

	t.Run("CreateInstance accepts CreateInstanceRequest", func(t *testing.T) {
		req := provider.CreateInstanceRequest{
			OfferID:      "tensordock-loc-123-gpu",
			SessionID:    "session-123",
			SSHPublicKey: TestSSHKey,
			DockerImage:  "nvidia/cuda:12.0-base",
			EnvVars:      map[string]string{"KEY": "value"},
			OnStartCmd:   "echo hello",
			Tags: models.InstanceTags{
				ShopperSessionID:    "session-123",
				ShopperDeploymentID: "deploy-123",
				ShopperExpiresAt:    time.Now().Add(12 * time.Hour),
				ShopperConsumerID:   "consumer-123",
			},
			LaunchMode:   provider.LaunchModeSSH,
			Entrypoint:   []string{"/bin/bash"},
			ExposedPorts: []int{8000, 8080},
			WorkloadConfig: &provider.WorkloadConfig{
				Type:    provider.WorkloadTypeVLLM,
				ModelID: "meta-llama/Llama-2-7b",
			},
		}
		// Will fail due to mock, but validates type compatibility
		_, _ = client.CreateInstance(ctx, req)
	})

	t.Run("GetInstanceStatus accepts string instanceID", func(t *testing.T) {
		_, _ = client.GetInstanceStatus(ctx, "instance-123")
	})

	t.Run("DestroyInstance accepts string instanceID", func(t *testing.T) {
		_ = client.DestroyInstance(ctx, "instance-123")
	})

	t.Run("ListAllInstances requires no parameters", func(t *testing.T) {
		_, _ = client.ListAllInstances(ctx)
	})
}

// =============================================================================
// Session Lifecycle Integration Tests
// =============================================================================

// TestSessionLifecycleFlow tests the complete session lifecycle through the provider
func TestSessionLifecycleFlow(t *testing.T) {
	// Track which API calls are made in order
	var callSequence []string
	var mu sync.Mutex

	instanceID := "inst-" + time.Now().Format("20060102150405")

	// Use proper UUID format for location ID (must be 36 chars for offer ID parsing)
	locationUUID := "12345678-1234-1234-1234-123456789012"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callSequence = append(callSequence, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch {
		case r.URL.Path == "/locations":
			json.NewEncoder(w).Encode(LocationsResponse{
				Data: LocationsData{
					Locations: []Location{{
						ID:   locationUUID,
						City: "Test City",
						GPUs: []LocationGPU{{
							V0Name:      "rtx4090",
							DisplayName: "RTX 4090 24GB",
							MaxCount:    4,
							PricePerHr:  0.50,
						}},
					}},
				},
			})

		case r.Method == "POST" && r.URL.Path == "/instances":
			json.NewEncoder(w).Encode(CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					Type:   "virtualmachine",
					ID:     instanceID,
					Name:   "shopper-session123",
					Status: "creating",
				},
			})

		case r.Method == "GET" && strings.Contains(r.URL.Path, "/instances/"):
			json.NewEncoder(w).Encode(InstanceResponse{
				Type:      "virtualmachine",
				ID:        instanceID,
				Name:      "shopper-session123",
				Status:    "running",
				IPAddress: "10.0.0.1",
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 20456},
				},
			})

		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/instances/"):
			w.WriteHeader(http.StatusOK)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0), // Disable rate limiting for tests
	)
	ctx := context.Background()

	// Step 1: List offers (inventory phase)
	t.Run("1_ListOffers", func(t *testing.T) {
		offers, err := client.ListOffers(ctx, models.OfferFilter{})
		require.NoError(t, err)
		require.Len(t, offers, 1)
		assert.Equal(t, "tensordock", offers[0].Provider)
		assert.Equal(t, "tensordock-12345678-1234-1234-1234-123456789012-rtx4090", offers[0].ID)
	})

	// Step 2: Create instance (provisioning phase)
	t.Run("2_CreateInstance", func(t *testing.T) {
		instance, err := client.CreateInstance(ctx, provider.CreateInstanceRequest{
			OfferID:      "tensordock-12345678-1234-1234-1234-123456789012-rtx4090",
			SessionID:    "session123",
			SSHPublicKey: TestSSHKey,
			Tags: models.InstanceTags{
				ShopperSessionID: "session123",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, instanceID, instance.ProviderInstanceID)
		assert.Equal(t, "user", instance.SSHUser)
	})

	// Step 3: Poll for status (SSH verification phase)
	t.Run("3_GetInstanceStatus", func(t *testing.T) {
		status, err := client.GetInstanceStatus(ctx, instanceID)
		require.NoError(t, err)
		assert.True(t, status.Running)
		assert.Equal(t, "10.0.0.1", status.SSHHost)
		assert.Equal(t, 20456, status.SSHPort) // Dynamic port assignment
		assert.Equal(t, "user", status.SSHUser)
	})

	// Step 4: Destroy instance (cleanup phase)
	t.Run("4_DestroyInstance", func(t *testing.T) {
		err := client.DestroyInstance(ctx, instanceID)
		require.NoError(t, err)
	})

	// Verify call sequence
	t.Run("VerifyCallSequence", func(t *testing.T) {
		mu.Lock()
		defer mu.Unlock()
		// Should have: GET /locations, POST /instances, GET /instances/{id}, DELETE /instances/{id}
		assert.GreaterOrEqual(t, len(callSequence), 4)
	})
}

// TestSessionTagsIntegration tests that instance tags are properly set and parsed
func TestSessionTagsIntegration(t *testing.T) {
	var capturedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			json.NewDecoder(r.Body).Decode(&capturedRequest)
			json.NewEncoder(w).Encode(CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					ID:     "inst-123",
					Name:   capturedRequest.Data.Attributes.Name,
					Status: "running",
				},
			})
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	tags := models.InstanceTags{
		ShopperSessionID:    "session-abc123",
		ShopperDeploymentID: "deploy-xyz",
		ShopperExpiresAt:    time.Now().Add(12 * time.Hour),
		ShopperConsumerID:   "consumer-456",
	}

	// Use proper UUID format for location ID (36 chars)
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "tensordock-12345678-1234-1234-1234-123456789012-rtx4090",
		SessionID:    "session-abc123",
		SSHPublicKey: TestSSHKey,
		Tags:         tags,
	})
	require.NoError(t, err)

	// Verify the name uses the label format
	expectedLabel := tags.ToLabel()
	assert.Equal(t, expectedLabel, capturedRequest.Data.Attributes.Name)
	assert.Equal(t, "shopper-session-abc123", capturedRequest.Data.Attributes.Name)

	// Verify ParseLabel can recover the session ID
	parsedSessionID, ok := models.ParseLabel(capturedRequest.Data.Attributes.Name)
	assert.True(t, ok)
	assert.Equal(t, "session-abc123", parsedSessionID)
}

// TestListAllInstancesFiltering tests that only our instances are returned
func TestListAllInstancesFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(InstancesResponse{
			Data: InstancesData{
				Instances: []Instance{
					{ID: "inst-1", Name: "shopper-session1", Status: "running"},
					{ID: "inst-2", Name: "other-vm", Status: "running"}, // Not ours
					{ID: "inst-3", Name: "shopper-session2", Status: "running"},
					{ID: "inst-4", Name: "my-test-vm", Status: "running"}, // Not ours
					{ID: "inst-5", Name: "shopper-", Status: "running"},   // Edge case - empty session ID
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	instances, err := client.ListAllInstances(context.Background())
	require.NoError(t, err)

	// Should only return instances with shopper- prefix and valid session ID
	assert.Len(t, instances, 2)

	sessionIDs := make(map[string]bool)
	for _, inst := range instances {
		sessionIDs[inst.Tags.ShopperSessionID] = true
	}
	assert.True(t, sessionIDs["session1"])
	assert.True(t, sessionIDs["session2"])
}

// =============================================================================
// Provisioner Service Integration Tests
// =============================================================================

// TestOfferIDParsing tests the offer ID format used by the provisioner
func TestOfferIDParsing(t *testing.T) {
	testCases := []struct {
		name        string
		offerID     string
		wantLocID   string
		wantGPUName string
		wantErr     bool
	}{
		{
			name:        "valid offer ID",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb",
			wantLocID:   "1a779525-4c04-4f2c-aa45-58b47d54bb38",
			wantGPUName: "geforcertx3090-pcie-24gb",
			wantErr:     false,
		},
		{
			name:        "valid offer ID with simple GPU name",
			offerID:     "tensordock-abcd1234-5678-90ab-cdef-1234567890ab-rtx4090",
			wantLocID:   "abcd1234-5678-90ab-cdef-1234567890ab",
			wantGPUName: "rtx4090",
			wantErr:     false,
		},
		{
			name:    "missing prefix",
			offerID: "1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090",
			wantErr: true,
		},
		{
			name:    "wrong prefix",
			offerID: "vastai-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090",
			wantErr: true,
		},
		{
			name:    "too short",
			offerID: "tensordock-short",
			wantErr: true,
		},
		{
			name:    "empty",
			offerID: "",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			locID, gpuName, err := parseOfferID(tc.offerID)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantLocID, locID)
				assert.Equal(t, tc.wantGPUName, gpuName)
			}
		})
	}
}

// TestOfferFilterIntegration tests offer filtering as used by the provisioner
func TestOfferFilterIntegration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:   "loc-1",
						City: "Los Angeles",
						Tier: 3,
						GPUs: []LocationGPU{
							{V0Name: "rtx4090", DisplayName: "RTX 4090 24GB", MaxCount: 4, PricePerHr: 0.50},
							{V0Name: "rtx3090", DisplayName: "RTX 3090 24GB", MaxCount: 2, PricePerHr: 0.35},
						},
					},
					{
						ID:   "loc-2",
						City: "New York",
						Tier: 2,
						GPUs: []LocationGPU{
							{V0Name: "a100-80gb", DisplayName: "A100 80GB", MaxCount: 8, PricePerHr: 2.00},
							{V0Name: "v100", DisplayName: "V100 16GB", MaxCount: 4, PricePerHr: 0.80},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))
	ctx := context.Background()

	t.Run("filter by VRAM", func(t *testing.T) {
		offers, err := client.ListOffers(ctx, models.OfferFilter{MinVRAM: 24})
		require.NoError(t, err)
		for _, offer := range offers {
			assert.GreaterOrEqual(t, offer.VRAM, 24, "Offer %s has less than 24GB VRAM", offer.ID)
		}
	})

	t.Run("filter by max price", func(t *testing.T) {
		offers, err := client.ListOffers(ctx, models.OfferFilter{MaxPrice: 1.0})
		require.NoError(t, err)
		for _, offer := range offers {
			assert.LessOrEqual(t, offer.PricePerHour, 1.0, "Offer %s exceeds max price", offer.ID)
		}
	})

	t.Run("filter by GPU count", func(t *testing.T) {
		offers, err := client.ListOffers(ctx, models.OfferFilter{MinGPUCount: 4})
		require.NoError(t, err)
		for _, offer := range offers {
			assert.GreaterOrEqual(t, offer.GPUCount, 4, "Offer %s has fewer than 4 GPUs", offer.ID)
		}
	})
}

// =============================================================================
// Error Propagation Tests
// =============================================================================

// TestErrorPropagation verifies errors are correctly typed for system handling
func TestErrorPropagation(t *testing.T) {
	testCases := []struct {
		name         string
		statusCode   int
		responseBody string
		checkError   func(t *testing.T, err error)
	}{
		{
			name:       "rate limit error",
			statusCode: http.StatusTooManyRequests,
			checkError: func(t *testing.T, err error) {
				assert.True(t, provider.IsRateLimitError(err))
				assert.True(t, provider.IsRetryable(err))
			},
		},
		{
			name:       "auth error - unauthorized",
			statusCode: http.StatusUnauthorized,
			checkError: func(t *testing.T, err error) {
				assert.True(t, provider.IsAuthError(err))
				assert.False(t, provider.IsRetryable(err))
			},
		},
		{
			name:       "auth error - forbidden",
			statusCode: http.StatusForbidden,
			checkError: func(t *testing.T, err error) {
				assert.True(t, provider.IsAuthError(err))
				assert.False(t, provider.IsRetryable(err))
			},
		},
		{
			name:       "not found error",
			statusCode: http.StatusNotFound,
			checkError: func(t *testing.T, err error) {
				assert.True(t, errors.Is(err, provider.ErrInstanceNotFound))
				assert.True(t, provider.IsNotFoundError(err))
			},
		},
		{
			name:       "server error - retryable",
			statusCode: http.StatusInternalServerError,
			checkError: func(t *testing.T, err error) {
				assert.True(t, provider.IsRetryable(err))
			},
		},
		{
			name:       "bad gateway - retryable",
			statusCode: http.StatusBadGateway,
			checkError: func(t *testing.T, err error) {
				assert.True(t, provider.IsRetryable(err))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				if tc.responseBody != "" {
					w.Write([]byte(tc.responseBody))
				}
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))
			_, err := client.GetInstanceStatus(context.Background(), "inst-123")
			require.Error(t, err)

			tc.checkError(t, err)
		})
	}
}

// TestStaleInventoryErrorHandling tests the stale inventory error detection
func TestStaleInventoryErrorHandling(t *testing.T) {
	testCases := []struct {
		name     string
		response string
		isStale  bool
	}{
		{
			name:     "no available nodes",
			response: `{"status": 400, "error": "No available nodes found for the requested configuration"}`,
			isStale:  true,
		},
		{
			name:     "insufficient capacity",
			response: `{"status": 400, "error": "Insufficient capacity in this region"}`,
			isStale:  true,
		},
		{
			name:     "out of stock",
			response: `{"status": 400, "error": "This GPU model is out of stock"}`,
			isStale:  true,
		},
		{
			name:     "resource unavailable",
			response: `{"status": 400, "error": "Resource unavailable at this time"}`,
			isStale:  true,
		},
		{
			name:     "validation error - not stale",
			response: `{"status": 400, "error": "Invalid SSH key format"}`,
			isStale:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK) // TensorDock returns 200 with error in body
				w.Write([]byte(tc.response))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))
			_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
				OfferID:      "tensordock-loc-123-abcdefgh-1234-5678-90ab-cdef12345678-rtx4090",
				SessionID:    "session-123",
				SSHPublicKey: TestSSHKey,
				Tags:         models.InstanceTags{ShopperSessionID: "session-123"},
			})

			if tc.isStale {
				require.Error(t, err)
				assert.True(t, provider.IsStaleInventoryError(err) || provider.ShouldRetryWithDifferentOffer(err),
					"Expected stale inventory error for: %s", tc.name)
			}
		})
	}
}

// TestProviderErrorContext verifies error messages include useful context
func TestProviderErrorContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid configuration"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))
	_, err := client.GetInstanceStatus(context.Background(), "inst-123")

	require.Error(t, err)

	// Error should contain provider name and operation
	errMsg := err.Error()
	assert.Contains(t, errMsg, "tensordock", "Error should mention provider name")
	assert.Contains(t, errMsg, "GetInstanceStatus", "Error should mention operation name")

	// Verify error is a ProviderError
	var provErr *provider.ProviderError
	require.True(t, errors.As(err, &provErr))
	assert.Equal(t, "tensordock", provErr.Provider)
	assert.Equal(t, "GetInstanceStatus", provErr.Operation)
	assert.Equal(t, http.StatusBadRequest, provErr.StatusCode)
}

// =============================================================================
// Concurrent Operation Tests
// =============================================================================

// TestConcurrentAPIRequests tests rate limiting under concurrent load
func TestConcurrentAPIRequests(t *testing.T) {
	var requestCount int64
	var requestTimes []time.Time
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		atomic.AddInt64(&requestCount, 1)
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()

		json.NewEncoder(w).Encode(InstanceResponse{
			ID:        "inst-123",
			Status:    "running",
			IPAddress: "10.0.0.1",
		})
	}))
	defer server.Close()

	// Use short rate limit for testing
	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(100*time.Millisecond),
	)

	// Launch concurrent requests
	const numRequests = 5
	var wg sync.WaitGroup
	wg.Add(numRequests)

	start := time.Now()
	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			_, _ = client.GetInstanceStatus(context.Background(), "inst-123")
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// With 100ms rate limit and 5 requests, should take at least 400ms
	assert.Equal(t, int64(numRequests), atomic.LoadInt64(&requestCount))
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(400),
		"Rate limiting should space out requests")
}

// TestContextCancellation tests that operations respect context cancellation
func TestContextCancellation(t *testing.T) {
	requestReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestReceived)
		// Delay response to allow cancellation
		time.Sleep(5 * time.Second)
		json.NewEncoder(w).Encode(InstanceResponse{ID: "inst-123"})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.GetInstanceStatus(ctx, "inst-123")

	// Should get a context deadline exceeded error
	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "deadline exceeded") ||
		strings.Contains(err.Error(), "context deadline"),
		"Expected context deadline error, got: %v", err)
}

// =============================================================================
// SSH Key Cloud-Init Integration Tests
// =============================================================================

// TestSSHKeyCloudInitConfiguration tests the cloud-init SSH key installation
func TestSSHKeyCloudInitConfiguration(t *testing.T) {
	var capturedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			json.NewDecoder(r.Body).Decode(&capturedRequest)
			json.NewEncoder(w).Encode(CreateInstanceResponse{
				Data: CreateInstanceResponseData{ID: "inst-123", Status: "running"},
			})
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "tensordock-loc-123-abcdefgh-1234-5678-90ab-cdef12345678-rtx4090",
		SessionID:    "session-123",
		SSHPublicKey: TestSSHKey,
		Tags:         models.InstanceTags{ShopperSessionID: "session-123"},
	})
	require.NoError(t, err)

	// Verify cloud-init is configured with runcmd for SSH key installation
	require.NotNil(t, capturedRequest.Data.Attributes.CloudInit, "CloudInit should be set")
	require.NotEmpty(t, capturedRequest.Data.Attributes.CloudInit.RunCmd, "CloudInit RunCmd should not be empty")

	// Verify SSH key is in runcmd (via echo command)
	foundSSHCmd := false
	for _, cmd := range capturedRequest.Data.Attributes.CloudInit.RunCmd {
		if strings.Contains(cmd, "authorized_keys") && strings.Contains(cmd, "echo") {
			foundSSHCmd = true
			break
		}
	}
	assert.True(t, foundSSHCmd, "Should have runcmd entry for SSH key installation")

	// Verify the ssh_key field is also set (required by TensorDock even though it doesn't work)
	assert.Equal(t, TestSSHKey, capturedRequest.Data.Attributes.SSHKey)
}

// =============================================================================
// Port Forwarding Integration Tests
// =============================================================================

// TestDynamicPortForwarding tests handling of TensorDock's dynamic port assignment
func TestDynamicPortForwarding(t *testing.T) {
	// TensorDock assigns random external ports
	externalPort := 20456

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/instances/") {
			json.NewEncoder(w).Encode(InstanceResponse{
				ID:        "inst-123",
				Status:    "running",
				IPAddress: "10.0.0.1",
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: externalPort},
				},
			})
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	status, err := client.GetInstanceStatus(context.Background(), "inst-123")
	require.NoError(t, err)

	// Should return the dynamically assigned port, not 22
	assert.Equal(t, externalPort, status.SSHPort)
	assert.NotEqual(t, 22, status.SSHPort)
}

// TestMultiplePortForwards tests handling of multiple port forwards
func TestMultiplePortForwards(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(InstanceResponse{
			ID:        "inst-123",
			Status:    "running",
			IPAddress: "10.0.0.1",
			PortForwards: []PortForward{
				{Protocol: "tcp", InternalPort: 8000, ExternalPort: 30000}, // API port
				{Protocol: "tcp", InternalPort: 22, ExternalPort: 30022},   // SSH port
				{Protocol: "tcp", InternalPort: 8080, ExternalPort: 30080}, // Additional port
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	status, err := client.GetInstanceStatus(context.Background(), "inst-123")
	require.NoError(t, err)

	// Should find SSH port specifically
	assert.Equal(t, 30022, status.SSHPort)
}

// TestNoSSHPortForward tests handling when SSH port is not in the forwards
func TestNoSSHPortForward(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(InstanceResponse{
			ID:        "inst-123",
			Status:    "running",
			IPAddress: "10.0.0.1",
			PortForwards: []PortForward{
				{Protocol: "tcp", InternalPort: 8000, ExternalPort: 30000}, // No SSH
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	status, err := client.GetInstanceStatus(context.Background(), "inst-123")
	require.NoError(t, err)

	// Should default to port 22 when not found
	assert.Equal(t, 22, status.SSHPort)
}

// =============================================================================
// Availability Confidence Tests
// =============================================================================

// TestAvailabilityConfidence tests that offers have correct confidence level
func TestAvailabilityConfidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{
				Locations: []Location{{
					ID:   "loc-123",
					City: "Test",
					GPUs: []LocationGPU{
						{V0Name: "rtx4090", DisplayName: "RTX 4090 24GB", PricePerHr: 0.50},
					},
				}},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	require.NoError(t, err)
	require.Len(t, offers, 1)

	// TensorDock has 30% confidence due to stale inventory issues
	assert.Equal(t, TensorDockAvailabilityConfidence, offers[0].AvailabilityConfidence)
	assert.Equal(t, 0.3, offers[0].AvailabilityConfidence)
}

// =============================================================================
// API Response Format Handling Tests
// =============================================================================

// TestListInstancesArrayFormat tests handling of array format response
func TestListInstancesArrayFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TensorDock sometimes returns data as direct array
		response := `{"data": [
			{"id": "inst-1", "name": "shopper-session1", "status": "running"},
			{"id": "inst-2", "name": "shopper-session2", "status": "stopped"}
		]}`
		w.Write([]byte(response))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	instances, err := client.ListAllInstances(context.Background())
	require.NoError(t, err)
	assert.Len(t, instances, 2)
}

// TestListInstancesNestedFormat tests handling of nested format response
func TestListInstancesNestedFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TensorDock sometimes returns data in nested format
		json.NewEncoder(w).Encode(InstancesResponse{
			Data: InstancesData{
				Instances: []Instance{
					{ID: "inst-1", Name: "shopper-session1", Status: "running"},
					{ID: "inst-2", Name: "shopper-session2", Status: "stopped"},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	instances, err := client.ListAllInstances(context.Background())
	require.NoError(t, err)
	assert.Len(t, instances, 2)
}

// TestListInstancesEmptyArray tests handling of empty array response
func TestListInstancesEmptyArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	instances, err := client.ListAllInstances(context.Background())
	require.NoError(t, err)
	assert.Len(t, instances, 0)
}

// =============================================================================
// Integration with Provider Registry Tests
// =============================================================================

// TestProviderRegistryIntegration tests that client works with provider registry
func TestProviderRegistryIntegration(t *testing.T) {
	client := NewClient("test-key", "test-token")

	// Simulate what SimpleProviderRegistry does
	providers := make(map[string]provider.Provider)
	providers[client.Name()] = client

	// Verify retrieval by name
	retrieved, ok := providers["tensordock"]
	require.True(t, ok)
	assert.Equal(t, "tensordock", retrieved.Name())
}

// =============================================================================
// Debug Mode Tests
// =============================================================================

// TestDebugModeEnabled tests that debug mode produces debug output
func TestDebugModeEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{Locations: []Location{}},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0),
		WithDebug(true),
	)

	// This should not panic and should work normally
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	require.NoError(t, err)
}

// =============================================================================
// Custom Configuration Tests
// =============================================================================

// TestClientOptions tests all client configuration options
func TestClientOptions(t *testing.T) {
	customClient := &http.Client{Timeout: 60 * time.Second}

	client := NewClient("test-key", "test-token",
		WithBaseURL("https://custom.url"),
		WithHTTPClient(customClient),
		WithMinInterval(500*time.Millisecond),
		WithDefaultImage("debian12"),
		WithDebug(true),
	)

	assert.Equal(t, "https://custom.url", client.baseURL)
	assert.Equal(t, customClient, client.httpClient)
	assert.Equal(t, rate.Every(500*time.Millisecond), client.limiter.Limit())
	assert.Equal(t, "debian12", client.defaultImage)
	assert.True(t, client.debugEnabled)
}

// =============================================================================
// GPU Offer Conversion Tests
// =============================================================================

// TestGPUOfferConversionIntegrity tests that all offer fields are properly set
func TestGPUOfferConversionIntegrity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{
				Locations: []Location{{
					ID:            "loc-uuid-1234",
					City:          "San Francisco",
					StateProvince: "California",
					Country:       "United States",
					Tier:          3,
					GPUs: []LocationGPU{{
						V0Name:      "rtx4090-pcie",
						DisplayName: "NVIDIA GeForce RTX 4090 PCIe 24GB",
						MaxCount:    8,
						PricePerHr:  0.55,
						Resources: GPUResources{
							MaxVCPUs:     64,
							MaxRAMGb:     512,
							MaxStorageGb: 50000,
						},
					}},
				}},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	require.NoError(t, err)
	require.Len(t, offers, 1)

	offer := offers[0]

	// Verify all fields are properly set
	assert.Equal(t, "tensordock-loc-uuid-1234-rtx4090-pcie", offer.ID)
	assert.Equal(t, "tensordock", offer.Provider)
	assert.Equal(t, "loc-uuid-1234", offer.ProviderID)
	assert.Equal(t, "RTX 4090", offer.GPUType)
	assert.Equal(t, 8, offer.GPUCount)
	assert.Equal(t, 24, offer.VRAM)
	assert.Equal(t, 0.55, offer.PricePerHour)
	assert.Contains(t, offer.Location, "San Francisco")
	assert.Contains(t, offer.Location, "California")
	assert.Contains(t, offer.Location, "United States")
	assert.Equal(t, 1.0, offer.Reliability) // Tier 3/3 = 1.0
	assert.True(t, offer.Available)
	assert.Equal(t, TensorDockAvailabilityConfidence, offer.AvailabilityConfidence)
	assert.False(t, offer.FetchedAt.IsZero())
}

// =============================================================================
// Idempotency Tests
// =============================================================================

// TestDestroyIdempotency tests that destroy is idempotent
func TestDestroyIdempotency(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Return 404 (already deleted) on all calls
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))

	// First destroy - should succeed (instance not found = already deleted)
	err := client.DestroyInstance(context.Background(), "inst-123")
	assert.NoError(t, err)

	// Second destroy - should also succeed
	err = client.DestroyInstance(context.Background(), "inst-123")
	assert.NoError(t, err)

	assert.Equal(t, 2, callCount)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

// BenchmarkListOffers benchmarks the list offers operation
func BenchmarkListOffers(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{
				Locations: []Location{{
					ID: "loc-123",
					GPUs: []LocationGPU{
						{V0Name: "rtx4090", DisplayName: "RTX 4090 24GB", PricePerHr: 0.50},
					},
				}},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.ListOffers(ctx, models.OfferFilter{})
	}
}

// BenchmarkOfferFiltering benchmarks offer filtering performance
func BenchmarkOfferFiltering(b *testing.B) {
	// Generate a large number of offers
	locations := make([]Location, 50)
	for i := 0; i < 50; i++ {
		gpus := make([]LocationGPU, 20)
		for j := 0; j < 20; j++ {
			gpus[j] = LocationGPU{
				V0Name:      fmt.Sprintf("gpu-%d-%d", i, j),
				DisplayName: fmt.Sprintf("GPU Model %d %dGB", i, (j+1)*8),
				PricePerHr:  float64(j) * 0.1,
				MaxCount:    j + 1,
			}
		}
		locations[i] = Location{
			ID:   fmt.Sprintf("loc-%d", i),
			City: fmt.Sprintf("City %d", i),
			GPUs: gpus,
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{Locations: locations},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL), WithMinInterval(0))
	ctx := context.Background()
	filter := models.OfferFilter{MinVRAM: 24, MaxPrice: 1.0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.ListOffers(ctx, filter)
	}
}
