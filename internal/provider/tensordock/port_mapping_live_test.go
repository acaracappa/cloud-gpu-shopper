//go:build live
// +build live

package tensordock

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// TestLive_PortMappings tests that port mappings are correctly parsed from running instances.
// This test provisions an actual TensorDock instance and verifies the port mapping fields.
//
// Run with: go test -tags=live -run TestLive_PortMappings ./internal/provider/tensordock/...
func TestLive_PortMappings(t *testing.T) {
	authID := os.Getenv("TENSORDOCK_AUTH_ID")
	apiToken := os.Getenv("TENSORDOCK_API_TOKEN")
	if authID == "" || apiToken == "" {
		t.Skip("TENSORDOCK_AUTH_ID and/or TENSORDOCK_API_TOKEN not set")
	}

	// Use longer timeout for create operations since TensorDock can be slow
	timeouts := DefaultTimeouts()
	timeouts.CreateInstance = 120 * time.Second
	client := NewClient(authID, apiToken, WithDebug(true), WithTimeouts(timeouts))
	ctx := context.Background()

	// Step 1: Find cheap offers
	t.Log("Step 1: Finding available GPU offers...")
	offers, err := client.ListOffers(ctx, models.OfferFilter{
		MaxPrice: 1.00, // Max $1.00/hr for testing
	})
	require.NoError(t, err)
	require.NotEmpty(t, offers, "No offers available under $1.00/hr")

	// Sort by price (find cheapest that actually works)
	// TensorDock has stale inventory, so we may need to try multiple offers
	sortedOffers := make([]models.GPUOffer, 0)
	for _, o := range offers {
		if o.Available {
			sortedOffers = append(sortedOffers, o)
		}
	}
	require.NotEmpty(t, sortedOffers, "No available offers found")

	// Sort by price
	for i := 0; i < len(sortedOffers)-1; i++ {
		for j := i + 1; j < len(sortedOffers); j++ {
			if sortedOffers[j].PricePerHour < sortedOffers[i].PricePerHour {
				sortedOffers[i], sortedOffers[j] = sortedOffers[j], sortedOffers[i]
			}
		}
	}

	t.Logf("Found %d available offers, will try cheapest first", len(sortedOffers))

	// Generate a temporary SSH key for this test
	sshPubKey, err := generateTestSSHKey(t)
	require.NoError(t, err)
	t.Logf("Generated test SSH key: %s...", sshPubKey[:50])

	// Step 2: Try to create instance with progressively more expensive offers
	t.Log("Step 2: Creating instance (trying multiple offers due to stale inventory)...")
	sessionID := "test-port-mapping-" + time.Now().Format("20060102-150405")

	var info *provider.InstanceInfo
	var selectedOffer models.GPUOffer
	maxAttempts := 10 // TensorDock has very stale inventory, need many attempts
	if len(sortedOffers) < maxAttempts {
		maxAttempts = len(sortedOffers)
	}

	for i := 0; i < maxAttempts; i++ {
		offer := sortedOffers[i]
		t.Logf("  Attempt %d: %s @ $%.4f/hr", i+1, offer.GPUType, offer.PricePerHour)

		createReq := provider.CreateInstanceRequest{
			OfferID:      offer.ID,
			SessionID:    sessionID,
			SSHPublicKey: sshPubKey,
			Tags: models.InstanceTags{
				ShopperSessionID: sessionID,
			},
		}

		info, err = client.CreateInstance(ctx, createReq)
		if err == nil {
			selectedOffer = offer
			break
		}

		t.Logf("  Failed: %v", err)

		// Continue trying if this is a stale inventory error or timeout
		errMsg := err.Error()
		if isStaleInventoryErrorMessage(errMsg) ||
			strings.Contains(errMsg, "context deadline exceeded") ||
			strings.Contains(errMsg, "connection refused") {
			continue
		}

		// Real error, don't retry
		require.NoError(t, err, "Unexpected error creating instance")
	}

	require.NotNil(t, info, "Failed to create instance after %d attempts", maxAttempts)
	require.NotEmpty(t, info.ProviderInstanceID)
	t.Logf("Created instance: %s (%s @ $%.4f/hr)", info.ProviderInstanceID, selectedOffer.GPUType, selectedOffer.PricePerHour)

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

	// Step 3: Wait for instance to be running and have IP
	t.Log("Step 3: Waiting for instance to get IP address...")
	var status *provider.InstanceStatus
	deadline := time.Now().Add(5 * time.Minute)

	for time.Now().Before(deadline) {
		status, err = client.GetInstanceStatus(ctx, info.ProviderInstanceID)
		if err != nil {
			t.Logf("Status check error (will retry): %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		t.Logf("Current status: %s (Running: %v, IP: %s)", status.Status, status.Running, status.SSHHost)

		if status.Running && status.SSHHost != "" {
			break
		}

		if status.Status == "stopped" || status.Status == "error" || status.Status == "deleted" {
			t.Fatalf("Instance reached terminal status: %s", status.Status)
		}

		time.Sleep(10 * time.Second)
	}

	require.True(t, status.Running, "Instance did not reach running status within 5 minutes")
	require.NotEmpty(t, status.SSHHost, "Instance did not get IP address within 5 minutes")

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

	// Verify port mappings include SSH port
	if status.Ports != nil && len(status.Ports) > 0 {
		t.Log("SUCCESS: Port mappings are being parsed!")
		for internalPort, externalPort := range status.Ports {
			t.Logf("  Internal port %d -> External port %d", internalPort, externalPort)
			if internalPort == 22 {
				assert.Equal(t, status.SSHPort, externalPort, "SSH port mapping should match SSHPort field")
			}
		}
	} else {
		t.Log("NOTE: No port mappings returned (may still be initializing)")
	}

	t.Log("Port mapping test completed successfully!")
}

// TestLive_ListOffers tests that we can list offers from TensorDock.
//
// Run with: go test -tags=live -run TestLive_ListOffers ./internal/provider/tensordock/...
func TestLive_ListOffers(t *testing.T) {
	authID := os.Getenv("TENSORDOCK_AUTH_ID")
	apiToken := os.Getenv("TENSORDOCK_API_TOKEN")
	if authID == "" || apiToken == "" {
		t.Skip("TENSORDOCK_AUTH_ID and/or TENSORDOCK_API_TOKEN not set")
	}

	client := NewClient(authID, apiToken, WithDebug(true))
	ctx := context.Background()

	t.Log("Listing available GPU offers...")
	offers, err := client.ListOffers(ctx, models.OfferFilter{})
	require.NoError(t, err)

	t.Logf("Found %d offers", len(offers))

	// Show top 10 cheapest
	if len(offers) > 10 {
		offers = offers[:10]
	}

	for i, offer := range offers {
		t.Logf("%d. %s @ $%.4f/hr (VRAM: %dGB, Location: %s)",
			i+1, offer.GPUType, offer.PricePerHour, offer.VRAM, offer.Location)
	}
}

// TestLive_ListInstances_ShowPorts lists all instances and their port mappings.
// Useful for seeing what ports are available on running instances.
//
// Run with: go test -tags=live -run TestLive_ListInstances_ShowPorts ./internal/provider/tensordock/...
func TestLive_ListInstances_ShowPorts(t *testing.T) {
	authID := os.Getenv("TENSORDOCK_AUTH_ID")
	apiToken := os.Getenv("TENSORDOCK_API_TOKEN")
	if authID == "" || apiToken == "" {
		t.Skip("TENSORDOCK_AUTH_ID and/or TENSORDOCK_API_TOKEN not set")
	}

	client := NewClient(authID, apiToken, WithDebug(true))
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
			for internalPort, externalPort := range status.Ports {
				t.Logf("    %d -> %d", internalPort, externalPort)
			}
		} else {
			t.Log("  Port Mappings: none")
		}
	}
}

// TestLive_GetInstanceStatus_ExistingInstance tests port mappings on an existing instance.
// This is useful for debugging without waiting for provisioning.
//
// Run with: TENSORDOCK_INSTANCE_ID=xxx go test -tags=live -run TestLive_GetInstanceStatus_ExistingInstance ./internal/provider/tensordock/...
func TestLive_GetInstanceStatus_ExistingInstance(t *testing.T) {
	authID := os.Getenv("TENSORDOCK_AUTH_ID")
	apiToken := os.Getenv("TENSORDOCK_API_TOKEN")
	if authID == "" || apiToken == "" {
		t.Skip("TENSORDOCK_AUTH_ID and/or TENSORDOCK_API_TOKEN not set")
	}

	instanceID := os.Getenv("TENSORDOCK_INSTANCE_ID")
	if instanceID == "" {
		t.Skip("TENSORDOCK_INSTANCE_ID not set - set this to test an existing instance")
	}

	client := NewClient(authID, apiToken, WithDebug(true))
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

	if status.Ports != nil && len(status.Ports) > 0 {
		t.Log("  Port Mappings:")
		for internalPort, externalPort := range status.Ports {
			t.Logf("    %d -> %d (http://%s:%d/)", internalPort, externalPort, status.PublicIP, externalPort)
		}
	} else {
		t.Log("  Port Mappings: none")
	}
}

// generateTestSSHKey creates a temporary Ed25519 SSH key for testing.
// Returns the public key in OpenSSH format (ssh-ed25519 AAAA... test-key).
func generateTestSSHKey(t *testing.T) (string, error) {
	t.Helper()

	// Generate Ed25519 key pair
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}

	// Convert to SSH public key format
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", err
	}

	// Format as authorized_keys line
	return string(ssh.MarshalAuthorizedKey(sshPubKey)), nil
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
