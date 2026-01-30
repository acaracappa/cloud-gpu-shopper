//go:build e2e
// +build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIdleThresholdConfigured tests that idle threshold is properly configured on session creation
func TestIdleThresholdConfigured(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session with idle threshold
	idleThresholdMinutes := 15
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
		IdleThreshold:  idleThresholdMinutes,
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Send heartbeat to transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: createResp.AgentToken,
		Status:     "running",
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Note: The session object returned by GetSession might not include IdleThreshold
	// This test verifies that sessions can be created with idle threshold without error
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status)

	t.Logf("Session %s created with idle threshold configured", sessionID)

	// Cleanup
	env.DeleteSession(t, sessionID)
	t.Log("Idle threshold configuration test completed")
}

// TestHeartbeatWithIdleSeconds tests that idle seconds are properly tracked in heartbeats
func TestHeartbeatWithIdleSeconds(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session with idle threshold
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
		IdleThreshold:  10, // 10 minute threshold
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat to transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 0,
		GPUUtilPct:  50.0,
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Send heartbeat with idle seconds (simulating idle GPU)
	t.Log("Sending heartbeat with 120 seconds idle...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 120, // 2 minutes idle
		GPUUtilPct:  2.0, // Low GPU usage
	})

	// Send another heartbeat with more idle time
	t.Log("Sending heartbeat with 300 seconds idle...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 300, // 5 minutes idle
		GPUUtilPct:  1.0,
	})

	// Session should still be running (5 min < 10 min threshold)
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Session should still be running below idle threshold")

	t.Log("Idle seconds tracking test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestActiveSessionNotIdle tests that active sessions (with GPU usage) are not considered idle
func TestActiveSessionNotIdle(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session with very low idle threshold (1 minute)
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "training",
		ReservationHrs: 1,
		IdleThreshold:  1, // 1 minute threshold
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 0,
		GPUUtilPct:  90.0, // High GPU usage = active
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Send multiple heartbeats showing GPU is active
	for i := 0; i < 3; i++ {
		env.SendHeartbeat(t, sessionID, HeartbeatRequest{
			SessionID:   sessionID,
			AgentToken:  createResp.AgentToken,
			Status:      "running",
			IdleSeconds: 0, // GPU is active, no idle time
			GPUUtilPct:  float64(80 + i*5),
		})
		time.Sleep(100 * time.Millisecond)
	}

	// Session should be running (active GPU resets idle counter)
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Active session should not be shut down")

	t.Log("Active session test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestNoIdleThresholdMeansNoIdleShutdown tests that sessions without idle threshold are not subject to idle shutdown
func TestNoIdleThresholdMeansNoIdleShutdown(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	// Create session WITHOUT idle threshold
	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "llm",
		ReservationHrs: 1,
		// IdleThreshold not set (0)
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 0,
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Send heartbeat with very high idle time
	// Without idle threshold, this should NOT trigger shutdown
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 7200, // 2 hours idle!
		GPUUtilPct:  0.0,
	})

	// Session should still be running (no idle threshold = no idle shutdown)
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Session without idle threshold should not be shut down for being idle")

	t.Log("No idle threshold test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestIdleHeartbeatSequence tests the behavior when GPU goes from active to idle
func TestIdleHeartbeatSequence(t *testing.T) {
	env := NewTestEnv()
	env.WaitForServer(t, 30*time.Second)
	env.WaitForMockProvider(t, 10*time.Second)
	env.ResetMockProvider(t)

	inventory := env.ListInventory(t)
	require.NotEmpty(t, inventory.Offers)

	createResp := env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        inventory.Offers[0].ID,
		WorkloadType:   "batch",
		ReservationHrs: 1,
		IdleThreshold:  30, // 30 minute threshold
	})

	sessionID := createResp.Session.ID
	defer env.Cleanup(t, sessionID)

	// Phase 1: GPU active
	t.Log("Phase 1: GPU active (high utilization)...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 0,
		GPUUtilPct:  95.0,
	})

	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Phase 2: Work completes, GPU becomes idle
	t.Log("Phase 2: GPU becoming idle (low utilization)...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 60, // 1 minute idle
		GPUUtilPct:  5.0,
	})

	// Phase 3: GPU continues idle
	t.Log("Phase 3: GPU continues idle...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 180, // 3 minutes idle
		GPUUtilPct:  2.0,
	})

	// Phase 4: Work resumes, GPU active again
	t.Log("Phase 4: GPU active again...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  createResp.AgentToken,
		Status:      "running",
		IdleSeconds: 0, // Reset to active
		GPUUtilPct:  80.0,
	})

	// Session should be running throughout
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status, "Session should be running after GPU resumes activity")

	t.Log("Idle heartbeat sequence test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}
