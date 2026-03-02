//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProvisionDestroy tests the full provision → destroy lifecycle
func TestProvisionDestroy(t *testing.T) {
	env := NewTestEnv()

	// Wait for services
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)

	// Reset mock provider state
	env.ResetMockProvider(t)

	// Step 1: List inventory
	t.Log("Step 1: Listing inventory...")
	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers, "Should have available offers")
	t.Logf("Found %d offers", inventory.Count)

	// Pick the first available offer
	offer := inventory.Offers[0]
	t.Logf("Selected offer: %s (%s) at $%.2f/hr", offer.ID, offer.GPUType, offer.PricePerHour)

	// Step 2: Create session
	t.Log("Step 2: Creating session...")
	consumerID := GenerateConsumerID()
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     consumerID,
		OfferID:        offer.ID,
		WorkloadType:   "llm",
		ReservationHrs: 2,
	})

	sessionID := createResp.Session.ID
	require.NotEmpty(t, sessionID, "Session ID should not be empty")
	require.NotEmpty(t, createResp.SSHPrivateKey, "SSH private key should be provided")
	t.Logf("Created session: %s", sessionID)

	// Cleanup on test end
	defer env.Cleanup(t, sessionID)

	// Step 3: Verify initial status
	t.Log("Step 3: Verifying initial status...")
	session := env.GetSession(t, sessionID)
	assert.Equal(t, consumerID, session.ConsumerID)
	assert.Contains(t, []string{"pending", "provisioning"}, session.Status)

	// Step 4: Wait for running status (SSH verification completes automatically)
	t.Log("Step 4: Waiting for running status...")
	session = env.WaitForStatus(t, sessionID, "running", 10*time.Second)
	assert.Equal(t, "running", session.Status)
	assert.NotEmpty(t, session.SSHHost, "SSH host should be set")
	t.Logf("Session running: SSH=%s:%d", session.SSHHost, session.SSHPort)

	// Step 5: Verify session details
	t.Log("Step 5: Verifying session details...")
	assert.Equal(t, offer.GPUType, session.GPUType)
	assert.Equal(t, 2, session.ReservationHrs)
	assert.False(t, session.ExpiresAt.IsZero(), "ExpiresAt should be set")
	assert.True(t, session.ExpiresAt.After(time.Now()), "ExpiresAt should be in the future")

	// Step 6: Delete session
	t.Log("Step 6: Deleting session...")
	env.DeleteSession(t, sessionID)

	// Step 7: Verify stopped status
	t.Log("Step 7: Verifying stopped status...")
	session = env.WaitForStatusWithRetry(t, sessionID, "stopped", 30*time.Second)
	assert.Equal(t, "stopped", session.Status)

	t.Log("Provision/destroy test completed successfully")
}

// TestProvisionWithStoragePolicy tests session creation with storage policy
func TestProvisionWithStoragePolicy(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	// List inventory
	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session with preserve storage policy
	consumerID := GenerateConsumerID()
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     consumerID,
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "training",
		ReservationHrs: 1,
		StoragePolicy:  "preserve",
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Verify session was created
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status)
	assert.Equal(t, "training", session.WorkloadType)

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestProvisionWithIdleThreshold tests session creation with idle threshold
func TestProvisionWithIdleThreshold(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session with idle threshold
	consumerID := GenerateConsumerID()
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     consumerID,
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
		IdleThreshold:  30, // 30 minutes
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	session := env.WaitForStatus(t, sessionID, "running", 10*time.Second)
	assert.Equal(t, "running", session.Status)

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestSignalDone tests the signal done flow
func TestSignalDone(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "batch",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Signal done
	t.Log("Signaling session done...")
	env.SignalDone(t, sessionID)

	// The session should eventually be stopped
	// Note: In real implementation, this triggers graceful shutdown
	// For now, we just verify the API accepts the request
	t.Log("Signal done accepted")
}

// TestExtendSession tests session extension
func TestExtendSession(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session with 1 hour reservation
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	session := env.WaitForStatus(t, sessionID, "running", 10*time.Second)
	originalExpiry := session.ExpiresAt

	// Extend by 2 hours
	t.Log("Extending session by 2 hours...")
	env.ExtendSession(t, sessionID, 2)

	// Verify new expiry
	session = env.GetSession(t, sessionID)
	expectedExpiry := originalExpiry.Add(2 * time.Hour)
	assert.WithinDuration(t, expectedExpiry, session.ExpiresAt, time.Minute)
	t.Logf("Session extended: old=%v new=%v", originalExpiry, session.ExpiresAt)

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestMultipleSessions tests creating multiple concurrent sessions
func TestMultipleSessions(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.GreaterOrEqual(t, len(inventory.Offers), 2, "Need at least 2 offers for this test")

	consumerID := GenerateConsumerID()
	var sessionIDs []string

	// Create multiple sessions with different offers
	for i := 0; i < 2; i++ {
		createResp := env.CreateSession(t, CreateSessionRequest{
			ConsumerID:     consumerID,
			OfferID:        inventory.Offers[i].ID,
			WorkloadType:   "llm",
			ReservationHrs: 1,
		})
		sessionIDs = append(sessionIDs, createResp.Session.ID)
		t.Logf("Created session %d: %s", i+1, createResp.Session.ID)
	}

	defer env.Cleanup(t, sessionIDs...)

	// Wait for all to be running (SSH verification completes automatically)
	for _, sessionID := range sessionIDs {
		env.WaitForStatus(t, sessionID, "running", 10*time.Second)
	}

	t.Logf("All %d sessions running", len(sessionIDs))

	// Cleanup all
	for _, sessionID := range sessionIDs {
		env.DeleteSession(t, sessionID)
	}
}

// TestProvisionFailure tests handling of provision failures
// NOTE: This test is skipped because the in-process MockProviderAdapter
// returns hardcoded success values and doesn't use the mock provider's
// HTTP API for instance creation. Failure injection is tested in unit tests
// at test/mockprovider/server_test.go.
func TestProvisionFailure(t *testing.T) {
	t.Skip("Skipped: In-process MockProviderAdapter doesn't support failure injection. See unit tests.")

	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	// Configure mock provider to fail
	env.ConfigureMockProvider(t, MockProviderConfig{
		FailCreate:    true,
		FailCreateMsg: "simulated provider failure",
	})

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Try to create session - this should fail
	reqBody := CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
	}

	// We expect the creation to fail, so use lower-level HTTP
	body, err := jsonMarshal(reqBody)
	require.NoError(t, err)
	resp, err := env.HTTPClient.Post(env.ServerURL+"/api/v1/sessions", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return error status
	assert.NotEqual(t, 201, resp.StatusCode, "Should not create session successfully")

	// Reset mock provider for cleanup
	env.ResetMockProvider(t)
}

// Helper to marshal JSON
func jsonMarshal(v interface{}) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(v)
	return buf, err
}
