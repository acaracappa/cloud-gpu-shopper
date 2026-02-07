package tensordock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// Offer ID Parsing Edge Cases
// =============================================================================

func TestParseOfferID_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		offerID     string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty offer ID",
			offerID:     "",
			expectError: true,
			errorMsg:    "missing required",
		},
		{
			name:        "missing tensordock prefix",
			offerID:     "vastai-loc-123-rtx4090",
			expectError: true,
			errorMsg:    "missing required",
		},
		{
			name:        "only prefix",
			offerID:     "tensordock-",
			expectError: true,
			errorMsg:    "too short",
		},
		{
			name:        "prefix with short UUID",
			offerID:     "tensordock-abc123-rtx4090",
			expectError: true,
			errorMsg:    "too short",
		},
		{
			name:        "valid UUID but no GPU name",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38",
			expectError: true,
			errorMsg:    "too short",
		},
		{
			name:        "valid UUID with dash only",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-",
			expectError: true, // UUID + dash is exactly 37 chars, but need 38 minimum
			errorMsg:    "too short",
		},
		{
			name:        "valid format",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090",
			expectError: false,
		},
		{
			name:        "UUID with special characters",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-gpu!@#$%",
			expectError: false, // No validation on GPU name characters
		},
		{
			name:        "very long GPU name",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-" + strings.Repeat("a", 1000),
			expectError: false, // No max length validation
		},
		{
			name:        "whitespace in offer ID",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38- gpu with spaces",
			expectError: false, // Spaces are allowed (though bad practice)
		},
		{
			name:        "newline characters",
			offerID:     "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-gpu\nname",
			expectError: false, // Newlines allowed (though problematic)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locationID, gpuName, err := parseOfferID(tt.offerID)

			if tt.expectError {
				require.Error(t, err, "expected error for offerID: %s", tt.offerID)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, locationID, "locationID should not be empty")
				// GPU name can be empty after dash, just verify we parsed something
				_ = gpuName
			}
		})
	}
}

// =============================================================================
// Network Error Handling
// =============================================================================

func TestClient_ListOffers_NetworkErrors(t *testing.T) {
	tests := []struct {
		name        string
		serverFunc  func(w http.ResponseWriter, r *http.Request)
		expectErr   bool
		errContains string
	}{
		{
			name: "connection refused",
			// Using a server that closes immediately
			serverFunc:  nil, // Will use closed server
			expectErr:   true,
			errContains: "request failed",
		},
		{
			name: "server returns 500",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Internal Server Error"))
			},
			expectErr:   true,
			errContains: "HTTP 500",
		},
		{
			name: "server returns 502 bad gateway",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte("<html>Bad Gateway</html>"))
			},
			expectErr:   true,
			errContains: "HTTP 502",
		},
		{
			name: "server returns 503 service unavailable",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Service Temporarily Unavailable"))
			},
			expectErr:   true,
			errContains: "HTTP 503",
		},
		{
			name: "rate limit exceeded (429)",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte("Rate limit exceeded"))
			},
			expectErr:   true,
			errContains: "rate limit",
		},
		{
			name: "unauthorized (401)",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("Invalid API credentials"))
			},
			expectErr:   true,
			errContains: "HTTP 401",
		},
		{
			name: "forbidden (403)",
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte("Access denied"))
			},
			expectErr:   true,
			errContains: "HTTP 403",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var serverURL string
			if tt.serverFunc == nil {
				// Create and immediately close a server to get a "connection refused" error
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				serverURL = server.URL
				server.Close()
			} else {
				server := httptest.NewServer(http.HandlerFunc(tt.serverFunc))
				defer server.Close()
				serverURL = server.URL
			}

			client := NewClient("test-key", "test-token",
				WithBaseURL(serverURL),
				WithMinInterval(0),
			)

			_, err := client.ListOffers(context.Background(), models.OfferFilter{})

			if tt.expectErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tt.errContains))
				}
			}
		})
	}
}

func TestClient_ListOffers_Timeout(t *testing.T) {
	// Create a server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with short timeout
	shortTimeoutClient := &http.Client{Timeout: 50 * time.Millisecond}
	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithHTTPClient(shortTimeoutClient),
		WithMinInterval(0),
	)

	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestClient_ListOffers_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.ListOffers(ctx, models.OfferFilter{})

	require.Error(t, err)
	// Should be a context error
	assert.Contains(t, err.Error(), "request failed")
}

// =============================================================================
// Invalid JSON Response Handling
// =============================================================================

func TestClient_ListOffers_InvalidJSON(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		expectErr bool
	}{
		{
			name:      "empty response body",
			response:  "",
			expectErr: true,
		},
		{
			name:      "null response",
			response:  "null",
			expectErr: false, // json.Unmarshal handles null
		},
		{
			name:      "malformed JSON",
			response:  "{invalid json}",
			expectErr: true,
		},
		{
			name:      "truncated JSON",
			response:  `{"data": {"locations": [{"id": "123"`,
			expectErr: true,
		},
		{
			name:      "HTML instead of JSON",
			response:  `<!DOCTYPE html><html><body>Error</body></html>`,
			expectErr: true,
		},
		{
			name:      "plain text error",
			response:  `Error: something went wrong`,
			expectErr: true,
		},
		{
			name:      "wrong structure - array instead of object",
			response:  `["item1", "item2"]`,
			expectErr: true,
		},
		{
			name:      "empty data object",
			response:  `{"data": {}}`,
			expectErr: false, // Valid structure, just empty
		},
		{
			name:      "null locations array",
			response:  `{"data": {"locations": null}}`,
			expectErr: false, // Should handle gracefully
		},
		{
			name:      "wrong type for locations",
			response:  `{"data": {"locations": "not an array"}}`,
			expectErr: true,
		},
		{
			name:      "mixed valid/invalid location entries",
			response:  `{"data": {"locations": [{"id": "valid"}, null, {"id": "also-valid"}]}}`,
			expectErr: false, // Go's JSON decoder handles null entries gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0),
			)

			_, err := client.ListOffers(context.Background(), models.OfferFilter{})

			if tt.expectErr {
				require.Error(t, err, "expected error for response: %s", tt.response)
				assert.Contains(t, err.Error(), "decode")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// CreateInstance Edge Cases
// =============================================================================

func TestClient_CreateInstance_InvalidOfferID(t *testing.T) {
	client := NewClient("test-key", "test-token", WithMinInterval(0))

	tests := []struct {
		name    string
		offerID string
	}{
		{"empty", ""},
		{"wrong provider", "vastai-123-rtx4090"},
		{"too short", "tensordock-abc"},
		{"no GPU name", "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := provider.CreateInstanceRequest{
				OfferID:      tt.offerID,
				SSHPublicKey: TestSSHKey,
				Tags:         models.InstanceTags{ShopperSessionID: "test123"},
			}

			_, err := client.CreateInstance(context.Background(), req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid offer ID")
		})
	}
}

func TestClient_CreateInstance_APIErrors(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		response    string
		expectErr   bool
		errContains string
		isStale     bool
	}{
		{
			name:        "validation error array format",
			statusCode:  http.StatusBadRequest,
			response:    `[{"code": "invalid", "message": "Invalid image name", "path": ["image"]}]`,
			expectErr:   true,
			errContains: "Invalid image name",
		},
		{
			name:        "multiple validation errors",
			statusCode:  http.StatusBadRequest,
			response:    `[{"code": "invalid", "message": "Invalid image"}, {"code": "required", "message": "Missing location_id"}]`,
			expectErr:   true,
			errContains: "Invalid image",
		},
		{
			name:        "no available nodes (stale inventory)",
			statusCode:  http.StatusOK,
			response:    `{"status": 400, "error": "No available nodes found"}`,
			expectErr:   true,
			errContains: "No available nodes",
			isStale:     true,
		},
		{
			name:        "insufficient capacity (stale inventory)",
			statusCode:  http.StatusOK,
			response:    `{"status": 400, "error": "Insufficient capacity in region"}`,
			expectErr:   true,
			errContains: "Insufficient capacity",
			isStale:     true,
		},
		{
			name:       "resource unavailable (stale inventory)",
			statusCode: http.StatusOK,
			response:   `{"status": 400, "error": "Resource unavailable"}`,
			expectErr:  true,
			isStale:    true,
		},
		{
			name:       "out of stock (stale inventory)",
			statusCode: http.StatusOK,
			response:   `{"status": 400, "error": "GPU model is out of stock"}`,
			expectErr:  true,
			isStale:    true,
		},
		{
			name:        "HTTP 200 with error in body",
			statusCode:  http.StatusOK,
			response:    `{"status": 500, "error": "Internal processing error"}`,
			expectErr:   true,
			errContains: "Internal processing error",
		},
		{
			name:        "SSH port forwarding required",
			statusCode:  http.StatusBadRequest,
			response:    `[{"code": "invalid", "message": "SSH port (22) must be forwarded for Ubuntu VMs"}]`,
			expectErr:   true,
			errContains: "SSH port",
		},
		{
			name:        "empty instance ID in response",
			statusCode:  http.StatusOK,
			response:    `{"data": {"id": "", "name": "test", "status": "running"}}`,
			expectErr:   true,
			errContains: "empty instance ID",
		},
		{
			name:        "missing data field",
			statusCode:  http.StatusOK,
			response:    `{"id": "123", "name": "test"}`,
			expectErr:   true,
			errContains: "empty instance ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0),
			)

			req := provider.CreateInstanceRequest{
				OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-rtx4090",
				SSHPublicKey: TestSSHKey,
				Tags:         models.InstanceTags{ShopperSessionID: "test123"},
			}

			_, err := client.CreateInstance(context.Background(), req)

			if tt.expectErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				if tt.isStale {
					assert.True(t, provider.IsStaleInventoryError(err), "expected stale inventory error")
				}
			}
		})
	}
}

func TestClient_CreateInstance_RequestValidation(t *testing.T) {
	var capturedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedRequest)

		response := CreateInstanceResponse{
			Data: CreateInstanceResponseData{
				ID:     "inst-123",
				Name:   "test",
				Status: "creating",
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0),
	)

	t.Run("SSH key is base64 encoded in cloud-init", func(t *testing.T) {
		req := provider.CreateInstanceRequest{
			OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-rtx4090",
			SSHPublicKey: TestSSHKey,
			Tags:         models.InstanceTags{ShopperSessionID: "test123"},
		}

		_, err := client.CreateInstance(context.Background(), req)
		require.NoError(t, err)

		// Verify cloud-init was set
		require.NotNil(t, capturedRequest.Data.Attributes.CloudInit)
		assert.True(t, len(capturedRequest.Data.Attributes.CloudInit.RunCmd) > 0)

		// Verify SSH key is included in request
		assert.Equal(t, TestSSHKey, capturedRequest.Data.Attributes.SSHKey)
	})

	t.Run("dedicated IP is requested for SSH access", func(t *testing.T) {
		req := provider.CreateInstanceRequest{
			OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-rtx4090",
			SSHPublicKey: TestSSHKey,
			Tags:         models.InstanceTags{ShopperSessionID: "test123"},
		}

		_, err := client.CreateInstance(context.Background(), req)
		require.NoError(t, err)

		// Verify dedicated IP is requested (replaces port forwarding)
		assert.True(t, capturedRequest.Data.Attributes.UseDedicatedIP, "useDedicatedIp should be true for SSH access")
	})

	t.Run("location ID extracted from offer ID", func(t *testing.T) {
		expectedLocationID := "1a779525-4c04-4f2c-aa45-58b47d54bb38"
		req := provider.CreateInstanceRequest{
			OfferID:      "tensordock-" + expectedLocationID + "-rtx4090",
			SSHPublicKey: TestSSHKey,
			Tags:         models.InstanceTags{ShopperSessionID: "test123"},
		}

		_, err := client.CreateInstance(context.Background(), req)
		require.NoError(t, err)

		assert.Equal(t, expectedLocationID, capturedRequest.Data.Attributes.LocationID)
	})
}

// =============================================================================
// GetInstanceStatus Edge Cases
// =============================================================================

func TestClient_GetInstanceStatus_EdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		instanceID      string
		statusCode      int
		response        string
		expectErr       bool
		errContains     string
		expectedSSHPort int
	}{
		{
			name:        "not found returns specific error",
			instanceID:  "nonexistent",
			statusCode:  http.StatusNotFound,
			response:    `{"error": "Instance not found"}`,
			expectErr:   true,
			errContains: "",
		},
		{
			name:            "no port forwards defaults to port 22",
			instanceID:      "inst-123",
			statusCode:      http.StatusOK,
			response:        `{"id": "inst-123", "status": "running", "ipAddress": "1.2.3.4", "portForwards": []}`,
			expectErr:       false,
			expectedSSHPort: 22,
		},
		{
			name:            "dynamic SSH port assignment",
			instanceID:      "inst-456",
			statusCode:      http.StatusOK,
			response:        `{"id": "inst-456", "status": "running", "ipAddress": "1.2.3.4", "portForwards": [{"internal_port": 22, "external_port": 20456}]}`,
			expectErr:       false,
			expectedSSHPort: 20456,
		},
		{
			name:       "no IP address yet (still provisioning)",
			instanceID: "inst-789",
			statusCode: http.StatusOK,
			response:   `{"id": "inst-789", "status": "creating", "ipAddress": "", "portForwards": []}`,
			expectErr:  false,
		},
		{
			name:        "invalid JSON response",
			instanceID:  "inst-123",
			statusCode:  http.StatusOK,
			response:    `{invalid}`,
			expectErr:   true,
			errContains: "decode",
		},
		{
			name:            "multiple port forwards - finds SSH",
			instanceID:      "inst-multi",
			statusCode:      http.StatusOK,
			response:        `{"id": "inst-multi", "status": "running", "ipAddress": "1.2.3.4", "portForwards": [{"internal_port": 80, "external_port": 8080}, {"internal_port": 22, "external_port": 2222}]}`,
			expectErr:       false,
			expectedSSHPort: 2222,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0),
			)

			status, err := client.GetInstanceStatus(context.Background(), tt.instanceID)

			if tt.expectErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				if tt.statusCode == http.StatusNotFound {
					assert.ErrorIs(t, err, provider.ErrInstanceNotFound)
				}
			} else {
				require.NoError(t, err)
				if tt.expectedSSHPort > 0 {
					assert.Equal(t, tt.expectedSSHPort, status.SSHPort)
				}
			}
		})
	}
}

// =============================================================================
// DestroyInstance Edge Cases
// =============================================================================

func TestClient_DestroyInstance_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		statusCode int
		response   string
		expectErr  bool
	}{
		{
			name:       "already deleted (404) is success",
			instanceID: "deleted-inst",
			statusCode: http.StatusNotFound,
			response:   `{"error": "Instance not found"}`,
			expectErr:  false, // 404 is treated as success
		},
		{
			name:       "successful deletion",
			instanceID: "inst-123",
			statusCode: http.StatusOK,
			response:   `{}`,
			expectErr:  false,
		},
		{
			name:       "no content response",
			instanceID: "inst-456",
			statusCode: http.StatusNoContent,
			response:   ``,
			expectErr:  false,
		},
		{
			name:       "server error",
			instanceID: "inst-789",
			statusCode: http.StatusInternalServerError,
			response:   `{"error": "Internal error"}`,
			expectErr:  true,
		},
		{
			name:       "rate limited",
			instanceID: "inst-rate",
			statusCode: http.StatusTooManyRequests,
			response:   `{"error": "Rate limit exceeded"}`,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0),
			)

			err := client.DestroyInstance(context.Background(), tt.instanceID)

			if tt.expectErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// ListAllInstances Edge Cases
// =============================================================================

func TestClient_ListAllInstances_EdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		response      string
		expectErr     bool
		expectedCount int
	}{
		{
			name:          "empty array format",
			statusCode:    http.StatusOK,
			response:      `{"data": []}`,
			expectErr:     false,
			expectedCount: 0,
		},
		{
			name:          "empty nested format",
			statusCode:    http.StatusOK,
			response:      `{"data": {"instances": []}}`,
			expectErr:     false,
			expectedCount: 0,
		},
		{
			name:          "array format with instances",
			statusCode:    http.StatusOK,
			response:      `{"data": [{"id": "inst-1", "name": "shopper-session1", "status": "running"}, {"id": "inst-2", "name": "other-vm", "status": "running"}]}`,
			expectErr:     false,
			expectedCount: 1, // Only shopper-* instances
		},
		{
			name:          "mixed shopper and non-shopper instances",
			statusCode:    http.StatusOK,
			response:      `{"data": {"instances": [{"id": "inst-1", "name": "shopper-abc"}, {"id": "inst-2", "name": "production-vm"}, {"id": "inst-3", "name": "shopper-xyz"}]}}`,
			expectErr:     false,
			expectedCount: 2,
		},
		{
			name:       "unauthorized returns error",
			statusCode: http.StatusUnauthorized,
			response:   `{"error": "Invalid credentials"}`,
			expectErr:  true,
		},
		{
			name:          "server error returns empty list",
			statusCode:    http.StatusInternalServerError,
			response:      `{"error": "Internal error"}`,
			expectErr:     false,
			expectedCount: 0, // Non-401 errors return empty list
		},
		{
			name:       "invalid JSON array in data",
			statusCode: http.StatusOK,
			response:   `{"data": "not an array or object"}`,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0),
			)

			instances, err := client.ListAllInstances(context.Background())

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, instances, tt.expectedCount)
			}
		})
	}
}

// =============================================================================
// Rate Limiting Behavior
// =============================================================================

func TestClient_RateLimiting(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		json.NewEncoder(w).Encode(LocationsResponse{})
	}))
	defer server.Close()

	// Use a longer interval to test rate limiting
	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(100*time.Millisecond),
	)

	// Make multiple rapid requests
	start := time.Now()
	for i := 0; i < 3; i++ {
		_, _ = client.ListOffers(context.Background(), models.OfferFilter{})
	}
	elapsed := time.Since(start)

	// Should take at least 200ms (2 intervals between 3 requests)
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(180), "Rate limiting should enforce delays")
	assert.Equal(t, 3, requestCount, "All requests should complete")
}

// =============================================================================
// Authentication Header Tests
// =============================================================================

func TestClient_AuthenticationHeaders(t *testing.T) {
	t.Run("/locations uses query params", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Should have api_key and api_token in query
			assert.NotEmpty(t, r.URL.Query().Get("api_key"), "api_key should be in query")
			assert.NotEmpty(t, r.URL.Query().Get("api_token"), "api_token should be in query")
			// Should NOT have Authorization header for /locations
			assert.Empty(t, r.Header.Get("Authorization"), "Authorization header should not be set for /locations")

			json.NewEncoder(w).Encode(LocationsResponse{})
		}))
		defer server.Close()

		client := NewClient("my-key", "my-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)
		_, _ = client.ListOffers(context.Background(), models.OfferFilter{})
	})

	t.Run("/instances uses Bearer token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Should have Bearer token in Authorization header
			authHeader := r.Header.Get("Authorization")
			assert.True(t, strings.HasPrefix(authHeader, "Bearer "), "Should use Bearer token")
			assert.Contains(t, authHeader, "my-token")

			json.NewEncoder(w).Encode(InstancesResponse{})
		}))
		defer server.Close()

		client := NewClient("my-key", "my-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)
		_, _ = client.ListAllInstances(context.Background())
	})
}

// =============================================================================
// Stale Inventory Detection
// =============================================================================

func TestIsStaleInventoryErrorMessage(t *testing.T) {
	tests := []struct {
		message  string
		expected bool
	}{
		// Original patterns
		{"No available nodes found", true},
		{"NO AVAILABLE NODES FOUND", true},
		{"no available nodes in region", true},
		{"Insufficient capacity", true},
		{"insufficient capacity in this location", true},
		{"Not enough capacity for this request", true},
		{"Resource unavailable", true},
		{"GPU resource unavailable", true},
		{"Out of stock", true},
		{"RTX 4090 is out of stock", true},

		// GPU-specific patterns (extended)
		{"GPU unavailable in this location", true},
		{"Requested GPU is not available", true},
		{"No GPUs available for allocation", true},
		{"GPU capacity exceeded", true},
		{"GPU is not available at this time", true},
		{"No matching GPU found", true},

		// Location/datacenter patterns (extended)
		{"Datacenter unavailable due to maintenance", true},
		{"Location unavailable", true},
		{"Service temporarily unavailable", true},
		{"System under maintenance", true},
		{"Region unavailable", true},

		// Demand-related patterns (extended)
		{"High demand - please try again later", true},
		{"GPU type sold out", true},
		{"Resources fully allocated", true},
		{"Datacenter at capacity", true},
		{"Cannot allocate requested resources", true},
		{"Allocation failed", true},

		// Resource limit patterns (extended)
		{"Resource limit reached", true},
		{"Quota exceeded for this account", true},
		{"Maximum instances reached", true},
		{"Limit reached for this type", true},

		// Network resource issues (BUG-010)
		{"No available public IPs on hostnode", true},
		{"No available public ip for this location", true},
		{"Error: no public IP available", true},
		{"NO AVAILABLE PUBLIC IPS", true},

		// Non-stale errors (should return false)
		{"Invalid image name", false},
		{"SSH port required", false},
		{"Authentication failed", false},
		{"Invalid API key", false},
		{"Permission denied", false},
		{"Internal server error", false},
		{"Rate limit exceeded", false},
		{"Validation error", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			result := isStaleInventoryErrorMessage(tt.message)
			assert.Equal(t, tt.expected, result, "message: %s", tt.message)
		})
	}
}

// =============================================================================
// Cloud-Init Edge Cases
// =============================================================================

func TestBuildSSHKeyCloudInit(t *testing.T) {
	tests := []struct {
		name   string
		sshKey string
	}{
		{
			name:   "standard RSA key",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDtest user@host",
		},
		{
			name:   "ed25519 key",
			sshKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest user@host",
		},
		{
			name:   "key with special characters in comment",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAA user+email@example.com",
		},
		{
			name:   "key with quotes",
			sshKey: `ssh-rsa AAAA "quoted comment"`,
		},
		{
			name:   "empty key",
			sshKey: "",
		},
		{
			name:   "very long key",
			sshKey: "ssh-rsa " + strings.Repeat("A", 4096) + " user@host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloudInit := buildSSHKeyCloudInit(tt.sshKey)

			assert.NotNil(t, cloudInit)

			// New implementation uses only runcmd (no write_files)
			assert.Nil(t, cloudInit.WriteFiles)

			// Verify runcmd has all commands for both root and user, plus driver fix
			// 11 SSH commands + 5 NVIDIA driver fix commands = 16 total
			assert.Len(t, cloudInit.RunCmd, 16)
			assert.Contains(t, cloudInit.RunCmd[0], "mkdir -p /root/.ssh")
			assert.Contains(t, cloudInit.RunCmd[5], "mkdir -p /home/user/.ssh")
			// BUG-009/013/014: Verify NVIDIA driver fix commands are present
			assert.Contains(t, cloudInit.RunCmd[11], "unattended-upgrades")  // kill lock holder
			assert.Contains(t, cloudInit.RunCmd[14], "dpkg --configure -a")  // fix partial installs
			assert.Contains(t, cloudInit.RunCmd[15], "nvidia-smi")           // driver fix
		})
	}
}

func TestBuildCloudInit_WithoutDrivers(t *testing.T) {
	// Test that we can disable driver installation
	cloudInit := buildCloudInit("ssh-rsa AAAA test@host", false)
	assert.NotNil(t, cloudInit)
	// Should only have 11 SSH commands, no driver install
	assert.Len(t, cloudInit.RunCmd, 11)
	for _, cmd := range cloudInit.RunCmd {
		assert.NotContains(t, cmd, "nvidia-driver", "Should not contain driver install when disabled")
	}
}

// =============================================================================
// Location Stats Tracking (Stale Inventory Fix)
// =============================================================================

func TestLocationStats_BasicTracking(t *testing.T) {
	stats := newLocationStats()

	// Initial confidence should be default
	assert.Equal(t, TensorDockAvailabilityConfidence, stats.getConfidence("loc-123"))

	// Record some failures
	stats.recordAttempt("loc-123", false)
	stats.recordAttempt("loc-123", false)
	stats.recordAttempt("loc-123", false)

	// Confidence should drop (3 failures, 0 successes = 0%, last failed → 0% * 0.5, but min is 5%)
	confidence := stats.getConfidence("loc-123")
	assert.Equal(t, 0.05, confidence, "Confidence should be at minimum (5%)")
}

func TestLocationStats_SuccessRateCalculation(t *testing.T) {
	stats := newLocationStats()

	// 5 successes, 5 failures = 50%
	for i := 0; i < 5; i++ {
		stats.recordAttempt("loc-456", true)
		stats.recordAttempt("loc-456", false)
	}

	// Last attempt was false (loop alternates true, false), so recency penalty applies:
	// rate = 5/10 = 0.5, then * 0.5 = 0.25
	confidence := stats.getConfidence("loc-456")
	assert.InDelta(t, 0.25, confidence, 0.01, "Confidence should be 25% (50% base with recency penalty)")

	// Record more successes to increase confidence
	for i := 0; i < 10; i++ {
		stats.recordAttempt("loc-456", true)
	}

	// Now: 15 successes, 5 failures = 75%, last was success (no penalty)
	confidence = stats.getConfidence("loc-456")
	assert.InDelta(t, 0.75, confidence, 0.01, "Confidence should be 75%")
}

func TestLocationStats_DifferentLocations(t *testing.T) {
	stats := newLocationStats()

	// Good location: all successes
	for i := 0; i < 10; i++ {
		stats.recordAttempt("good-loc", true)
	}

	// Bad location: all failures
	for i := 0; i < 10; i++ {
		stats.recordAttempt("bad-loc", false)
	}

	assert.Equal(t, 1.0, stats.getConfidence("good-loc"), "Good location should have 100% confidence")
	assert.Equal(t, 0.05, stats.getConfidence("bad-loc"), "Bad location should have minimum 5% confidence")
	assert.Equal(t, TensorDockAvailabilityConfidence, stats.getConfidence("unknown-loc"), "Unknown location should have default confidence")
}

func TestLocationStats_GetStats(t *testing.T) {
	stats := newLocationStats()

	stats.recordAttempt("loc-789", true)
	stats.recordAttempt("loc-789", true)
	stats.recordAttempt("loc-789", false)

	attempts, successes, confidence := stats.getStats("loc-789")
	assert.Equal(t, 3, attempts)
	assert.Equal(t, 2, successes)
	// 2/3 ≈ 0.667, last attempt was failure → 0.667 * 0.5 ≈ 0.333
	assert.InDelta(t, 0.333, confidence, 0.01)
}

// =============================================================================
// GPU Name Parsing Edge Cases
// =============================================================================

func TestNormalizeGPUName_EdgeCases(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"   ", ""},
		// TrimPrefix + TrimSpace behavior: TrimSpace happens first, then prefix removal
		// "NVIDIA " -> TrimSpace -> "NVIDIA" -> no prefix match (prefix is "NVIDIA " with space)
		{"NVIDIA ", "NVIDIA"},   // TrimSpace removes trailing, doesn't match "NVIDIA "
		{"NVIDIA   ", "NVIDIA"}, // Same - after TrimSpace becomes "NVIDIA"
		{"GeForce ", "GeForce"}, // Same logic
		{"Tesla   ", "Tesla"},   // Same logic
		{"NVIDIA GeForce RTX 4090 PCIe 24GB", "RTX 4090"},
		{"  NVIDIA GeForce RTX 4090 PCIe 24GB  ", "RTX 4090"},
		{"Unknown GPU Model", "Unknown GPU Model"},
		{"RTX 4090 PCIe 48GB PCIe 24GB", "RTX 4090 PCIe 48GB"}, // Only last VRAM suffix removed
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("input_%s", tt.input), func(t *testing.T) {
			result := normalizeGPUName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseVRAMFromName_EdgeCases(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"No VRAM info", 0},
		{"24GB", 24},
		{"24 GB", 24},
		{"24gb", 24},                   // Case-insensitive: gb is valid
		{"24 Gb", 24},                  // Case-insensitive: Gb is valid
		{"Multiple 24GB and 48GB", 24}, // Returns first match
		{"0GB", 0},
		{"1024GB", 1024},
		{"1GB", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseVRAMFromName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// Provider Error Classification
// =============================================================================

func TestProviderErrorClassification(t *testing.T) {
	t.Run("rate limit error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Rate limit exceeded"))
		}))
		defer server.Close()

		client := NewClient("test-key", "test-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)

		_, err := client.ListOffers(context.Background(), models.OfferFilter{})
		require.Error(t, err)
		assert.True(t, provider.IsRateLimitError(err))
		assert.True(t, provider.IsRetryable(err))
	})

	t.Run("auth error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Invalid credentials"))
		}))
		defer server.Close()

		client := NewClient("test-key", "test-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)

		_, err := client.ListOffers(context.Background(), models.OfferFilter{})
		require.Error(t, err)
		assert.True(t, provider.IsAuthError(err))
		assert.False(t, provider.IsRetryable(err))
	})

	t.Run("server error is retryable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal error"))
		}))
		defer server.Close()

		client := NewClient("test-key", "test-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)

		_, err := client.ListOffers(context.Background(), models.OfferFilter{})
		require.Error(t, err)
		assert.True(t, provider.IsRetryable(err))
	})
}

// =============================================================================
// Debug Mode
// =============================================================================

func TestClient_DebugMode(t *testing.T) {
	// Just verify debug mode can be enabled without panics
	client := NewClient("test-key", "test-token",
		WithDebug(true),
		WithMinInterval(0),
	)

	assert.True(t, client.debugEnabled)

	// debugLog should not panic
	client.debugLog("test message: %s", "value")
}

// =============================================================================
// Empty/Null Response Handling
// =============================================================================

func TestClient_EmptyResponses(t *testing.T) {
	t.Run("ListOffers with no locations", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(LocationsResponse{
				Data: LocationsData{
					Locations: []Location{},
				},
			})
		}))
		defer server.Close()

		client := NewClient("test-key", "test-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)

		offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
		require.NoError(t, err)
		assert.Empty(t, offers)
	})

	t.Run("ListOffers with location but no GPUs", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(LocationsResponse{
				Data: LocationsData{
					Locations: []Location{
						{
							ID:   "loc-123",
							City: "Test City",
							GPUs: []LocationGPU{},
						},
					},
				},
			})
		}))
		defer server.Close()

		client := NewClient("test-key", "test-token",
			WithBaseURL(server.URL),
			WithMinInterval(0),
		)

		offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
		require.NoError(t, err)
		assert.Empty(t, offers)
	})
}
