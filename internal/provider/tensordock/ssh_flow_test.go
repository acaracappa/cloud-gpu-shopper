package tensordock

import (
	"context"
	"encoding/json"
	"fmt"
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
// SSH Key Generation and Encoding Tests
// =============================================================================

// TestBuildSSHKeyCloudInit_BasicEncoding verifies basic SSH key in cloud-init runcmd
func TestBuildSSHKeyCloudInit_BasicEncoding(t *testing.T) {
	// Standard OpenSSH public key format
	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host"

	cloudInit := buildSSHKeyCloudInit(sshKey)

	require.NotNil(t, cloudInit)

	// New implementation uses runcmd only (no write_files) for reliability
	assert.Nil(t, cloudInit.WriteFiles)

	// Verify runcmd contains all commands for both root and user
	// 6 commands for root + 6 commands for user = 12 total
	require.Len(t, cloudInit.RunCmd, 11)

	// Verify root directory and key setup (first 6 commands)
	assert.Contains(t, cloudInit.RunCmd[0], "mkdir -p /root/.ssh")
	assert.Contains(t, cloudInit.RunCmd[1], "chmod 700 /root/.ssh")
	assert.Contains(t, cloudInit.RunCmd[2], sshKey) // echo command with key
	assert.Contains(t, cloudInit.RunCmd[2], "/root/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[3], "chmod 600 /root/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[4], "chown root:root /root/.ssh/authorized_keys")

	// Verify user directory and key setup (next 6 commands)
	assert.Contains(t, cloudInit.RunCmd[5], "mkdir -p /home/user/.ssh")
	assert.Contains(t, cloudInit.RunCmd[6], "chmod 700 /home/user/.ssh")
	assert.Contains(t, cloudInit.RunCmd[7], "chown user:user /home/user/.ssh")
	assert.Contains(t, cloudInit.RunCmd[8], sshKey) // echo command with key
	assert.Contains(t, cloudInit.RunCmd[8], "/home/user/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[9], "chmod 600 /home/user/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[10], "chown user:user /home/user/.ssh/authorized_keys")
}

// TestBuildSSHKeyCloudInit_SpecialCharacters tests SSH keys with special characters
func TestBuildSSHKeyCloudInit_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name   string
		sshKey string
	}{
		{
			name:   "key with single quotes",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAA'quote'test user@host",
		},
		{
			name:   "key with double quotes",
			sshKey: `ssh-rsa AAAAB3NzaC1yc2EAAA"dquote"test user@host`,
		},
		{
			name:   "key with backticks",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAA`backtick`test user@host",
		},
		{
			name:   "key with dollar signs",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAA$VARIABLE$test user@host",
		},
		{
			name:   "key with equals and plus",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAA+test==padding user@host",
		},
		{
			name:   "key with spaces",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC test user with spaces@host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloudInit := buildSSHKeyCloudInit(tt.sshKey)

			require.NotNil(t, cloudInit)
			assert.Nil(t, cloudInit.WriteFiles)
			require.Len(t, cloudInit.RunCmd, 11)

			// Find the echo command for root's authorized_keys
			echoCmd := cloudInit.RunCmd[2]
			assert.Contains(t, echoCmd, "/root/.ssh/authorized_keys")

			// Single quotes should be escaped properly with '\''
			if strings.Contains(tt.sshKey, "'") {
				assert.Contains(t, echoCmd, `'\''`, "single quotes should be escaped")
			}
		})
	}
}

// TestBuildSSHKeyCloudInit_RealKeyFormats tests with realistic SSH key formats
func TestBuildSSHKeyCloudInit_RealKeyFormats(t *testing.T) {
	tests := []struct {
		name   string
		sshKey string
	}{
		{
			name:   "RSA 4096 key",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQDJzL8i5H+OtS2YxFNqJJQm5R5cD5OQS7iu3h9xMYFk1mDq9gJsVA8vAGKlJQI7DU1Rl9EoNCOMM0LGhH3t5TJhQDmKqZp8qZbLEqMkDYKlS user@example.com",
		},
		{
			name:   "Ed25519 key",
			sshKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl user@example.com",
		},
		{
			name:   "ECDSA key",
			sshKey: "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBJnpx8N7aNpOBvgW0Xr1w2hKf1WmEHLfXl0H6ZlG/ZHPHGG4LdRy2ZG7MBmG1+H1H5s7pQ user@example.com",
		},
		{
			name:   "key without comment",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC",
		},
		{
			name:   "key with email comment",
			sshKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC test.user+tag@subdomain.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloudInit := buildSSHKeyCloudInit(tt.sshKey)

			require.NotNil(t, cloudInit)
			assert.Nil(t, cloudInit.WriteFiles)
			require.Len(t, cloudInit.RunCmd, 11)

			// Verify the echo command contains the key for root
			echoCmd := cloudInit.RunCmd[2]
			assert.Contains(t, echoCmd, "/root/.ssh/authorized_keys")
			assert.Contains(t, echoCmd, "echo")

			// Verify the key appears in the command (may be shell-escaped)
			assert.Contains(t, echoCmd, strings.Split(tt.sshKey, " ")[0]) // Key type prefix
		})
	}
}

// TestBuildSSHKeyCloudInit_EmptyKey tests behavior with empty key
func TestBuildSSHKeyCloudInit_EmptyKey(t *testing.T) {
	cloudInit := buildSSHKeyCloudInit("")

	require.NotNil(t, cloudInit)
	// Even with empty key, should still create the structure
	assert.Nil(t, cloudInit.WriteFiles)
	assert.Len(t, cloudInit.RunCmd, 11)

	// Verify echo command exists (even with empty key)
	assert.Contains(t, cloudInit.RunCmd[2], "echo ''")
}

// TestBuildSSHKeyCloudInit_VeryLongKey tests with an unusually long key
func TestBuildSSHKeyCloudInit_VeryLongKey(t *testing.T) {
	// Generate a very long key (8192 bits would be much longer)
	longKey := "ssh-rsa " + strings.Repeat("A", 4096) + " user@host"

	cloudInit := buildSSHKeyCloudInit(longKey)

	require.NotNil(t, cloudInit)
	assert.Nil(t, cloudInit.WriteFiles)
	require.Len(t, cloudInit.RunCmd, 11)

	// Verify the long key is included in the echo command
	echoCmd := cloudInit.RunCmd[2]
	assert.Contains(t, echoCmd, "ssh-rsa")
	assert.Contains(t, echoCmd, "/root/.ssh/authorized_keys")
}

// =============================================================================
// CreateInstance SSH Key Integration Tests
// =============================================================================

// TestCreateInstance_SSHKeyInRequest verifies SSH key is properly included in create request
func TestCreateInstance_SSHKeyInRequest(t *testing.T) {
	var capturedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			// Capture the request body
			err := json.NewDecoder(r.Body).Decode(&capturedRequest)
			require.NoError(t, err)

			// Return success response
			resp := CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					Type:   "virtualmachine",
					ID:     "inst-123",
					Name:   "shopper-session-abc",
					Status: "creating",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	// Offer ID must have valid UUID format (36 chars): xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
		SessionID:    "session-abc",
		SSHPublicKey: TestSSHKey,
		Tags: models.InstanceTags{
			ShopperSessionID: "session-abc",
		},
	}

	_, err := client.CreateInstance(ctx, req)
	require.NoError(t, err)

	// Verify SSH key was included in the request
	assert.Equal(t, TestSSHKey, capturedRequest.Data.Attributes.SSHKey)

	// Verify cloud-init was populated with runcmd (no write_files in new impl)
	require.NotNil(t, capturedRequest.Data.Attributes.CloudInit)
	assert.Nil(t, capturedRequest.Data.Attributes.CloudInit.WriteFiles)
	require.Len(t, capturedRequest.Data.Attributes.CloudInit.RunCmd, 11)

	// Verify the echo command contains the key
	assert.Contains(t, capturedRequest.Data.Attributes.CloudInit.RunCmd[2], "/root/.ssh/authorized_keys")
}

// TestCreateInstance_NoSSHKey verifies behavior when no SSH key is provided
func TestCreateInstance_NoSSHKey(t *testing.T) {
	var capturedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			err := json.NewDecoder(r.Body).Decode(&capturedRequest)
			require.NoError(t, err)

			resp := CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					Type:   "virtualmachine",
					ID:     "inst-123",
					Name:   "shopper-session-abc",
					Status: "creating",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
		SessionID:    "session-abc",
		SSHPublicKey: "", // No SSH key
		Tags: models.InstanceTags{
			ShopperSessionID: "session-abc",
		},
	}

	_, err := client.CreateInstance(ctx, req)
	require.NoError(t, err)

	// Verify SSH key and cloud-init are empty when no key provided
	assert.Empty(t, capturedRequest.Data.Attributes.SSHKey)
	assert.Nil(t, capturedRequest.Data.Attributes.CloudInit)
}

// =============================================================================
// Port Forwarding Tests
// =============================================================================

// TestCreateInstance_PortForwarding verifies SSH port forwarding is configured
func TestCreateInstance_PortForwarding(t *testing.T) {
	var capturedRequest CreateInstanceRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			err := json.NewDecoder(r.Body).Decode(&capturedRequest)
			require.NoError(t, err)

			resp := CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					Type:   "virtualmachine",
					ID:     "inst-123",
					Name:   "shopper-session-abc",
					Status: "creating",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
		SessionID:    "session-abc",
		SSHPublicKey: TestSSHKey,
		Tags: models.InstanceTags{
			ShopperSessionID: "session-abc",
		},
	}

	_, err := client.CreateInstance(ctx, req)
	require.NoError(t, err)

	// Verify port forwarding is configured for SSH
	require.Len(t, capturedRequest.Data.Attributes.PortForwards, 1)
	assert.Equal(t, "tcp", capturedRequest.Data.Attributes.PortForwards[0].Protocol)
	assert.Equal(t, 22, capturedRequest.Data.Attributes.PortForwards[0].InternalPort)
	assert.Equal(t, 22, capturedRequest.Data.Attributes.PortForwards[0].ExternalPort)
}

// TestGetInstanceStatus_DynamicPort verifies dynamic port assignment handling
func TestGetInstanceStatus_DynamicPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/instances/") {
			// TensorDock assigned a different external port than requested
			resp := InstanceResponse{
				Type:      "virtualmachine",
				ID:        "inst-123",
				Name:      "shopper-session-abc",
				Status:    "running",
				IPAddress: "192.168.1.100",
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 20456},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	status, err := client.GetInstanceStatus(ctx, "inst-123")

	require.NoError(t, err)
	assert.Equal(t, "192.168.1.100", status.SSHHost)
	// Should return the ACTUAL external port, not the requested one
	assert.Equal(t, 20456, status.SSHPort)
	assert.Equal(t, "user", status.SSHUser)
	assert.True(t, status.Running)
}

// TestGetInstanceStatus_NoPortForwarding verifies fallback when no port forwarding
func TestGetInstanceStatus_NoPortForwarding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/instances/") {
			// No port forwards in response
			resp := InstanceResponse{
				Type:         "virtualmachine",
				ID:           "inst-123",
				Name:         "shopper-session-abc",
				Status:       "running",
				IPAddress:    "192.168.1.100",
				PortForwards: []PortForward{}, // Empty
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	status, err := client.GetInstanceStatus(ctx, "inst-123")

	require.NoError(t, err)
	// Should default to port 22 when no port forwarding info
	assert.Equal(t, 22, status.SSHPort)
}

// TestGetInstanceStatus_MultiplePortForwards verifies correct port extraction
func TestGetInstanceStatus_MultiplePortForwards(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/instances/") {
			resp := InstanceResponse{
				Type:      "virtualmachine",
				ID:        "inst-123",
				Name:      "shopper-session-abc",
				Status:    "running",
				IPAddress: "192.168.1.100",
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 8080, ExternalPort: 30080},
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 20456}, // SSH
					{Protocol: "tcp", InternalPort: 443, ExternalPort: 30443},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	status, err := client.GetInstanceStatus(ctx, "inst-123")

	require.NoError(t, err)
	// Should find SSH port (internal 22) even among multiple forwards
	assert.Equal(t, 20456, status.SSHPort)
}

// TestGetInstanceStatus_PortMappings verifies all port mappings are returned
func TestGetInstanceStatus_PortMappings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/instances/") {
			resp := InstanceResponse{
				Type:      "virtualmachine",
				ID:        "inst-123",
				Name:      "shopper-session-abc",
				Status:    "running",
				IPAddress: "174.94.145.71",
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 20456},
					{Protocol: "tcp", InternalPort: 8000, ExternalPort: 30000}, // vLLM
					{Protocol: "tcp", InternalPort: 8080, ExternalPort: 30080}, // HTTP
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	status, err := client.GetInstanceStatus(ctx, "inst-123")

	require.NoError(t, err)
	assert.True(t, status.Running)
	assert.Equal(t, "running", status.Status)
	assert.Equal(t, "174.94.145.71", status.SSHHost)
	assert.Equal(t, 20456, status.SSHPort)
	assert.Equal(t, "user", status.SSHUser)

	// Verify PublicIP is populated
	assert.Equal(t, "174.94.145.71", status.PublicIP)

	// Verify all port mappings are returned
	require.NotNil(t, status.Ports)
	assert.Equal(t, 20456, status.Ports[22], "SSH port mapping")
	assert.Equal(t, 30000, status.Ports[8000], "vLLM port mapping")
	assert.Equal(t, 30080, status.Ports[8080], "HTTP port mapping")

	// Verify we can construct API URLs from the port mappings
	apiURL := fmt.Sprintf("http://%s:%d/v1/completions", status.PublicIP, status.Ports[8000])
	assert.Equal(t, "http://174.94.145.71:30000/v1/completions", apiURL)
}

// =============================================================================
// SSH Connection Info Flow Tests
// =============================================================================

// TestCreateInstance_NoIPInCreateResponse verifies IP is not in create response
func TestCreateInstance_NoIPInCreateResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			// TensorDock create response does NOT include IP address
			resp := CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					Type:   "virtualmachine",
					ID:     "inst-123",
					Name:   "shopper-session-abc",
					Status: "creating",
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
		SessionID:    "session-abc",
		SSHPublicKey: TestSSHKey,
		Tags: models.InstanceTags{
			ShopperSessionID: "session-abc",
		},
	}

	info, err := client.CreateInstance(ctx, req)
	require.NoError(t, err)

	// Verify IP is empty in create response
	assert.Empty(t, info.SSHHost, "SSHHost should be empty in create response")
	assert.Equal(t, 22, info.SSHPort, "Default SSH port should be 22")
	assert.Equal(t, "user", info.SSHUser)
}

// TestSSHInfoPollingFlow tests the flow from create to status with SSH info
func TestSSHInfoPollingFlow(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			resp := CreateInstanceResponse{
				Data: CreateInstanceResponseData{
					Type:   "virtualmachine",
					ID:     "inst-123",
					Name:   "shopper-session-abc",
					Status: "creating",
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/instances/") {
			callCount++

			var resp InstanceResponse
			if callCount < 3 {
				// First few calls: instance still creating, no IP
				resp = InstanceResponse{
					Type:   "virtualmachine",
					ID:     "inst-123",
					Name:   "shopper-session-abc",
					Status: "creating",
				}
			} else {
				// After a few calls: instance running with IP
				resp = InstanceResponse{
					Type:      "virtualmachine",
					ID:        "inst-123",
					Name:      "shopper-session-abc",
					Status:    "running",
					IPAddress: "10.0.0.50",
					PortForwards: []PortForward{
						{Protocol: "tcp", InternalPort: 22, ExternalPort: 21234},
					},
				}
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()

	// Create instance
	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
		SessionID:    "session-abc",
		SSHPublicKey: TestSSHKey,
		Tags: models.InstanceTags{
			ShopperSessionID: "session-abc",
		},
	}

	info, err := client.CreateInstance(ctx, req)
	require.NoError(t, err)
	assert.Empty(t, info.SSHHost)

	// Poll for status until we get SSH info
	var status *provider.InstanceStatus
	for i := 0; i < 5; i++ {
		status, err = client.GetInstanceStatus(ctx, info.ProviderInstanceID)
		require.NoError(t, err)
		if status.SSHHost != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.Equal(t, "10.0.0.50", status.SSHHost)
	assert.Equal(t, 21234, status.SSHPort)
	assert.True(t, status.Running)
}

// =============================================================================
// Error Handling Tests
// =============================================================================

// TestCreateInstance_SSHPortError tests error when SSH port not forwarded
func TestCreateInstance_SSHPortError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/instances" {
			// TensorDock returns this error for Ubuntu VMs without SSH port forwarding
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`[{"code":"custom","message":"SSH port (22) must be forwarded for Ubuntu VMs","path":["data","attributes","port_forwards"]}]`))
		}
	}))
	defer server.Close()

	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(0))

	ctx := context.Background()
	req := provider.CreateInstanceRequest{
		OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
		SessionID:    "session-abc",
		SSHPublicKey: TestSSHKey,
		Tags: models.InstanceTags{
			ShopperSessionID: "session-abc",
		},
	}

	_, err := client.CreateInstance(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH port (22) must be forwarded")
}

// TestCreateInstance_StaleInventoryError tests stale inventory detection
func TestCreateInstance_StaleInventoryError(t *testing.T) {
	tests := []struct {
		name         string
		errorMessage string
	}{
		{
			name:         "no available nodes",
			errorMessage: `{"status": 404, "error": "No available nodes found"}`,
		},
		{
			name:         "insufficient capacity",
			errorMessage: `{"status": 404, "error": "Insufficient capacity for requested resources"}`,
		},
		{
			name:         "out of stock",
			errorMessage: `{"status": 404, "error": "GPU model out of stock at this location"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == "/instances" {
					// TensorDock returns 200 OK with error in body for some stale inventory errors
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(tt.errorMessage))
				}
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0))

			ctx := context.Background()
			req := provider.CreateInstanceRequest{
				OfferID:      "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx4090-pcie-24gb",
				SessionID:    "session-abc",
				SSHPublicKey: TestSSHKey,
				Tags: models.InstanceTags{
					ShopperSessionID: "session-abc",
				},
			}

			_, err := client.CreateInstance(ctx, req)
			require.Error(t, err)

			// Should be identified as stale inventory error
			var provErr *provider.ProviderError
			if assert.ErrorAs(t, err, &provErr) {
				assert.ErrorIs(t, provErr.Unwrap(), provider.ErrOfferStaleInventory)
			}
		})
	}
}

// =============================================================================
// Cloud-init JSON Serialization Tests
// =============================================================================

// TestCloudInit_JSONSerialization verifies cloud-init serializes correctly
func TestCloudInit_JSONSerialization(t *testing.T) {
	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC test@host"
	cloudInit := buildSSHKeyCloudInit(sshKey)

	// Serialize to JSON
	data, err := json.Marshal(cloudInit)
	require.NoError(t, err)

	// Verify JSON structure
	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// New implementation uses only runcmd (no write_files)
	_, hasWriteFiles := parsed["write_files"]
	assert.False(t, hasWriteFiles, "write_files should not be present")

	runcmd, ok := parsed["runcmd"].([]interface{})
	require.True(t, ok, "runcmd should be an array")
	assert.Len(t, runcmd, 11)

	// Verify commands are strings
	for _, cmd := range runcmd {
		_, ok := cmd.(string)
		assert.True(t, ok, "each runcmd element should be a string")
	}
}

// TestCreateInstanceRequest_FullSerialization tests complete request serialization
func TestCreateInstanceRequest_FullSerialization(t *testing.T) {
	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC test@host"

	req := CreateInstanceRequest{
		Data: CreateInstanceData{
			Type: "virtualmachine",
			Attributes: CreateInstanceAttributes{
				Name:       "shopper-test-session",
				Type:       "virtualmachine",
				Image:      "ubuntu2404",
				LocationID: "loc-123",
				Resources: ResourcesConfig{
					VCPUCount: 8,
					RAMGb:     32,
					StorageGb: 100,
					GPUs: map[string]GPUCount{
						"geforcertx4090-pcie-24gb": {Count: 1},
					},
				},
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 22},
				},
				SSHKey:    sshKey,
				CloudInit: buildSSHKeyCloudInit(sshKey),
			},
		},
	}

	// Serialize
	data, err := json.Marshal(req)
	require.NoError(t, err)

	// Verify it can be parsed back
	var parsed CreateInstanceRequest
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// Verify critical fields
	assert.Equal(t, "virtualmachine", parsed.Data.Type)
	assert.Equal(t, sshKey, parsed.Data.Attributes.SSHKey)
	assert.NotNil(t, parsed.Data.Attributes.CloudInit)
	assert.Nil(t, parsed.Data.Attributes.CloudInit.WriteFiles)
	assert.Len(t, parsed.Data.Attributes.CloudInit.RunCmd, 11)
}

// =============================================================================
// Edge Cases and Boundary Tests
// =============================================================================

// TestBuildSSHKeyCloudInit_UnicodeKey tests SSH key with unicode characters
func TestBuildSSHKeyCloudInit_UnicodeKey(t *testing.T) {
	// While unusual, SSH key comments could contain unicode
	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC 中文用户@host"

	cloudInit := buildSSHKeyCloudInit(sshKey)

	require.NotNil(t, cloudInit)
	assert.Nil(t, cloudInit.WriteFiles)
	require.Len(t, cloudInit.RunCmd, 11)

	// Verify the key is in the echo command
	echoCmd := cloudInit.RunCmd[2]
	assert.Contains(t, echoCmd, "ssh-rsa")
	assert.Contains(t, echoCmd, "/root/.ssh/authorized_keys")
}

// TestParseOfferID_ValidFormats tests offer ID parsing
func TestParseOfferID_ValidFormats(t *testing.T) {
	tests := []struct {
		name           string
		offerID        string
		wantLocationID string
		wantGPUName    string
		wantErr        bool
	}{
		{
			name:           "standard format",
			offerID:        "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb",
			wantLocationID: "1a779525-4c04-4f2c-aa45-58b47d54bb38",
			wantGPUName:    "geforcertx3090-pcie-24gb",
			wantErr:        false,
		},
		{
			name:           "missing prefix",
			offerID:        "1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090",
			wantLocationID: "",
			wantGPUName:    "",
			wantErr:        true,
		},
		{
			name:           "too short",
			offerID:        "tensordock-123",
			wantLocationID: "",
			wantGPUName:    "",
			wantErr:        true,
		},
		{
			name:           "wrong prefix",
			offerID:        "vastai-1a779525-4c04-4f2c-aa45-58b47d54bb38-gpu",
			wantLocationID: "",
			wantGPUName:    "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locationID, gpuName, err := parseOfferID(tt.offerID)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantLocationID, locationID)
				assert.Equal(t, tt.wantGPUName, gpuName)
			}
		})
	}
}

// =============================================================================
// SSH Key Installation Timing Tests
// =============================================================================

// TestCloudInit_ExpectedExecutionOrder verifies cloud-init structure is correct
func TestCloudInit_ExpectedExecutionOrder(t *testing.T) {
	sshKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC test@host"
	cloudInit := buildSSHKeyCloudInit(sshKey)

	// New implementation uses only runcmd for everything (no write_files)
	// This ensures proper directory creation before file writing
	assert.Nil(t, cloudInit.WriteFiles)
	require.Len(t, cloudInit.RunCmd, 11)

	// Verify execution order for root (first 6 commands)
	assert.Contains(t, cloudInit.RunCmd[0], "mkdir -p /root/.ssh")
	assert.Contains(t, cloudInit.RunCmd[1], "chmod 700 /root/.ssh")
	assert.Contains(t, cloudInit.RunCmd[2], "echo") // Key write
	assert.Contains(t, cloudInit.RunCmd[2], "/root/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[3], "chmod 600 /root/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[4], "chown root:root /root/.ssh/authorized_keys")

	// Verify execution order for user (next 6 commands)
	assert.Contains(t, cloudInit.RunCmd[5], "mkdir -p /home/user/.ssh")
	assert.Contains(t, cloudInit.RunCmd[6], "chmod 700 /home/user/.ssh")
	assert.Contains(t, cloudInit.RunCmd[7], "chown user:user /home/user/.ssh")
	assert.Contains(t, cloudInit.RunCmd[8], "echo") // Key write
	assert.Contains(t, cloudInit.RunCmd[8], "/home/user/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[9], "chmod 600 /home/user/.ssh/authorized_keys")
	assert.Contains(t, cloudInit.RunCmd[10], "chown user:user /home/user/.ssh/authorized_keys")
}

// =============================================================================
// Instance Status State Transitions Tests
// =============================================================================

// TestGetInstanceStatus_StateTransitions tests various instance states
func TestGetInstanceStatus_StateTransitions(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		wantRunning bool
	}{
		{
			name:        "creating state",
			status:      "creating",
			wantRunning: false,
		},
		{
			name:        "running state",
			status:      "running",
			wantRunning: true,
		},
		{
			name:        "stopped state",
			status:      "stopped",
			wantRunning: false,
		},
		{
			name:        "deleting state",
			status:      "deleting",
			wantRunning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resp := InstanceResponse{
					Type:      "virtualmachine",
					ID:        "inst-123",
					Name:      "test",
					Status:    tt.status,
					IPAddress: "10.0.0.1",
				}
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			client := NewClient("test-key", "test-token",
				WithBaseURL(server.URL),
				WithMinInterval(0))

			status, err := client.GetInstanceStatus(context.Background(), "inst-123")
			require.NoError(t, err)

			assert.Equal(t, tt.status, status.Status)
			assert.Equal(t, tt.wantRunning, status.Running)
		})
	}
}
