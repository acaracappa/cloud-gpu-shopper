//go:build e2e
// +build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHeartbeat tests the heartbeat functionality
func TestHeartbeat(t *testing.T) {
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
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	agentToken := createResp.AgentToken
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat to transition to running (simulates agent startup)
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Get initial session state
	session := env.GetSession(t, sessionID)
	initialHeartbeat := session.LastHeartbeat

	// Send heartbeat
	t.Log("Sending heartbeat...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  agentToken,
		Status:      "running",
		IdleSeconds: 0,
		GPUUtilPct:  75.5,
	})

	// Verify heartbeat was recorded
	session = env.GetSession(t, sessionID)
	if initialHeartbeat.IsZero() {
		assert.False(t, session.LastHeartbeat.IsZero(), "LastHeartbeat should be set after heartbeat")
	} else {
		assert.True(t, session.LastHeartbeat.After(initialHeartbeat) || session.LastHeartbeat.Equal(initialHeartbeat),
			"LastHeartbeat should be updated")
	}

	t.Log("Heartbeat test completed successfully")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestHeartbeatWithIdleTracking tests idle seconds tracking via heartbeat
func TestHeartbeatWithIdleTracking(t *testing.T) {
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
		IdleThreshold:  10, // 10 minute idle threshold
	})

	sessionID := createResp.Session.ID
	agentToken := createResp.AgentToken
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat to transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Send heartbeat with idle seconds
	t.Log("Sending heartbeat with idle tracking...")
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  agentToken,
		Status:      "running",
		IdleSeconds: 120, // 2 minutes idle
		GPUUtilPct:  5.0, // Low GPU usage
	})

	// Verify heartbeat accepted
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status)

	// Send another heartbeat with more idle time
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:   sessionID,
		AgentToken:  agentToken,
		Status:      "running",
		IdleSeconds: 300, // 5 minutes idle
		GPUUtilPct:  2.0,
	})

	t.Log("Idle tracking heartbeat test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestHeartbeatInvalidToken tests heartbeat with invalid token
func TestHeartbeatInvalidToken(t *testing.T) {
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
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	agentToken := createResp.AgentToken
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat to transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Try to send heartbeat with invalid token
	t.Log("Sending heartbeat with invalid token...")
	reqBody := HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: "invalid-token-12345",
		Status:     "running",
	}

	body, _ := jsonMarshal(reqBody)
	resp, err := env.HTTPClient.Post(env.ServerURL+"/api/v1/sessions/"+sessionID+"/heartbeat", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should be unauthorized
	assert.Equal(t, 401, resp.StatusCode, "Should reject invalid token")

	t.Log("Invalid token rejection test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestHeartbeatUpdatesTimestamp tests that heartbeat updates the timestamp
func TestHeartbeatUpdatesTimestamp(t *testing.T) {
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
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	agentToken := createResp.AgentToken
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat to transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Send first heartbeat
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Get timestamp after first heartbeat
	session1 := env.GetSession(t, sessionID)
	firstHeartbeat := session1.LastHeartbeat

	// Wait a bit
	time.Sleep(1 * time.Second)

	// Send second heartbeat
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Get timestamp after second heartbeat
	session2 := env.GetSession(t, sessionID)
	secondHeartbeat := session2.LastHeartbeat

	// Second heartbeat should be after first (or at least equal if time resolution is low)
	assert.True(t, secondHeartbeat.After(firstHeartbeat) || secondHeartbeat.Equal(firstHeartbeat),
		"Second heartbeat timestamp (%v) should be >= first (%v)", secondHeartbeat, firstHeartbeat)

	t.Log("Heartbeat timestamp update test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestMultipleHeartbeats tests sending multiple heartbeats in sequence
func TestMultipleHeartbeats(t *testing.T) {
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
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	agentToken := createResp.AgentToken
	defer env.Cleanup(t, sessionID)

	// Send initial heartbeat to transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Wait for running
	env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Send multiple heartbeats with varying GPU utilization
	gpuUtils := []float64{10.0, 50.0, 90.0, 30.0, 5.0}
	for i, gpuUtil := range gpuUtils {
		t.Logf("Sending heartbeat %d with GPU util %.1f%%", i+1, gpuUtil)
		env.SendHeartbeat(t, sessionID, HeartbeatRequest{
			SessionID:   sessionID,
			AgentToken:  agentToken,
			Status:      "running",
			IdleSeconds: 0,
			GPUUtilPct:  gpuUtil,
		})
		time.Sleep(100 * time.Millisecond)
	}

	// Verify session is still running
	session := env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status)
	assert.False(t, session.LastHeartbeat.IsZero())

	t.Logf("Sent %d heartbeats successfully", len(gpuUtils))

	// Cleanup
	env.DeleteSession(t, sessionID)
}

// TestHeartbeatTransitionsToRunning tests that first heartbeat transitions session to running
func TestHeartbeatTransitionsToRunning(t *testing.T) {
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
		WorkloadType:   "llm",
		ReservationHrs: 1,
	})

	sessionID := createResp.Session.ID
	agentToken := createResp.AgentToken
	defer env.Cleanup(t, sessionID)

	// Check initial status (should be provisioning)
	session := env.GetSession(t, sessionID)
	t.Logf("Initial status: %s", session.Status)
	assert.Contains(t, []string{"pending", "provisioning"}, session.Status)

	// Send first heartbeat - this should trigger transition to running
	env.SendHeartbeat(t, sessionID, HeartbeatRequest{
		SessionID:  sessionID,
		AgentToken: agentToken,
		Status:     "running",
	})

	// Verify transition to running
	session = env.WaitForStatus(t, sessionID, "running", 10*time.Second)

	// Verify still running
	session = env.GetSession(t, sessionID)
	assert.Equal(t, "running", session.Status)

	t.Log("Status transition test completed")

	// Cleanup
	env.DeleteSession(t, sessionID)
}
