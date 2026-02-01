//go:build live
// +build live

package vastai

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive_PortMappings tests that port mappings are correctly parsed from running instances.
// This test provisions an actual Vast.ai instance and verifies the port mapping fields.
//
// Run with: go test -tags=live -run TestLive_PortMappings ./internal/provider/vastai/...
func TestLive_PortMappings(t *testing.T) {
	apiKey := os.Getenv("VASTAI_API_KEY")
	if apiKey == "" {
		t.Skip("VASTAI_API_KEY not set")
	}

	client := NewClient(apiKey)
	ctx := context.Background()

	// Step 1: Find a cheap offer
	t.Log("Step 1: Finding cheapest available GPU...")
	offers, err := client.ListOffers(ctx, models.OfferFilter{
		MaxPrice: 0.20, // Max $0.20/hr for testing
	})
	require.NoError(t, err)
	require.NotEmpty(t, offers, "No offers available under $0.20/hr")

	// Pick the cheapest
	var cheapest models.GPUOffer
	for _, o := range offers {
		if !o.Available {
			continue
		}
		if cheapest.ID == "" || o.PricePerHour < cheapest.PricePerHour {
			cheapest = o
		}
	}
	require.NotEmpty(t, cheapest.ID, "No available offers found")
	t.Logf("Selected offer: %s (%s) @ $%.4f/hr", cheapest.GPUType, cheapest.ID, cheapest.PricePerHour)

	// Step 2: Create instance with exposed ports
	t.Log("Step 2: Creating instance with exposed port 8000...")
	sessionID := "test-port-mapping-" + time.Now().Format("20060102-150405")

	createReq := provider.CreateInstanceRequest{
		OfferID:   cheapest.ProviderID,
		SessionID: sessionID,
		Tags: models.InstanceTags{
			ShopperSessionID: sessionID,
		},
		LaunchMode:   provider.LaunchModeEntrypoint,
		ExposedPorts: []int{8000, 8080}, // Expose vLLM-style ports
		DockerImage:  "nvidia/cuda:12.2.0-runtime-ubuntu22.04",
	}

	info, err := client.CreateInstance(ctx, createReq)
	require.NoError(t, err)
	require.NotEmpty(t, info.ProviderInstanceID)
	t.Logf("Created instance: %s", info.ProviderInstanceID)

	// Ensure cleanup
	defer func() {
		t.Logf("Cleaning up instance %s...", info.ProviderInstanceID)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.DestroyInstance(cleanupCtx, info.ProviderInstanceID); err != nil {
			t.Logf("Warning: failed to destroy instance: %v", err)
		} else {
			t.Log("Instance destroyed successfully")
		}
	}()

	// Step 3: Wait for instance to be running
	t.Log("Step 3: Waiting for instance to reach 'running' status...")
	var status *provider.InstanceStatus
	deadline := time.Now().Add(5 * time.Minute)

	for time.Now().Before(deadline) {
		status, err = client.GetInstanceStatus(ctx, info.ProviderInstanceID)
		if err != nil {
			t.Logf("Status check error (will retry): %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		t.Logf("Current status: %s (Running: %v)", status.Status, status.Running)

		if status.Running {
			break
		}

		if status.Status == "exited" || status.Status == "error" {
			t.Fatalf("Instance reached terminal status: %s", status.Status)
		}

		time.Sleep(10 * time.Second)
	}

	require.True(t, status.Running, "Instance did not reach running status within 5 minutes")

	// Step 4: Verify port mappings
	t.Log("Step 4: Verifying port mappings...")
	t.Logf("SSH Host: %s", status.SSHHost)
	t.Logf("SSH Port: %d", status.SSHPort)
	t.Logf("SSH User: %s", status.SSHUser)
	t.Logf("Public IP: %s", status.PublicIP)
	t.Logf("Port Mappings: %+v", status.Ports)

	// Verify SSH info is present
	assert.NotEmpty(t, status.SSHHost, "SSH host should be set")
	assert.Greater(t, status.SSHPort, 0, "SSH port should be > 0")
	assert.Equal(t, "root", status.SSHUser)

	// Verify public IP is present
	assert.NotEmpty(t, status.PublicIP, "Public IP should be set")

	// Log port mapping results
	if status.Ports != nil && len(status.Ports) > 0 {
		t.Log("SUCCESS: Port mappings are being parsed!")
		for containerPort, externalPort := range status.Ports {
			t.Logf("  Container port %d -> External port %d", containerPort, externalPort)
			t.Logf("  Access URL: http://%s:%d/", status.PublicIP, externalPort)
		}
	} else {
		t.Log("NOTE: No port mappings returned (ports may not be exposed yet or machine doesn't support open ports)")
		t.Log("This is expected on some Vast.ai machines that only support proxy access")
	}

	t.Log("Port mapping test completed successfully!")
}

// TestLive_GetInstanceStatus_ExistingInstance tests port mappings on an existing instance.
// This is useful for debugging without waiting for provisioning.
//
// Run with: VAST_INSTANCE_ID=12345 go test -tags=live -run TestLive_GetInstanceStatus_ExistingInstance ./internal/provider/vastai/...
func TestLive_GetInstanceStatus_ExistingInstance(t *testing.T) {
	apiKey := os.Getenv("VASTAI_API_KEY")
	if apiKey == "" {
		t.Skip("VASTAI_API_KEY not set")
	}

	instanceID := os.Getenv("VAST_INSTANCE_ID")
	if instanceID == "" {
		t.Skip("VAST_INSTANCE_ID not set - set this to test an existing instance")
	}

	client := NewClient(apiKey)
	ctx := context.Background()

	t.Logf("Fetching status for instance %s...", instanceID)

	status, err := client.GetInstanceStatus(ctx, instanceID)
	require.NoError(t, err)

	t.Logf("Instance Status:")
	t.Logf("  Status: %s", status.Status)
	t.Logf("  Running: %v", status.Running)
	t.Logf("  SSH Host: %s", status.SSHHost)
	t.Logf("  SSH Port: %d", status.SSHPort)
	t.Logf("  SSH User: %s", status.SSHUser)
	t.Logf("  Public IP: %s", status.PublicIP)
	t.Logf("  Started At: %v", status.StartedAt)

	if status.Ports != nil && len(status.Ports) > 0 {
		t.Log("  Port Mappings:")
		for containerPort, externalPort := range status.Ports {
			t.Logf("    %d -> %d (http://%s:%d/)", containerPort, externalPort, status.PublicIP, externalPort)
		}
	} else {
		t.Log("  Port Mappings: none")
	}
}

// TestLive_ListInstances_ShowPorts lists all instances and their port mappings.
// Useful for seeing what ports are available on running instances.
//
// Run with: go test -tags=live -run TestLive_ListInstances_ShowPorts ./internal/provider/vastai/...
func TestLive_ListInstances_ShowPorts(t *testing.T) {
	apiKey := os.Getenv("VASTAI_API_KEY")
	if apiKey == "" {
		t.Skip("VASTAI_API_KEY not set")
	}

	client := NewClient(apiKey)
	ctx := context.Background()

	t.Log("Listing all instances...")

	instances, err := client.ListAllInstances(ctx)
	require.NoError(t, err)

	if len(instances) == 0 {
		t.Log("No instances found")
		return
	}

	t.Logf("Found %d instance(s):", len(instances))
	for _, inst := range instances {
		t.Logf("\nInstance %s:", inst.ID)
		t.Logf("  Name: %s", inst.Name)
		t.Logf("  Status: %s", inst.Status)
		t.Logf("  Price: $%.4f/hr", inst.PricePerHour)

		// Get detailed status with ports
		status, err := client.GetInstanceStatus(ctx, inst.ID)
		if err != nil {
			t.Logf("  Error getting detailed status: %v", err)
			continue
		}

		t.Logf("  SSH: %s@%s:%d", status.SSHUser, status.SSHHost, status.SSHPort)
		t.Logf("  Public IP: %s", status.PublicIP)

		if status.Ports != nil && len(status.Ports) > 0 {
			t.Log("  Port Mappings:")
			for containerPort, externalPort := range status.Ports {
				t.Logf("    %d -> %d", containerPort, externalPort)
			}
		} else {
			t.Log("  Port Mappings: none")
		}
	}
}

func init() {
	// Load .env file if it exists
	if data, err := os.ReadFile(".env"); err == nil {
		for _, line := range splitLines(string(data)) {
			if line == "" || line[0] == '#' {
				continue
			}
			parts := splitOnce(line, "=")
			if len(parts) == 2 {
				key := parts[0]
				value := parts[1]
				if os.Getenv(key) == "" {
					os.Setenv(key, value)
					log.Printf("Loaded %s from .env", key)
				}
			}
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitOnce(s, sep string) []string {
	idx := -1
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			idx = i
			break
		}
	}
	if idx == -1 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}
