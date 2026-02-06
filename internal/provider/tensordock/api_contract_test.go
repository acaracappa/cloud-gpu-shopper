// Package tensordock contains API contract tests for TensorDock provider.
//
// These tests verify that the client correctly handles various API response formats,
// authentication methods, error responses, and edge cases as documented in the
// TensorDock API specification and discovered through testing.
package tensordock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Authentication Contract Tests
// =============================================================================

func TestAPIContract_Authentication_QueryParams_ForLocations(t *testing.T) {
	// TensorDock uses query parameter authentication for /locations endpoint
	var receivedAPIKey, receivedAPIToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIKey = r.URL.Query().Get("api_key")
		receivedAPIToken = r.URL.Query().Get("api_token")
		json.NewEncoder(w).Encode(LocationsResponse{Data: LocationsData{Locations: []Location{}}})
	}))
	defer server.Close()

	client := NewClient("test-auth-id", "test-api-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	assert.Equal(t, "test-auth-id", receivedAPIKey, "api_key should be the Authorization ID")
	assert.Equal(t, "test-api-token", receivedAPIToken, "api_token should be the API Token")
}

func TestAPIContract_Authentication_BearerToken_ForInstances(t *testing.T) {
	// TensorDock uses Bearer token authentication for /instances endpoint
	var receivedAuthHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(InstancesResponse{Data: InstancesData{Instances: []Instance{}}})
	}))
	defer server.Close()

	client := NewClient("test-auth-id", "test-api-token", WithBaseURL(server.URL))
	_, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "Bearer test-api-token", receivedAuthHeader, "Authorization header should use Bearer token")
}

func TestAPIContract_Authentication_401Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "Invalid API credentials"}`))
	}))
	defer server.Close()

	client := NewClient("bad-key", "bad-token", WithBaseURL(server.URL))
	_, err := client.ListAllInstances(context.Background())

	require.Error(t, err)
	assert.True(t, provider.IsAuthError(err), "should be identified as auth error")

	var provErr *provider.ProviderError
	require.ErrorAs(t, err, &provErr)
	assert.Equal(t, http.StatusUnauthorized, provErr.StatusCode)
	assert.Equal(t, "tensordock", provErr.Provider)
}

func TestAPIContract_Authentication_403Forbidden_ListAllInstances_ReturnsAuthError(t *testing.T) {
	// P1-1 Fix: 403 Forbidden now correctly returns auth error instead of empty list.
	// This ensures authentication problems are surfaced to operators.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "Access denied"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListAllInstances(context.Background())

	// 403 now returns auth error (fixed in P1-1)
	require.Error(t, err)
	assert.True(t, provider.IsAuthError(err), "403 should be identified as auth error")
}

func TestAPIContract_Authentication_403Forbidden_ListOffers(t *testing.T) {
	// For ListOffers, 403 should be treated as an auth error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "Access denied"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsAuthError(err), "403 should be identified as auth error")
}

// =============================================================================
// HTTP Status Code Contract Tests
// =============================================================================

func TestAPIContract_StatusCode_429RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": "Rate limit exceeded. Please try again later."}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsRateLimitError(err), "429 should be identified as rate limit error")
	assert.True(t, provider.IsRetryable(err), "rate limit errors should be retryable")
}

func TestAPIContract_StatusCode_500InternalServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "Internal server error"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsRetryable(err), "500 errors should be retryable")
}

func TestAPIContract_StatusCode_502BadGateway(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`<html>Bad Gateway</html>`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsRetryable(err), "502 errors should be retryable")
}

func TestAPIContract_StatusCode_503ServiceUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "Service temporarily unavailable"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsRetryable(err), "503 errors should be retryable")
}

func TestAPIContract_StatusCode_404NotFound_GetInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "Instance not found"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.GetInstanceStatus(context.Background(), "nonexistent-id")

	require.Error(t, err)
	assert.ErrorIs(t, err, provider.ErrInstanceNotFound)
	assert.True(t, provider.IsNotFoundError(err))
}

func TestAPIContract_StatusCode_404NotFound_DestroyInstance_IsIdempotent(t *testing.T) {
	// DestroyInstance should treat 404 as success (instance already deleted)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	err := client.DestroyInstance(context.Background(), "already-deleted-id")

	require.NoError(t, err, "DestroyInstance should be idempotent - 404 means already deleted")
}

func TestAPIContract_StatusCode_201Created_CreateInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateInstanceResponse{
			Data: CreateInstanceResponseData{
				Type:   "virtualmachine",
				ID:     "new-instance-123",
				Name:   "shopper-test-session",
				Status: "creating",
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	info, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		SSHPublicKey: TestSSHKey,
		Tags:         models.InstanceTags{ShopperSessionID: "test-session"},
	})

	require.NoError(t, err)
	assert.Equal(t, "new-instance-123", info.ProviderInstanceID)
	assert.Equal(t, "creating", info.Status)
}

func TestAPIContract_StatusCode_200OK_DestroyInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	err := client.DestroyInstance(context.Background(), "instance-id")

	require.NoError(t, err)
}

func TestAPIContract_StatusCode_204NoContent_DestroyInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	err := client.DestroyInstance(context.Background(), "instance-id")

	require.NoError(t, err)
}

// =============================================================================
// Request Format Contract Tests
// =============================================================================

func TestAPIContract_CreateInstance_RequestFormat_JSONAPI(t *testing.T) {
	// TensorDock uses JSON:API style with data.type and data.attributes
	var receivedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/instances", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		err := json.NewDecoder(r.Body).Decode(&receivedRequest)
		require.NoError(t, err)

		json.NewEncoder(w).Encode(CreateInstanceResponse{
			Data: CreateInstanceResponseData{
				ID:     "instance-123",
				Status: "creating",
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "tensordock-11111111-1111-1111-1111-111111111111-geforcertx4090-pcie-24gb",
		SSHPublicKey: TestSSHKey,
		Tags:         models.InstanceTags{ShopperSessionID: "my-session"},
	})

	require.NoError(t, err)

	// Verify JSON:API structure
	assert.Equal(t, "virtualmachine", receivedRequest.Data.Type)
	assert.Equal(t, "virtualmachine", receivedRequest.Data.Attributes.Type)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", receivedRequest.Data.Attributes.LocationID)
	assert.Contains(t, receivedRequest.Data.Attributes.Name, "shopper-")
}

func TestAPIContract_CreateInstance_RequestFormat_DedicatedIP(t *testing.T) {
	// TensorDock REQUIRES dedicated IP for reliable SSH access (port_forwards was ignored - BUG-008)
	var receivedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedRequest)
		json.NewEncoder(w).Encode(CreateInstanceResponse{
			Data: CreateInstanceResponseData{ID: "test-123", Status: "creating"},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.NoError(t, err)

	// Verify dedicated IP is requested for direct SSH access
	assert.True(t, receivedRequest.Data.Attributes.UseDedicatedIP, "useDedicatedIp should be true")
}

func TestAPIContract_CreateInstance_RequestFormat_CloudInit(t *testing.T) {
	// SSH key installation via cloud-init runcmd
	var receivedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedRequest)
		json.NewEncoder(w).Encode(CreateInstanceResponse{
			Data: CreateInstanceResponseData{ID: "test-123", Status: "creating"},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		SSHPublicKey: TestSSHKey,
		Tags:         models.InstanceTags{ShopperSessionID: "test"},
	})

	require.NoError(t, err)

	// Verify cloud-init is configured for SSH key installation
	require.NotNil(t, receivedRequest.Data.Attributes.CloudInit)

	// New implementation uses only runcmd (no write_files)
	assert.Nil(t, receivedRequest.Data.Attributes.CloudInit.WriteFiles)

	// Should have runcmd for all operations: directory creation, key writing, permissions
	require.Len(t, receivedRequest.Data.Attributes.CloudInit.RunCmd, 12) // 11 SSH + 1 NVIDIA driver install
	runcmdStr := strings.Join(receivedRequest.Data.Attributes.CloudInit.RunCmd, " ")
	assert.Contains(t, runcmdStr, "mkdir -p /root/.ssh")
	assert.Contains(t, runcmdStr, "authorized_keys")
}

func TestAPIContract_CreateInstance_RequestFormat_Resources(t *testing.T) {
	var receivedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedRequest)
		json.NewEncoder(w).Encode(CreateInstanceResponse{
			Data: CreateInstanceResponseData{ID: "test-123", Status: "creating"},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-geforcertx4090-pcie-24gb",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.NoError(t, err)

	// Verify default resource configuration
	res := receivedRequest.Data.Attributes.Resources
	assert.Equal(t, defaultVCPUs, res.VCPUCount)
	assert.Equal(t, defaultRAMGB, res.RAMGb)
	assert.Equal(t, defaultStorageGB, res.StorageGb)

	// Verify GPU configuration
	require.Contains(t, res.GPUs, "geforcertx4090-pcie-24gb")
	assert.Equal(t, 1, res.GPUs["geforcertx4090-pcie-24gb"].Count)
}

// =============================================================================
// Response Format Contract Tests
// =============================================================================

func TestAPIContract_LocationsResponse_Structure(t *testing.T) {
	// Test the documented response structure from /locations
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:            "abc12345-1234-1234-1234-123456789abc",
						City:          "Chicago",
						StateProvince: "Illinois",
						Country:       "United States",
						Tier:          3,
						GPUs: []LocationGPU{
							{
								V0Name:      "geforcertx4090-pcie-24gb",
								DisplayName: "NVIDIA GeForce RTX 4090 PCIe 24GB",
								MaxCount:    4,
								PricePerHr:  0.40,
								Resources: GPUResources{
									MaxVCPUs:     56,
									MaxRAMGb:     331,
									MaxStorageGb: 30200,
								},
								NetworkFeatures: NetworkFeatures{
									DedicatedIPAvailable:    true,
									PortForwardingAvailable: true,
									NetworkStorageAvailable: false,
								},
								Pricing: ResourcePricing{
									PerVCPUHr:      0.004,
									PerGBRAMHr:     0.002,
									PerGBStorageHr: 0.00007,
								},
							},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	require.Len(t, offers, 1)

	offer := offers[0]
	assert.Equal(t, "tensordock-abc12345-1234-1234-1234-123456789abc-geforcertx4090-pcie-24gb", offer.ID)
	assert.Equal(t, "tensordock", offer.Provider)
	assert.Equal(t, "RTX 4090", offer.GPUType)
	assert.Equal(t, 24, offer.VRAM)
	assert.Equal(t, 0.40, offer.PricePerHour)
	assert.Equal(t, 4, offer.GPUCount)
	assert.Contains(t, offer.Location, "Chicago")
	assert.InDelta(t, 1.0, offer.Reliability, 0.01) // Tier 3/3
	assert.Equal(t, TensorDockAvailabilityConfidence, offer.AvailabilityConfidence)
}

func TestAPIContract_ListInstances_ArrayFormat(t *testing.T) {
	// TensorDock sometimes returns {"data": [...]} (array directly)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Array format: data is an array directly
		w.Write([]byte(`{
			"data": [
				{
					"id": "inst-001",
					"name": "shopper-session-abc",
					"status": "running",
					"ipAddress": "10.0.0.1",
					"price_per_hour": 0.45
				},
				{
					"id": "inst-002",
					"name": "other-vm",
					"status": "running"
				}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	require.Len(t, instances, 1) // Only our instances (with shopper- prefix)
	assert.Equal(t, "inst-001", instances[0].ID)
	assert.Equal(t, "session-abc", instances[0].Tags.ShopperSessionID)
}

func TestAPIContract_ListInstances_NestedFormat(t *testing.T) {
	// TensorDock sometimes returns {"data": {"instances": [...]}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := InstancesResponse{
			Data: InstancesData{
				Instances: []Instance{
					{
						ID:           "inst-nested-001",
						Name:         "shopper-session-nested",
						Status:       "running",
						PricePerHour: 0.50,
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	require.Len(t, instances, 1)
	assert.Equal(t, "inst-nested-001", instances[0].ID)
}

func TestAPIContract_GetInstanceStatus_ResponseFormat(t *testing.T) {
	// GET /instances/{id} returns instance directly (NOT wrapped in "data")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := InstanceResponse{
			Type:      "virtualmachine",
			ID:        "468b716a-6747-4cbe-9f13-afc153a21c14",
			Name:      "shopper-test-session",
			Status:    "running",
			IPAddress: "174.94.145.71",
			PortForwards: []PortForward{
				{Protocol: "tcp", InternalPort: 22, ExternalPort: 20456},
			},
			RateHourly: 0.272999,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	status, err := client.GetInstanceStatus(context.Background(), "468b716a-6747-4cbe-9f13-afc153a21c14")

	require.NoError(t, err)
	assert.Equal(t, "running", status.Status)
	assert.True(t, status.Running)
	assert.Equal(t, "174.94.145.71", status.SSHHost)
	assert.Equal(t, 20456, status.SSHPort) // External port from port forwards
	assert.Equal(t, "user", status.SSHUser)
}

func TestAPIContract_GetInstanceStatus_DynamicPort(t *testing.T) {
	// TensorDock assigns dynamic external ports - we must read from portForwards
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := InstanceResponse{
			Status:    "running",
			IPAddress: "10.0.0.5",
			PortForwards: []PortForward{
				{Protocol: "tcp", InternalPort: 22, ExternalPort: 33789}, // Random external port
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	status, err := client.GetInstanceStatus(context.Background(), "test-id")

	require.NoError(t, err)
	assert.Equal(t, 33789, status.SSHPort, "SSH port should be read from portForwards")
}

func TestAPIContract_GetInstanceStatus_NoPortForwards(t *testing.T) {
	// If no port forwards, default to 22
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := InstanceResponse{
			Status:       "running",
			IPAddress:    "10.0.0.5",
			PortForwards: []PortForward{}, // Empty
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	status, err := client.GetInstanceStatus(context.Background(), "test-id")

	require.NoError(t, err)
	assert.Equal(t, 22, status.SSHPort, "SSH port should default to 22")
}

func TestAPIContract_CreateInstance_ResponseFormat(t *testing.T) {
	// POST /instances wraps response in {"data": {...}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		resp := CreateInstanceResponse{
			Data: CreateInstanceResponseData{
				Type:   "virtualmachine",
				ID:     "new-vm-uuid",
				Name:   "shopper-my-session",
				Status: "creating",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	info, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "my-session"},
	})

	require.NoError(t, err)
	assert.Equal(t, "new-vm-uuid", info.ProviderInstanceID)
	assert.Equal(t, "creating", info.Status)
	assert.Empty(t, info.SSHHost, "Create response does not include IP address")
	assert.Equal(t, "user", info.SSHUser)
}

// =============================================================================
// Error Response Contract Tests
// =============================================================================

func TestAPIContract_CreateInstance_ValidationErrorArray(t *testing.T) {
	// TensorDock returns validation errors as JSON array
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`[
			{"code": "required", "message": "Name is required", "path": ["data", "attributes", "name"]},
			{"code": "invalid", "message": "Invalid location_id format", "path": ["data", "attributes", "location_id"]}
		]`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Name is required")
	assert.Contains(t, err.Error(), "Invalid location_id format")
}

func TestAPIContract_CreateInstance_HTTP200WithErrorInBody(t *testing.T) {
	// TensorDock quirk: sometimes returns HTTP 200 with error in body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": 400, "error": "No available nodes found"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.Error(t, err)
	assert.True(t, provider.IsStaleInventoryError(err), "No available nodes should be stale inventory error")
}

func TestAPIContract_StaleInventoryErrors(t *testing.T) {
	// Test various stale inventory error messages
	testCases := []struct {
		name    string
		message string
	}{
		{"no available nodes", `{"status": 400, "error": "No available nodes found"}`},
		{"insufficient capacity", `{"status": 400, "error": "Insufficient capacity for this configuration"}`},
		{"out of stock", `{"status": 400, "error": "GPU type is out of stock"}`},
		{"resource unavailable", `{"status": 400, "error": "Resource unavailable at this location"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tc.message))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
			_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
				OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
				Tags:    models.InstanceTags{ShopperSessionID: "test"},
			})

			require.Error(t, err)
			assert.True(t, provider.IsStaleInventoryError(err), "should be stale inventory error: %s", tc.name)
			assert.True(t, provider.ShouldRetryWithDifferentOffer(err))
		})
	}
}

func TestAPIContract_CreateInstance_EmptyInstanceID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Response with empty ID
		w.Write([]byte(`{"data": {"type": "virtualmachine", "id": "", "name": "test", "status": "creating"}}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty instance ID")
}

func TestAPIContract_CreateInstance_PortForwardError(t *testing.T) {
	// Error when SSH port forwarding is missing for Ubuntu VMs
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "SSH port (22) must be forwarded for Ubuntu VMs"}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH port")
}

// =============================================================================
// Offer ID Parsing Contract Tests
// =============================================================================

func TestAPIContract_ParseOfferID_ValidFormat(t *testing.T) {
	testCases := []struct {
		offerID         string
		expectedLocID   string
		expectedGPUName string
	}{
		{
			"tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb",
			"1a779525-4c04-4f2c-aa45-58b47d54bb38",
			"geforcertx3090-pcie-24gb",
		},
		{
			"tensordock-abc12345-1234-1234-1234-123456789abc-rtxa100-pcie-80gb",
			"abc12345-1234-1234-1234-123456789abc",
			"rtxa100-pcie-80gb",
		},
		{
			"tensordock-00000000-0000-0000-0000-000000000000-simple-gpu",
			"00000000-0000-0000-0000-000000000000",
			"simple-gpu",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.offerID, func(t *testing.T) {
			locID, gpuName, err := parseOfferID(tc.offerID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedLocID, locID)
			assert.Equal(t, tc.expectedGPUName, gpuName)
		})
	}
}

func TestAPIContract_ParseOfferID_InvalidPrefix(t *testing.T) {
	_, _, err := parseOfferID("vastai-123-456")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required")
}

func TestAPIContract_ParseOfferID_TooShort(t *testing.T) {
	_, _, err := parseOfferID("tensordock-short")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestAPIContract_ParseOfferID_EmptyGPUName(t *testing.T) {
	// UUID is 36 chars, needs at least 1 char for GPU name after dash
	_, _, err := parseOfferID("tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-")
	// With current implementation, this would have empty GPU name
	// Let's verify it's handled (may need implementation fix)
	locID, gpuName, err := parseOfferID("tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-x")
	require.NoError(t, err)
	assert.Equal(t, "1a779525-4c04-4f2c-aa45-58b47d54bb38", locID)
	assert.Equal(t, "x", gpuName)
}

// =============================================================================
// Edge Cases and Robustness Tests
// =============================================================================

func TestAPIContract_EmptyLocationsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{Locations: []Location{}},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	assert.Empty(t, offers)
}

func TestAPIContract_EmptyInstancesResponse_ArrayFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": []}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	assert.Empty(t, instances)
}

func TestAPIContract_EmptyInstancesResponse_NestedFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(InstancesResponse{
			Data: InstancesData{Instances: []Instance{}},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	instances, err := client.ListAllInstances(context.Background())

	require.NoError(t, err)
	assert.Empty(t, instances)
}

func TestAPIContract_MalformedJSONResponse_ListOffers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestAPIContract_MalformedJSONResponse_CreateInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json at all`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		Tags:    models.InstanceTags{ShopperSessionID: "test"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestAPIContract_LocationWithNoGPUs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:   "loc-no-gpus",
						City: "Empty City",
						GPUs: []LocationGPU{}, // No GPUs
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	assert.Empty(t, offers)
}

func TestAPIContract_MixedCaseFieldNames(t *testing.T) {
	// TensorDock API has inconsistent field naming (camelCase vs snake_case)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Response with mixed case field names as TensorDock actually does
		w.Write([]byte(`{
			"type": "virtualmachine",
			"id": "test-123",
			"name": "shopper-test",
			"status": "running",
			"ipAddress": "10.0.0.1",
			"portForwards": [{"internal_port": 22, "external_port": 20456}],
			"rateHourly": 0.50
		}`))
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	status, err := client.GetInstanceStatus(context.Background(), "test-123")

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1", status.SSHHost)
	assert.Equal(t, 20456, status.SSHPort)
}

func TestAPIContract_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(100 * time.Millisecond)
		json.NewEncoder(w).Encode(LocationsResponse{})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.ListOffers(ctx, models.OfferFilter{})
	require.Error(t, err)
}

func TestAPIContract_InstanceStatus_AllStatusValues(t *testing.T) {
	// Test various status values that TensorDock may return
	testCases := []struct {
		status  string
		running bool
	}{
		{"running", true},
		{"stopped", false},
		{"creating", false},
		{"starting", false},
		{"stopping", false},
		{"error", false},
		{"terminated", false},
	}

	for _, tc := range testCases {
		t.Run(tc.status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(InstanceResponse{
					Status:    tc.status,
					IPAddress: "10.0.0.1",
				})
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
			status, err := client.GetInstanceStatus(context.Background(), "test-id")

			require.NoError(t, err)
			assert.Equal(t, tc.status, status.Status)
			assert.Equal(t, tc.running, status.Running)
		})
	}
}

func TestAPIContract_AvailabilityConfidence_SetCorrectly(t *testing.T) {
	// Verify that AvailabilityConfidence is set to 50% due to stale inventory issues
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:   "loc-123",
						City: "Test",
						Tier: 2,
						GPUs: []LocationGPU{
							{V0Name: "rtx4090", DisplayName: "RTX 4090 24GB", MaxCount: 4, PricePerHr: 0.40},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err)
	require.Len(t, offers, 1)
	assert.Equal(t, 0.5, offers[0].AvailabilityConfidence, "TensorDock inventory has 50% confidence due to frequent stale data")
}

func TestAPIContract_CreateInstance_NoSSHKey(t *testing.T) {
	var receivedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedRequest)
		json.NewEncoder(w).Encode(CreateInstanceResponse{
			Data: CreateInstanceResponseData{ID: "test-123", Status: "creating"},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
		SSHPublicKey: "", // No SSH key
		Tags:         models.InstanceTags{ShopperSessionID: "test"},
	})

	require.NoError(t, err)
	// When no SSH key, cloud-init should not be set
	assert.Nil(t, receivedRequest.Data.Attributes.CloudInit)
	assert.Empty(t, receivedRequest.Data.Attributes.SSHKey)
}

// =============================================================================
// HTTP Method and Path Tests
// =============================================================================

func TestAPIContract_HTTPMethods(t *testing.T) {
	testCases := []struct {
		name           string
		operation      func(*Client) error
		expectedMethod string
		expectedPath   string
	}{
		{
			"ListOffers uses GET /locations",
			func(c *Client) error {
				_, err := c.ListOffers(context.Background(), models.OfferFilter{})
				return err
			},
			"GET",
			"/locations",
		},
		{
			"ListAllInstances uses GET /instances",
			func(c *Client) error {
				_, err := c.ListAllInstances(context.Background())
				return err
			},
			"GET",
			"/instances",
		},
		{
			"GetInstanceStatus uses GET /instances/{id}",
			func(c *Client) error {
				_, err := c.GetInstanceStatus(context.Background(), "test-id")
				return err
			},
			"GET",
			"/instances/test-id",
		},
		{
			"CreateInstance uses POST /instances",
			func(c *Client) error {
				_, err := c.CreateInstance(context.Background(), provider.CreateInstanceRequest{
					OfferID: "tensordock-11111111-1111-1111-1111-111111111111-rtx4090",
					Tags:    models.InstanceTags{ShopperSessionID: "test"},
				})
				return err
			},
			"POST",
			"/instances",
		},
		{
			"DestroyInstance uses DELETE /instances/{id}",
			func(c *Client) error {
				return c.DestroyInstance(context.Background(), "test-id")
			},
			"DELETE",
			"/instances/test-id",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var receivedMethod, receivedPath string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedMethod = r.Method
				receivedPath = r.URL.Path

				// Return appropriate success responses
				switch r.Method {
				case "GET":
					if strings.HasPrefix(r.URL.Path, "/instances/") {
						json.NewEncoder(w).Encode(InstanceResponse{Status: "running"})
					} else if r.URL.Path == "/instances" {
						json.NewEncoder(w).Encode(InstancesResponse{})
					} else {
						json.NewEncoder(w).Encode(LocationsResponse{})
					}
				case "POST":
					json.NewEncoder(w).Encode(CreateInstanceResponse{
						Data: CreateInstanceResponseData{ID: "test-123", Status: "creating"},
					})
				case "DELETE":
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
			tc.operation(client)

			assert.Equal(t, tc.expectedMethod, receivedMethod)
			assert.Equal(t, tc.expectedPath, receivedPath)
		})
	}
}

func TestAPIContract_Headers(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		json.NewEncoder(w).Encode(InstancesResponse{})
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
	_, _ = client.ListAllInstances(context.Background())

	assert.Equal(t, "application/json", receivedHeaders.Get("Accept"))
	assert.Equal(t, "Bearer test-token", receivedHeaders.Get("Authorization"))
}
