//go:build e2e
// +build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGhostDetection tests that ghost sessions (exist in DB but not on provider) are detected and fixed
func TestGhostDetection(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	// Create a legitimate session through the API
	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	t.Logf("Created session: %s", sessionID)

	// Cleanup session on test failure
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	session := env.WaitForStatus(t, sessionID, "running", 10*time.Second)
	t.Logf("Session is running")

	// Get the provider instance ID (should be something like "mock-instance-xxxx")
	// The mock provider uses numeric IDs, let's find it
	providerInstances := env.ListProviderInstances(t)
	t.Logf("Provider instances before deletion: %v", providerInstances)
	require.NotEmpty(t, providerInstances, "Should have at least one instance on provider")

	// Get initial ghost metrics
	_, _, initialGhostsFound, initialGhostsFixed := env.GetReconcileMetrics(t)

	// Delete the instance directly from the provider (simulating provider-side failure)
	// This creates a "ghost" - session in DB but instance gone from provider
	instanceID := providerInstances[0] // Use the first instance (should be our session's)
	t.Logf("Deleting instance %s directly from provider to create ghost", instanceID)
	env.DeleteInstanceFromProvider(t, instanceID)

	// Verify instance is gone from provider
	providerInstances = env.ListProviderInstances(t)
	assert.NotContains(t, providerInstances, instanceID, "Instance should be deleted from provider")

	// Session should still show as running in DB (ghost state)
	session = env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Session should still show as running before reconciliation")

	// Run reconciliation to detect the ghost
	t.Log("Running reconciliation to detect ghost...")
	env.RunReconciliation(t)

	// Wait for reconciliation to complete and session to be marked as stopped
	require.Eventually(t, func() bool {
		session = env.GetSession(t, sessionID)
		return session.Status == "stopped"
	}, 10*time.Second, 100*time.Millisecond, "Ghost session should be marked as stopped")

	// Check metrics - ghost should be detected and fixed
	_, _, ghostsFound, ghostsFixed := env.GetReconcileMetrics(t)
	t.Logf("Metrics: ghosts_found=%d (was %d), ghosts_fixed=%d (was %d)",
		ghostsFound, initialGhostsFound, ghostsFixed, initialGhostsFixed)

	assert.Greater(t, ghostsFound, initialGhostsFound, "Should have detected the ghost")
	assert.Greater(t, ghostsFixed, initialGhostsFixed, "Should have fixed the ghost")

	// Session should now be marked as stopped
	assert.Equal(t, "stopped", session.Status, "Ghost session should be marked as stopped")
	assert.Contains(t, session.Error, "not found", "Error should indicate instance not found")

	t.Log("Ghost detection test completed successfully")
}

// TestGhostDetectionPreservesRunningInstances verifies that sessions with valid instances are not affected
func TestGhostDetectionPreservesRunningInstances(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	// Create a legitimate session
	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Get initial metrics
	_, _, initialGhostsFound, _ := env.GetReconcileMetrics(t)

	// Run reconciliation - should not detect any ghosts since instance exists
	t.Log("Running reconciliation with valid instance...")
	env.RunReconciliation(t)

	// Wait for reconciliation to complete by verifying session remains running
	require.Eventually(t, func() bool {
		session := env.GetSession(t, sessionID)
		// Session should remain running (not turned into a ghost)
		return session.Status == "running"
	}, 10*time.Second, 100*time.Millisecond, "Legitimate session should remain running")

	// Check that no new ghosts were detected
	_, _, ghostsFound, _ := env.GetReconcileMetrics(t)
	assert.Equal(t, initialGhostsFound, ghostsFound, "No new ghosts should be detected")

	// Session should still be running
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Session should still be running")

	// Cleanup
	env.DeleteSession(t, sessionID)
	t.Log("Preserve running instances test completed successfully")
}

// TestMultipleGhostDetection tests detection of multiple ghost sessions
func TestMultipleGhostDetection(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.GreaterOrEqual(t, len(inventory.Offers), 2, "Need at least 2 offers")

	// Create multiple sessions
	var sessionIDs []string
	for i := 0; i < 2; i++ {
		createResp := env.CreateSession(t, CreateSessionRequest{
			ConsumerID:     GenerateConsumerID(),
			OfferID:        inventory.Offers[i].ID,
			WorkloadType:   "llm",
			ReservationHrs: 1,
		})
		sessionIDs = append(sessionIDs, createResp.Session.ID)
		t.Logf("Created session %d: %s", i+1, createResp.Session.ID)
	}

	// Cleanup sessions on test failure
	defer env.Cleanup(t, sessionIDs...)

	// Wait for all running (SSH verification completes automatically)
	for _, sessionID := range sessionIDs {
		env.WaitForStatus(t, sessionID, "running", 10*time.Second)
	}

	// Get initial metrics
	_, _, initialGhostsFound, initialGhostsFixed := env.GetReconcileMetrics(t)

	// Delete ALL instances from provider (create multiple ghosts)
	providerInstances := env.ListProviderInstances(t)
	t.Logf("Deleting %d instances to create ghosts", len(providerInstances))
	for _, instID := range providerInstances {
		env.DeleteInstanceFromProvider(t, instID)
	}

	// Run reconciliation
	t.Log("Running reconciliation to detect multiple ghosts...")
	env.RunReconciliation(t)

	// Wait for reconciliation to complete and all sessions to be marked as stopped
	require.Eventually(t, func() bool {
		for _, sessionID := range sessionIDs {
			session := env.GetSession(t, sessionID)
			if session.Status != "stopped" {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond, "All ghost sessions should be marked as stopped")

	// Check metrics
	_, _, ghostsFound, ghostsFixed := env.GetReconcileMetrics(t)
	expectedGhosts := initialGhostsFound + int64(len(sessionIDs))

	t.Logf("Metrics: ghosts_found=%d (expected %d), ghosts_fixed=%d",
		ghostsFound, expectedGhosts, ghostsFixed)

	assert.GreaterOrEqual(t, ghostsFound, expectedGhosts, "Should have detected all ghosts")
	assert.GreaterOrEqual(t, ghostsFixed, initialGhostsFixed+int64(len(sessionIDs)), "Should have fixed all ghosts")

	// All sessions should be stopped
	for _, sessionID := range sessionIDs {
		session := env.GetSession(t, sessionID)
		assert.Equal(t, "stopped", session.Status, "Ghost session should be marked as stopped")
	}

	t.Log("Multiple ghost detection test completed successfully")
}
