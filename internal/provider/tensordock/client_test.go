package tensordock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Name(t *testing.T) {
	c := NewClient("test-key", "test-token")
	assert.Equal(t, "tensordock", c.Name())
}

func TestClient_SupportsFeature(t *testing.T) {
	c := NewClient("test-key", "test-token")

	tests := []struct {
		feature  provider.ProviderFeature
		expected bool
	}{
		{provider.FeatureCustomImages, true},
		{provider.FeatureInstanceTags, false},
		{provider.FeatureSpotPricing, false},
		{provider.FeatureIdleDetection, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.feature), func(t *testing.T) {
			assert.Equal(t, tt.expected, c.SupportsFeature(tt.feature))
		})
	}
}

func TestClient_ListOffers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/locations", r.URL.Path)
		// Check auth params
		assert.NotEmpty(t, r.URL.Query().Get("api_key"))
		assert.NotEmpty(t, r.URL.Query().Get("api_token"))

		resp := LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:            "loc-123",
						City:          "Chubbuck",
						StateProvince: "Idaho",
						Country:       "United States",
						Tier:          1,
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
							},
						},
					},
					{
						ID:            "loc-456",
						City:          "Delaware",
						StateProvince: "Wilmington",
						Country:       "United States",
						Tier:          2,
						GPUs: []LocationGPU{
							{
								V0Name:      "rtxa100-pcie-80gb",
								DisplayName: "NVIDIA A100 PCIe 80GB",
								MaxCount:    2,
								PricePerHr:  1.50,
								Resources: GPUResources{
									MaxVCPUs:     32,
									MaxRAMGb:     256,
									MaxStorageGb: 10000,
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
	assert.Len(t, offers, 2)

	// Check first offer
	assert.Equal(t, "tensordock-loc-123-geforcertx4090-pcie-24gb", offers[0].ID)
	assert.Equal(t, "tensordock", offers[0].Provider)
	assert.Equal(t, "RTX 4090", offers[0].GPUType)
	assert.Equal(t, 24, offers[0].VRAM)
	assert.Equal(t, 0.40, offers[0].PricePerHour)
	assert.Contains(t, offers[0].Location, "Chubbuck")

	// Check second offer
	assert.Equal(t, "A100", offers[1].GPUType)
	assert.Equal(t, 80, offers[1].VRAM)
}

func TestClient_ListOffers_WithFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := LocationsResponse{
			Data: LocationsData{
				Locations: []Location{
					{
						ID:   "loc-123",
						City: "Test",
						GPUs: []LocationGPU{
							{
								V0Name:      "rtx4090",
								DisplayName: "RTX 4090 24GB",
								MaxCount:    4,
								PricePerHr:  0.40,
							},
							{
								V0Name:      "rtx3080",
								DisplayName: "RTX 3080 10GB",
								MaxCount:    2,
								PricePerHr:  0.20,
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

	// Filter for >= 20GB VRAM
	filter := models.OfferFilter{
		MinVRAM: 20,
	}
	offers, err := client.ListOffers(context.Background(), filter)

	require.NoError(t, err)
	assert.Len(t, offers, 1)
	assert.Equal(t, 24, offers[0].VRAM)
}

func TestClient_ListAllInstances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/instances", r.URL.Path)

		resp := InstancesResponse{
			Data: InstancesData{
				Instances: []Instance{
					{
						ID:           "inst-abc",
						Name:         "shopper-session123",
						Status:       "running",
						PricePerHour: 0.45,
					},
					{
						ID:     "inst-xyz",
						Name:   "other-vm", // Not ours
						Status: "running",
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
	// Should only return our instances (with shopper- prefix)
	assert.Len(t, instances, 1)
	assert.Equal(t, "inst-abc", instances[0].ID)
	assert.Equal(t, "session123", instances[0].Tags.ShopperSessionID)
}

func TestClient_GetInstanceStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token", WithBaseURL(server.URL))

	_, err := client.GetInstanceStatus(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.ErrorIs(t, err, provider.ErrInstanceNotFound)
}

func TestClient_ListAllInstances_AuthErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{
			name:       "401 Unauthorized returns error",
			statusCode: http.StatusUnauthorized,
			wantError:  true,
		},
		{
			name:       "403 Forbidden returns error",
			statusCode: http.StatusForbidden,
			wantError:  true,
		},
		{
			name:       "404 Not Found returns empty list",
			statusCode: http.StatusNotFound,
			wantError:  false,
		},
		{
			name:       "500 Internal Server Error returns empty list",
			statusCode: http.StatusInternalServerError,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(`{"error": "test error"}`))
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token", WithBaseURL(server.URL))
			instances, err := client.ListAllInstances(context.Background())

			if tt.wantError {
				require.Error(t, err)
				assert.ErrorIs(t, err, provider.ErrProviderAuth)
				assert.Nil(t, instances)
			} else {
				require.NoError(t, err)
				assert.Empty(t, instances)
			}
		})
	}
}

func TestNormalizeGPUName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"NVIDIA GeForce RTX 4090 PCIe 24GB", "RTX 4090"},
		{"NVIDIA A100 PCIe 80GB", "A100"},
		{"GeForce RTX 3090 PCIe 24GB", "RTX 3090"},
		{"Tesla V100 PCIe 32GB", "V100"},
		{"RTX 5090", "RTX 5090"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeGPUName(tt.input))
		})
	}
}

func TestParseVRAMFromName(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"NVIDIA GeForce RTX 4090 PCIe 24GB", 24},
		{"NVIDIA A100 PCIe 80GB", 80},
		{"RTX 3080 10GB", 10},
		{"Some GPU", 0},
		// Case-insensitive tests
		{"RTX 3090 24gb", 24},
		{"RTX 4090 48Gb", 48},
		{"A100 80gB", 80},
		{"Custom GPU 16 GB", 16},
		{"Custom GPU 32 gb", 32},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseVRAMFromName(tt.input))
		})
	}
}

func TestRedactCredentials(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "redacts api_key in URL",
			input:    "https://api.tensordock.com/locations?api_key=secret123&api_token=token456",
			expected: "https://api.tensordock.com/locations?api_key=REDACTED&api_token=REDACTED",
		},
		{
			name:     "redacts api_key at end of URL",
			input:    "https://api.tensordock.com/locations?api_token=token456&api_key=secret123",
			expected: "https://api.tensordock.com/locations?api_token=REDACTED&api_key=REDACTED",
		},
		{
			name:     "no credentials to redact",
			input:    "https://api.tensordock.com/instances",
			expected: "https://api.tensordock.com/instances",
		},
		{
			name:     "only api_key",
			input:    "https://api.tensordock.com/locations?api_key=mysecret",
			expected: "https://api.tensordock.com/locations?api_key=REDACTED",
		},
		{
			name:     "only api_token",
			input:    "https://api.tensordock.com/locations?api_token=mytoken",
			expected: "https://api.tensordock.com/locations?api_token=REDACTED",
		},
		{
			name:     "preserves other query params",
			input:    "https://api.tensordock.com/locations?foo=bar&api_key=secret&baz=qux",
			expected: "https://api.tensordock.com/locations?foo=bar&api_key=REDACTED&baz=qux",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactCredentials(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRedactSSHKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "redacts ssh_key in JSON body",
			input:    `{"data":{"attributes":{"ssh_key":"ssh-rsa AAAAB3NzaC1yc2E... user@host"}}}`,
			expected: `{"data":{"attributes":{"ssh_key":"REDACTED"}}}`,
		},
		{
			name:     "redacts base64 echo command in cloud-init",
			input:    `echo 'c3NoLXJzYSBBQUFBQjNOemFDMXljMkVBQUFBREF...' | base64 -d >> /root/.ssh/authorized_keys`,
			expected: `echo 'REDACTED' | base64 -d >> /root/.ssh/authorized_keys`,
		},
		{
			name:     "preserves non-SSH JSON fields",
			input:    `{"name":"test","ssh_key":"secret-key","status":"running"}`,
			expected: `{"name":"test","ssh_key":"REDACTED","status":"running"}`,
		},
		{
			name:     "handles escaped quotes in SSH key",
			input:    `{"ssh_key":"key with \"quotes\""}`,
			expected: `{"ssh_key":"REDACTED"}`,
		},
		{
			name:     "no SSH key to redact",
			input:    `{"name":"test","status":"running"}`,
			expected: `{"name":"test","status":"running"}`,
		},
		{
			name:     "handles multiple redactions",
			input:    `{"ssh_key":"key1"} and echo 'base64data' | base64 -d`,
			expected: `{"ssh_key":"REDACTED"} and echo 'REDACTED' | base64 -d`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactCredentials(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateSSHPublicKey(t *testing.T) {
	// Valid ED25519 key - this is a real test key
	validED25519Key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICZKc67k8xgOtBqKhxpzM0lJl7rLG/dQTqWBCpHLwEJN test@example"

	tests := []struct {
		name    string
		key     string
		wantErr bool
		errType error
	}{
		{
			name:    "valid ED25519 key",
			key:     validED25519Key,
			wantErr: false,
		},
		{
			name:    "valid key with leading/trailing whitespace",
			key:     "  " + validED25519Key + "  ",
			wantErr: false,
		},
		{
			name:    "empty key",
			key:     "",
			wantErr: true,
			errType: ErrInvalidSSHKey,
		},
		{
			name:    "whitespace only",
			key:     "   ",
			wantErr: true,
			errType: ErrInvalidSSHKey,
		},
		{
			name:    "invalid key format",
			key:     "not-a-valid-ssh-key",
			wantErr: true,
			errType: ErrInvalidSSHKey,
		},
		{
			name:    "invalid base64 in key",
			key:     "ssh-rsa INVALID!!!BASE64 test@example",
			wantErr: true,
			errType: ErrInvalidSSHKey,
		},
		{
			name:    "private key instead of public",
			key:     "-----BEGIN RSA PRIVATE KEY-----",
			wantErr: true,
			errType: ErrInvalidSSHKey,
		},
		{
			name:    "incomplete key type only",
			key:     "ssh-rsa",
			wantErr: true,
			errType: ErrInvalidSSHKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSSHPublicKey(tt.key)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateInstanceID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		wantErr    bool
		errType    error
	}{
		{
			name:       "valid UUID",
			instanceID: "468b716a-6747-4cbe-9f13-afc153a21c14",
			wantErr:    false,
		},
		{
			name:       "valid short ID",
			instanceID: "inst-abc123",
			wantErr:    false,
		},
		{
			name:       "empty instance ID",
			instanceID: "",
			wantErr:    true,
			errType:    ErrInvalidInstanceID,
		},
		{
			name:       "instance ID with forward slash",
			instanceID: "inst-123/../../etc/passwd",
			wantErr:    true,
			errType:    ErrInvalidInstanceID,
		},
		{
			name:       "instance ID with backslash",
			instanceID: "inst-123\\..\\..\\etc\\passwd",
			wantErr:    true,
			errType:    ErrInvalidInstanceID,
		},
		{
			name:       "instance ID with URL-encoded slash",
			instanceID: "inst-123%2f..%2f..%2fetc%2fpasswd",
			wantErr:    true,
			errType:    ErrInvalidInstanceID,
		},
		{
			name:       "instance ID with URL-encoded backslash",
			instanceID: "inst-123%5c..%5c..%5cetc%5cpasswd",
			wantErr:    true,
			errType:    ErrInvalidInstanceID,
		},
		{
			name:       "instance ID too long",
			instanceID: string(make([]byte, 200)),
			wantErr:    true,
			errType:    ErrInvalidInstanceID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstanceID(tt.instanceID)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestClient_GetInstanceStatus_InvalidID(t *testing.T) {
	client := NewClient("test-key", "test-token")

	// Should fail validation before making any HTTP request
	_, err := client.GetInstanceStatus(context.Background(), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInstanceID)

	_, err = client.GetInstanceStatus(context.Background(), "inst/../../etc/passwd")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInstanceID)
}

func TestClient_DestroyInstance_InvalidID(t *testing.T) {
	client := NewClient("test-key", "test-token")

	// Should fail validation before making any HTTP request
	err := client.DestroyInstance(context.Background(), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInstanceID)

	err = client.DestroyInstance(context.Background(), "inst/../../etc/passwd")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidInstanceID)
}

func TestDefaultTimeouts(t *testing.T) {
	timeouts := DefaultTimeouts()

	assert.Equal(t, 30*time.Second, timeouts.ListOffers)
	assert.Equal(t, 60*time.Second, timeouts.CreateInstance)
	assert.Equal(t, 30*time.Second, timeouts.GetStatus)
	assert.Equal(t, 30*time.Second, timeouts.Destroy)
	assert.Equal(t, 30*time.Second, timeouts.ListInstances)
}

func TestWithTimeouts(t *testing.T) {
	customTimeouts := OperationTimeouts{
		ListOffers:     5 * time.Second,
		CreateInstance: 120 * time.Second,
		GetStatus:      10 * time.Second,
		Destroy:        15 * time.Second,
		ListInstances:  20 * time.Second,
	}

	client := NewClient("test-key", "test-token", WithTimeouts(customTimeouts))

	assert.Equal(t, 5*time.Second, client.timeouts.ListOffers)
	assert.Equal(t, 120*time.Second, client.timeouts.CreateInstance)
	assert.Equal(t, 10*time.Second, client.timeouts.GetStatus)
	assert.Equal(t, 15*time.Second, client.timeouts.Destroy)
	assert.Equal(t, 20*time.Second, client.timeouts.ListInstances)
}

func TestContextWithTimeout(t *testing.T) {
	client := NewClient("test-key", "test-token")

	t.Run("applies timeout to context without deadline", func(t *testing.T) {
		ctx := context.Background()
		newCtx, cancel := client.contextWithTimeout(ctx, 5*time.Second)
		defer cancel()

		deadline, ok := newCtx.Deadline()
		require.True(t, ok, "context should have a deadline")
		assert.True(t, time.Until(deadline) <= 5*time.Second)
	})

	t.Run("respects existing shorter deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		newCtx, newCancel := client.contextWithTimeout(ctx, 10*time.Second)
		defer newCancel()

		deadline, ok := newCtx.Deadline()
		require.True(t, ok, "context should have a deadline")
		// Should use the parent's shorter deadline
		assert.True(t, time.Until(deadline) <= 1*time.Second)
	})
}

func TestCircuitBreaker(t *testing.T) {
	t.Run("starts in closed state", func(t *testing.T) {
		cb := newCircuitBreaker(DefaultCircuitBreakerConfig())
		assert.Equal(t, CircuitClosed, cb.State())
		assert.True(t, cb.allow())
	})

	t.Run("opens after threshold failures", func(t *testing.T) {
		config := CircuitBreakerConfig{
			FailureThreshold: 3,
			ResetTimeout:     1 * time.Second,
			MaxBackoff:       1 * time.Minute,
			BaseBackoff:      100 * time.Millisecond,
		}
		cb := newCircuitBreaker(config)

		// Record failures
		for i := 0; i < 3; i++ {
			cb.recordFailure()
		}

		assert.Equal(t, CircuitOpen, cb.State())
		assert.False(t, cb.allow())
	})

	t.Run("resets to half-open after timeout", func(t *testing.T) {
		config := CircuitBreakerConfig{
			FailureThreshold: 2,
			ResetTimeout:     50 * time.Millisecond,
			MaxBackoff:       1 * time.Minute,
			BaseBackoff:      100 * time.Millisecond,
		}
		cb := newCircuitBreaker(config)

		// Open the circuit
		cb.recordFailure()
		cb.recordFailure()
		assert.Equal(t, CircuitOpen, cb.State())

		// Wait for reset timeout
		time.Sleep(60 * time.Millisecond)

		// Should transition to half-open and allow request
		assert.True(t, cb.allow())
		assert.Equal(t, CircuitHalfOpen, cb.State())
	})

	t.Run("closes on success in half-open state", func(t *testing.T) {
		config := CircuitBreakerConfig{
			FailureThreshold: 2,
			ResetTimeout:     10 * time.Millisecond,
			MaxBackoff:       1 * time.Minute,
			BaseBackoff:      100 * time.Millisecond,
		}
		cb := newCircuitBreaker(config)

		// Open the circuit
		cb.recordFailure()
		cb.recordFailure()

		// Wait and transition to half-open
		time.Sleep(15 * time.Millisecond)
		cb.allow()
		assert.Equal(t, CircuitHalfOpen, cb.State())

		// Success closes the circuit
		cb.recordSuccess()
		assert.Equal(t, CircuitClosed, cb.State())
	})

	t.Run("reopens on failure in half-open state", func(t *testing.T) {
		config := CircuitBreakerConfig{
			FailureThreshold: 2,
			ResetTimeout:     10 * time.Millisecond,
			MaxBackoff:       1 * time.Minute,
			BaseBackoff:      100 * time.Millisecond,
		}
		cb := newCircuitBreaker(config)

		// Open the circuit
		cb.recordFailure()
		cb.recordFailure()

		// Wait and transition to half-open
		time.Sleep(15 * time.Millisecond)
		cb.allow()
		assert.Equal(t, CircuitHalfOpen, cb.State())

		// Failure reopens
		cb.recordFailure()
		assert.Equal(t, CircuitOpen, cb.State())
	})

	t.Run("exponential backoff increases", func(t *testing.T) {
		config := CircuitBreakerConfig{
			FailureThreshold: 1,
			ResetTimeout:     10 * time.Millisecond,
			MaxBackoff:       10 * time.Second,
			BaseBackoff:      100 * time.Millisecond,
		}
		cb := newCircuitBreaker(config)

		// First open
		cb.recordFailure()
		backoff1 := cb.getBackoff()

		// Wait and let it try again, fail again
		time.Sleep(15 * time.Millisecond)
		cb.allow() // transitions to half-open
		cb.recordFailure()
		backoff2 := cb.getBackoff()

		assert.True(t, backoff2 > backoff1, "backoff should increase: %v > %v", backoff2, backoff1)
	})

	t.Run("backoff caps at max", func(t *testing.T) {
		config := CircuitBreakerConfig{
			FailureThreshold: 1,
			ResetTimeout:     1 * time.Millisecond,
			MaxBackoff:       500 * time.Millisecond,
			BaseBackoff:      100 * time.Millisecond,
		}
		cb := newCircuitBreaker(config)

		// Cause many failures to increase backoff
		for i := 0; i < 20; i++ {
			cb.recordFailure()
			time.Sleep(2 * time.Millisecond)
			cb.allow()
		}

		backoff := cb.getBackoff()
		assert.LessOrEqual(t, backoff, config.MaxBackoff)
	})
}

func TestClient_CircuitBreakerIntegration(t *testing.T) {
	failCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failCount++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	config := CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     100 * time.Millisecond,
		MaxBackoff:       1 * time.Second,
		BaseBackoff:      50 * time.Millisecond,
	}

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0),
		WithCircuitBreaker(config),
	)

	// First two calls should hit the server and fail
	_, err1 := client.ListOffers(context.Background(), models.OfferFilter{})
	require.Error(t, err1)

	_, err2 := client.ListOffers(context.Background(), models.OfferFilter{})
	require.Error(t, err2)

	// Circuit should now be open - third call should fail without hitting server
	serverCallsBefore := failCount
	_, err3 := client.ListOffers(context.Background(), models.OfferFilter{})
	require.Error(t, err3)
	assert.ErrorIs(t, err3, ErrCircuitOpen)
	assert.Equal(t, serverCallsBefore, failCount, "should not have called server when circuit is open")
}

func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short message unchanged",
			input:    "Error: resource not found",
			expected: "Error: resource not found",
		},
		{
			name:     "removes newlines",
			input:    "Error\nwith\nnewlines",
			expected: "Error with newlines",
		},
		{
			name:     "removes carriage returns",
			input:    "Error\r\nwith\r\nCRLF",
			expected: "Error  with  CRLF",
		},
		{
			name:     "truncates long message",
			input:    string(make([]byte, 2000)),
			expected: string(make([]byte, 1000)) + "... [truncated]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeErrorMessage(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLocationGPUToOffer(t *testing.T) {
	loc := Location{
		ID:            "loc-123",
		City:          "TestCity",
		StateProvince: "TestState",
		Country:       "TestCountry",
		Tier:          2,
	}

	gpu := LocationGPU{
		V0Name:      "geforcertx4090-pcie-24gb",
		DisplayName: "NVIDIA GeForce RTX 4090 PCIe 24GB",
		MaxCount:    4,
		PricePerHr:  0.40,
	}

	offer := locationGPUToOffer(loc, gpu)

	assert.Equal(t, "tensordock-loc-123-geforcertx4090-pcie-24gb", offer.ID)
	assert.Equal(t, "tensordock", offer.Provider)
	assert.Equal(t, "loc-123", offer.ProviderID)
	assert.Equal(t, "RTX 4090", offer.GPUType)
	assert.Equal(t, 4, offer.GPUCount)
	assert.Equal(t, 24, offer.VRAM)
	assert.Equal(t, 0.40, offer.PricePerHour)
	assert.Contains(t, offer.Location, "TestCity")
	assert.InDelta(t, 0.67, offer.Reliability, 0.01) // Tier 2/3
}
