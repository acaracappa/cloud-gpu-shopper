//go:build e2e
// +build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrphanDetection tests that orphan instances (exist on provider but not in DB) are detected and destroyed
func TestOrphanDetection(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	// Get initial metrics
	initialOrphans, initialDestroyed, _, _ := env.GetReconcileMetrics(t)

	// Create an orphan instance directly in the mock provider (bypassing API)
	// This simulates an instance that exists on the provider but we have no DB record for
	orphanLabel := GenerateSessionLabel()
	t.Logf("Creating orphan instance with label: %s", orphanLabel)
	orphanID := env.CreateOrphanInstance(t, orphanLabel)
	require.NotEmpty(t, orphanID, "Orphan instance ID should not be empty")
	t.Logf("Created orphan instance: %s", orphanID)

	// Cleanup orphan if test fails before reconciliation destroys it
	defer func() {
		instances := env.ListProviderInstances(t)
		for _, instID := range instances {
			if instID == orphanID {
				t.Logf("Cleanup: deleting orphan instance %s", orphanID)
				env.DeleteInstanceFromProvider(t, orphanID)
				break
			}
		}
	}()

	// Verify instance exists on provider
	instances := env.ListProviderInstances(t)
	assert.Contains(t, instances, orphanID, "Orphan should exist on provider")

	// Run reconciliation
	t.Log("Running reconciliation...")
	env.RunReconciliation(t)

	// Wait for reconciliation to complete and verify orphan is destroyed
	require.Eventually(t, func() bool {
		instances := env.ListProviderInstances(t)
		for _, id := range instances {
			if id == orphanID {
				return false // Still exists
			}
		}
		return true // Destroyed
	}, 10*time.Second, 100*time.Millisecond, "Orphan should be destroyed by reconciliation")

	// Check metrics - orphan should be detected and destroyed
	orphansFound, orphansDestroyed, _, _ := env.GetReconcileMetrics(t)
	t.Logf("Metrics: orphans_found=%d (was %d), orphans_destroyed=%d (was %d)",
		orphansFound, initialOrphans, orphansDestroyed, initialDestroyed)

	assert.Greater(t, orphansFound, initialOrphans, "Should have detected at least one orphan")
	assert.Greater(t, orphansDestroyed, initialDestroyed, "Should have destroyed at least one orphan")

	t.Log("Orphan detection test completed successfully")
}

// TestOrphanDetectionWithMultipleOrphans tests detection of multiple orphan instances
func TestOrphanDetectionWithMultipleOrphans(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	// Get initial metrics
	initialOrphans, initialDestroyed, _, _ := env.GetReconcileMetrics(t)

	// Create multiple orphan instances
	orphanIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		label := GenerateSessionLabel()
		orphanIDs[i] = env.CreateOrphanInstance(t, label)
		t.Logf("Created orphan %d: %s", i+1, orphanIDs[i])
	}

	// Cleanup orphans if test fails before reconciliation destroys them
	defer func() {
		instances := env.ListProviderInstances(t)
		instanceSet := make(map[string]bool)
		for _, id := range instances {
			instanceSet[id] = true
		}
		for _, orphanID := range orphanIDs {
			if instanceSet[orphanID] {
				t.Logf("Cleanup: deleting orphan instance %s", orphanID)
				env.DeleteInstanceFromProvider(t, orphanID)
			}
		}
	}()

	// Verify all orphans exist
	instances := env.ListProviderInstances(t)
	for _, id := range orphanIDs {
		assert.Contains(t, instances, id, "Orphan should exist on provider")
	}

	// Run reconciliation
	t.Log("Running reconciliation...")
	env.RunReconciliation(t)

	// Wait for reconciliation to complete and verify all orphans are destroyed
	require.Eventually(t, func() bool {
		instances := env.ListProviderInstances(t)
		instanceSet := make(map[string]bool)
		for _, id := range instances {
			instanceSet[id] = true
		}
		for _, orphanID := range orphanIDs {
			if instanceSet[orphanID] {
				return false // At least one orphan still exists
			}
		}
		return true // All orphans destroyed
	}, 10*time.Second, 100*time.Millisecond, "All orphans should be destroyed by reconciliation")

	// Check metrics
	orphansFound, orphansDestroyed, _, _ := env.GetReconcileMetrics(t)
	expectedFound := initialOrphans + 3
	expectedDestroyed := initialDestroyed + 3

	t.Logf("Metrics: orphans_found=%d (expected %d), orphans_destroyed=%d (expected %d)",
		orphansFound, expectedFound, orphansDestroyed, expectedDestroyed)

	assert.GreaterOrEqual(t, orphansFound, expectedFound, "Should have detected all orphans")
	assert.GreaterOrEqual(t, orphansDestroyed, expectedDestroyed, "Should have destroyed all orphans")

	t.Log("Multiple orphan detection test completed successfully")
}

// TestLegitimateSessionNotOrphan verifies that legitimate sessions are not treated as orphans
func TestLegitimateSessionNotOrphan(t *testing.T) {
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
	defer env.Cleanup(t, sessionID)

	// Wait for running (SSH verification completes automatically)
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Get metrics before reconciliation
	initialOrphans, _, _, _ := env.GetReconcileMetrics(t)

	// Run reconciliation
	t.Log("Running reconciliation with legitimate session...")
	env.RunReconciliation(t)

	// Wait for reconciliation to complete by polling until metrics stabilize
	// (no new orphans should be detected for a legitimate session)
	require.Eventually(t, func() bool {
		session := env.GetSession(t, sessionID)
		// Session should still be running (not affected by reconciliation)
		return session.Status == "running"
	}, 10*time.Second, 100*time.Millisecond, "Legitimate session should remain running")

	// Check that no new orphans were detected (our session is legitimate)
	orphansFound, _, _, _ := env.GetReconcileMetrics(t)
	assert.Equal(t, initialOrphans, orphansFound, "Legitimate session should not be detected as orphan")

	// Session should still be running
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Session should still be running")

	// Cleanup
	env.DeleteSession(t, sessionID)
	t.Log("Legitimate session test completed successfully")
}
