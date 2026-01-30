//go:build live
// +build live

package live

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ==============================================================================
// AGENT LIFECYCLE TESTS
// ==============================================================================

// TestAgent_Startup_Connection_VastAI tests agent startup and connection on Vast.ai
func TestAgent_Startup_Connection_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentStartupConnection(t, ProviderVastAI)
}

// TestAgent_Startup_Connection_TensorDock tests agent startup and connection on TensorDock
func TestAgent_Startup_Connection_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentStartupConnection(t, ProviderTensorDock)
}

func testAgentStartupConnection(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Setup diagnostics
	diag := testEnv.GetDiagnosticsCollector(resp.Session.ID)
	if diag != nil {
		diag.CollectProvisionData(&CreateSessionRequest{
			ConsumerID:     GenerateConsumerID(),
			OfferID:        resp.Session.ID,
			WorkloadType:   "agent-test",
			ReservationHrs: 1,
		}, resp)
	}

	// Wait for running status (agent heartbeat received)
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	t.Logf("Session reached running status")
	t.Logf("  SSH: %s@%s:%d", session.SSHUser, session.SSHHost, session.SSHPort)
	t.Logf("  Last Heartbeat: %v", session.LastHeartbeat)

	// Verify heartbeat was received
	assert.False(t, session.LastHeartbeat.IsZero(), "Should have received heartbeat")

	// If we have SSH key, connect and verify agent
	if resp.SSHPrivateKey != "" {
		ssh := testEnv.ConnectSSH(t, session, resp.SSHPrivateKey)
		defer ssh.Close()

		if diag != nil {
			diag.SetSSHHelper(ssh)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Verify agent process is running
		running, err := ssh.ProcessRunning(ctx, "gpu-agent")
		if err != nil {
			t.Logf("Warning: Could not check agent process: %v", err)
		} else {
			assert.True(t, running, "Agent process should be running")
		}

		// Check agent health endpoint
		health, err := ssh.CurlEndpoint(ctx, "http://localhost:8081/health")
		if err != nil {
			t.Logf("Warning: Could not reach agent health endpoint: %v", err)
		} else {
			t.Logf("Agent health response: %s", health)
			assert.Contains(t, health, "session_id", "Health should contain session_id")
		}

		// Check environment variables
		env, err := ssh.GetEnvironment(ctx)
		if err != nil {
			t.Logf("Warning: Could not get environment: %v", err)
		} else {
			t.Logf("Agent environment:\n%s", env)
		}

		// Collect diagnostic snapshot
		testEnv.CollectDiagnostics(t, resp.Session.ID, "startup_complete")
	} else {
		t.Log("SSH private key not available, skipping SSH verification")
	}

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

// TestAgent_Heartbeat_Continuous_VastAI tests continuous heartbeat on Vast.ai
func TestAgent_Heartbeat_Continuous_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentHeartbeatContinuous(t, ProviderVastAI)
}

// TestAgent_Heartbeat_Continuous_TensorDock tests continuous heartbeat on TensorDock
func TestAgent_Heartbeat_Continuous_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentHeartbeatContinuous(t, ProviderTensorDock)
}

func testAgentHeartbeatContinuous(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// Record initial heartbeat
	session := testEnv.GetSession(t, resp.Session.ID)
	initialHeartbeat := session.LastHeartbeat
	t.Logf("Initial heartbeat: %v", initialHeartbeat)

	// Wait for 3 minutes (expect ~6 heartbeats at 30s interval)
	heartbeatRecords := make([]HeartbeatRecord, 0)
	heartbeatRecords = append(heartbeatRecords, HeartbeatRecord{
		Timestamp: initialHeartbeat,
	})

	t.Log("Monitoring heartbeats for 3 minutes...")
	for i := 0; i < 6; i++ {
		time.Sleep(30 * time.Second)

		session = testEnv.GetSession(t, resp.Session.ID)
		t.Logf("  Heartbeat check %d: %v", i+1, session.LastHeartbeat)

		heartbeatRecords = append(heartbeatRecords, HeartbeatRecord{
			Timestamp: session.LastHeartbeat,
		})
	}

	// Verify heartbeats are being received
	assert.True(t, session.LastHeartbeat.After(initialHeartbeat),
		"Heartbeat should have been updated")

	// Check heartbeat freshness
	assert.WithinDuration(t, time.Now(), session.LastHeartbeat, 45*time.Second,
		"Heartbeat should be recent")

	// Save heartbeat data to diagnostics
	diag := testEnv.GetDiagnosticsCollector(resp.Session.ID)
	if diag != nil {
		diag.CollectHeartbeatData(heartbeatRecords)
	}

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}

// TestAgent_GPU_Monitoring_VastAI tests GPU metrics collection on Vast.ai
func TestAgent_GPU_Monitoring_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentGPUMonitoring(t, ProviderVastAI)
}

// TestAgent_GPU_Monitoring_TensorDock tests GPU metrics collection on TensorDock
func TestAgent_GPU_Monitoring_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentGPUMonitoring(t, ProviderTensorDock)
}

func testAgentGPUMonitoring(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// If we have SSH key, check GPU monitoring
	if resp.SSHPrivateKey != "" {
		ssh := testEnv.ConnectSSH(t, session, resp.SSHPrivateKey)
		defer ssh.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Check nvidia-smi availability
		nvsmi, err := ssh.GetNvidiaSMI(ctx)
		if err != nil {
			t.Logf("nvidia-smi not available: %v", err)
		} else {
			t.Logf("nvidia-smi output:\n%s", nvsmi)
		}

		// Query agent status endpoint for GPU metrics
		status, err := ssh.CurlEndpoint(ctx, "http://localhost:8081/status")
		if err != nil {
			t.Logf("Warning: Could not get agent status: %v", err)
		} else {
			t.Logf("Agent status: %s", status)
			// Verify GPU metrics are present (even if zeros)
			assert.Contains(t, status, "gpu_utilization", "Status should contain GPU utilization")
			assert.Contains(t, status, "memory_used", "Status should contain memory used")
		}

		// Collect diagnostic snapshot
		testEnv.CollectDiagnostics(t, resp.Session.ID, "gpu_monitoring")
	} else {
		t.Log("SSH private key not available, skipping GPU monitoring verification")
	}

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}

// ==============================================================================
// DECOMMISSION TESTS
// ==============================================================================

// TestAgent_GracefulShutdown_SignalDone_VastAI tests graceful shutdown on Vast.ai
func TestAgent_GracefulShutdown_SignalDone_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentGracefulShutdown(t, ProviderVastAI)
}

// TestAgent_GracefulShutdown_SignalDone_TensorDock tests graceful shutdown on TensorDock
func TestAgent_GracefulShutdown_SignalDone_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentGracefulShutdown(t, ProviderTensorDock)
}

func testAgentGracefulShutdown(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	// No defer cleanup - we're testing destruction

	// Setup diagnostics
	diag := testEnv.GetDiagnosticsCollector(resp.Session.ID)

	// Wait for running
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// Collect pre-shutdown diagnostics
	if resp.SSHPrivateKey != "" && diag != nil {
		ssh := testEnv.ConnectSSH(t, session, resp.SSHPrivateKey)
		diag.SetSSHHelper(ssh)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		diag.CollectSnapshot(ctx, "pre_shutdown")
		cancel()
		ssh.Close()
	}

	// Signal done
	t.Log("Signaling session done...")
	startShutdown := time.Now()
	testEnv.SignalDone(t, resp.Session.ID)

	// Wait for stopped status
	session = testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 3*time.Minute)
	shutdownDuration := time.Since(startShutdown)

	t.Logf("Session stopped in %v", shutdownDuration)

	// Verify final status
	assert.Equal(t, "stopped", session.Status)

	// Generate summary
	if diag != nil {
		diag.GenerateSummary("graceful_shutdown", true, nil)
	}
}

// TestAgent_ForceDestruction_VastAI tests force destruction on Vast.ai
func TestAgent_ForceDestruction_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentForceDestruction(t, ProviderVastAI)
}

// TestAgent_ForceDestruction_TensorDock tests force destruction on TensorDock
func TestAgent_ForceDestruction_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentForceDestruction(t, ProviderTensorDock)
}

func testAgentForceDestruction(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	// No defer cleanup - we're testing destruction

	// Wait for running
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// Force destroy
	t.Log("Force destroying session...")
	startDestroy := time.Now()
	testEnv.DestroySession(t, resp.Session.ID)

	// Wait for stopped status
	session := testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 3*time.Minute)
	destroyDuration := time.Since(startDestroy)

	t.Logf("Session force destroyed in %v", destroyDuration)

	// Verify final status
	assert.Equal(t, "stopped", session.Status)
}

// ==============================================================================
// CROSS-PROVIDER AGENT TESTS
// ==============================================================================

// TestAgent_CrossProvider_Comparison tests agent behavior across providers
func TestAgent_CrossProvider_Comparison(t *testing.T) {
	// Require both providers
	if !testConfig.HasProvider(ProviderVastAI) || !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("Both Vast.ai and TensorDock must be configured")
	}

	// Provision from both providers simultaneously
	vastResp := testEnv.ProvisionReliableGPU(t, ProviderVastAI)
	defer testEnv.Cleanup(t, vastResp.Session.ID)

	tensorResp := testEnv.ProvisionReliableGPU(t, ProviderTensorDock)
	defer testEnv.Cleanup(t, tensorResp.Session.ID)

	// Wait for both to be running
	vastStart := time.Now()
	vastSession := testEnv.WaitForStatus(t, vastResp.Session.ID, "running", 5*time.Minute)
	vastStartupTime := time.Since(vastStart)

	tensorStart := time.Now()
	tensorSession := testEnv.WaitForStatus(t, tensorResp.Session.ID, "running", 5*time.Minute)
	tensorStartupTime := time.Since(tensorStart)

	t.Logf("Startup times:")
	t.Logf("  Vast.ai: %v", vastStartupTime)
	t.Logf("  TensorDock: %v", tensorStartupTime)

	// Wait for heartbeats
	time.Sleep(90 * time.Second)

	// Compare heartbeat behavior
	vastSession = testEnv.GetSession(t, vastResp.Session.ID)
	tensorSession = testEnv.GetSession(t, tensorResp.Session.ID)

	t.Logf("Heartbeat status:")
	t.Logf("  Vast.ai: %v", vastSession.LastHeartbeat)
	t.Logf("  TensorDock: %v", tensorSession.LastHeartbeat)

	// Both should have recent heartbeats
	assert.WithinDuration(t, time.Now(), vastSession.LastHeartbeat, 60*time.Second,
		"Vast.ai heartbeat should be recent")
	assert.WithinDuration(t, time.Now(), tensorSession.LastHeartbeat, 60*time.Second,
		"TensorDock heartbeat should be recent")

	// Test shutdown on both
	vastShutdownStart := time.Now()
	testEnv.SignalDone(t, vastResp.Session.ID)
	testEnv.WaitForStatus(t, vastResp.Session.ID, "stopped", 2*time.Minute)
	vastShutdownTime := time.Since(vastShutdownStart)

	tensorShutdownStart := time.Now()
	testEnv.SignalDone(t, tensorResp.Session.ID)
	testEnv.WaitForStatus(t, tensorResp.Session.ID, "stopped", 2*time.Minute)
	tensorShutdownTime := time.Since(tensorShutdownStart)

	t.Logf("Shutdown times:")
	t.Logf("  Vast.ai: %v", vastShutdownTime)
	t.Logf("  TensorDock: %v", tensorShutdownTime)
}

// ==============================================================================
// PROVISIONING TESTS
// ==============================================================================

// TestAgent_ProvisioningFailure tests handling of provisioning failures
func TestAgent_ProvisioningFailure(t *testing.T) {
	// Try to provision with an invalid offer ID
	_, err := testEnv.CreateSessionWithError(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        "invalid-offer-id-12345",
		WorkloadType:   "test",
		ReservationHrs: 1,
	})

	// Should fail with an error
	assert.Error(t, err, "Should fail with invalid offer ID")
	t.Logf("Expected error: %v", err)
}

// ==============================================================================
// ERROR AND RECOVERY TESTS
// ==============================================================================

// TestAgent_Heartbeat_Recovery_VastAI tests heartbeat recovery after network issues on Vast.ai
func TestAgent_Heartbeat_Recovery_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentHeartbeatRecovery(t, ProviderVastAI)
}

// TestAgent_Heartbeat_Recovery_TensorDock tests heartbeat recovery after network issues on TensorDock
func TestAgent_Heartbeat_Recovery_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentHeartbeatRecovery(t, ProviderTensorDock)
}

func testAgentHeartbeatRecovery(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// Record initial heartbeat
	initialHeartbeat := session.LastHeartbeat
	t.Logf("Initial heartbeat: %v", initialHeartbeat)

	// Wait a bit to confirm heartbeats are working
	time.Sleep(45 * time.Second)

	session = testEnv.GetSession(t, resp.Session.ID)
	assert.True(t, session.LastHeartbeat.After(initialHeartbeat),
		"Heartbeat should have updated")

	// Note: We can't easily test network blocking without SSH and iptables access
	// This test verifies that heartbeats continue working over time
	t.Log("Monitoring heartbeat continuity for 2 minutes...")

	missedHeartbeats := 0
	lastHeartbeat := session.LastHeartbeat

	for i := 0; i < 4; i++ {
		time.Sleep(30 * time.Second)

		session = testEnv.GetSession(t, resp.Session.ID)

		if session.LastHeartbeat.Equal(lastHeartbeat) {
			missedHeartbeats++
			t.Logf("  Check %d: No new heartbeat (missed: %d)", i+1, missedHeartbeats)
		} else {
			t.Logf("  Check %d: Heartbeat received at %v", i+1, session.LastHeartbeat)
			lastHeartbeat = session.LastHeartbeat
		}
	}

	// Should have at most 1 missed heartbeat (timing variance)
	assert.LessOrEqual(t, missedHeartbeats, 1,
		"Should not miss more than 1 heartbeat in normal operation")

	// Final heartbeat should be recent
	assert.WithinDuration(t, time.Now(), session.LastHeartbeat, 60*time.Second,
		"Final heartbeat should be recent")

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}

// TestAgent_Failsafe_ShopperUnreachable_VastAI tests failsafe behavior on Vast.ai
// Note: This test requires SSH access and iptables to block the shopper
func TestAgent_Failsafe_ShopperUnreachable_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	t.Skip("This test requires SSH access with iptables permissions - enable manually for full testing")
	testAgentFailsafeShopperUnreachable(t, ProviderVastAI)
}

// TestAgent_Failsafe_ShopperUnreachable_TensorDock tests failsafe behavior on TensorDock
func TestAgent_Failsafe_ShopperUnreachable_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	t.Skip("This test requires SSH access with iptables permissions - enable manually for full testing")
	testAgentFailsafeShopperUnreachable(t, ProviderTensorDock)
}

func testAgentFailsafeShopperUnreachable(t *testing.T, provider Provider) {
	// This test would provision with SHOPPER_FAILSAFE_THRESHOLD=5
	// to trigger failsafe after ~2.5 minutes instead of 30 minutes

	// Provision instance (would need custom env vars for low threshold)
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	if resp.SSHPrivateKey == "" {
		t.Skip("SSH key not available for failsafe test")
	}

	// Connect SSH
	ssh := testEnv.ConnectSSH(t, session, resp.SSHPrivateKey)
	defer ssh.Close()

	ctx := context.Background()

	// Block shopper connection
	// Note: This requires root/sudo access which may not be available
	t.Log("Blocking shopper connection...")
	if err := ssh.BlockHost(ctx, testEnv.Config.ServerURL); err != nil {
		t.Skipf("Cannot block host (need sudo): %v", err)
	}

	// Wait for failsafe to trigger (depends on threshold)
	// With default threshold of 60 at 30s interval, this would take 30 minutes
	// With threshold of 5, it would take ~2.5 minutes
	t.Log("Waiting for failsafe to trigger...")

	// Monitor agent status
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		// Check if instance is still reachable
		_, err := ssh.CurlEndpoint(ctx, "http://localhost:8081/health")
		if err != nil {
			t.Log("Agent no longer responding - failsafe may have triggered")
			break
		}
		time.Sleep(30 * time.Second)
	}

	// Cleanup - unblock host (may fail if instance is already terminated)
	ssh.UnblockHost(ctx, testEnv.Config.ServerURL)
}

// TestAgent_StartupFailure_NoHeartbeat tests handling when agent doesn't send heartbeats
func TestAgent_StartupFailure_NoHeartbeat(t *testing.T) {
	t.Skip("This test requires ability to sabotage agent startup - skipping for normal runs")

	// This test would:
	// 1. Provision instance with invalid SHOPPER_AGENT_URL
	// 2. Wait for heartbeat timeout (5 minutes default)
	// 3. Verify session marked as failed
	// 4. Verify instance auto-destroyed

	// Implementation would require modifying provisioner to allow custom env vars
}

// TestAgent_IdleDetection_VastAI tests idle detection on Vast.ai
func TestAgent_IdleDetection_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	testAgentIdleDetection(t, ProviderVastAI)
}

// TestAgent_IdleDetection_TensorDock tests idle detection on TensorDock
func TestAgent_IdleDetection_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	testAgentIdleDetection(t, ProviderTensorDock)
}

func testAgentIdleDetection(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// If we have SSH, check idle detection via agent status
	if resp.SSHPrivateKey != "" {
		ssh := testEnv.ConnectSSH(t, session, resp.SSHPrivateKey)
		defer ssh.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Get initial status
		status, err := ssh.CurlEndpoint(ctx, "http://localhost:8081/status")
		if err != nil {
			t.Logf("Warning: Could not get agent status: %v", err)
		} else {
			t.Logf("Agent status: %s", status)
		}

		// Wait a bit for idle time to accumulate (GPU should be idle)
		t.Log("Waiting 60 seconds for idle time to accumulate...")
		time.Sleep(60 * time.Second)

		// Get status again
		status, err = ssh.CurlEndpoint(ctx, "http://localhost:8081/status")
		if err != nil {
			t.Logf("Warning: Could not get agent status: %v", err)
		} else {
			t.Logf("Agent status after wait: %s", status)
			// Idle seconds should have increased (if GPU is truly idle)
			assert.Contains(t, status, "idle_seconds", "Status should contain idle_seconds")
		}
	}

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}

// TestAgent_SelfDestruct_Timer tests self-destruct timer behavior
func TestAgent_SelfDestruct_Timer(t *testing.T) {
	t.Skip("This test requires waiting for self-destruct timer (30+ minutes) - skipping for normal runs")

	// This test would:
	// 1. Provision instance with SHOPPER_EXPIRES_AT set to 2 min from now
	// 2. Provision with SHOPPER_SELF_DESTRUCT_GRACE=30s
	// 3. Monitor agent logs for countdown
	// 4. Verify instance terminates at expected time
}

// ==============================================================================
// EXTENDED DURATION TESTS
// ==============================================================================

// TestAgent_LongRunning_Stability_VastAI tests agent stability over extended period on Vast.ai
func TestAgent_LongRunning_Stability_VastAI(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	t.Skip("Long running test - enable manually when needed")
	testAgentLongRunningStability(t, ProviderVastAI)
}

func testAgentLongRunningStability(t *testing.T, provider Provider) {
	// Provision instance
	resp := testEnv.ProvisionReliableGPU(t, provider)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// Monitor for 15 minutes
	t.Log("Running stability test for 15 minutes...")

	heartbeatsMissed := 0
	checks := 30 // Check every 30 seconds for 15 minutes

	for i := 0; i < checks; i++ {
		session := testEnv.GetSession(t, resp.Session.ID)

		// Check heartbeat freshness
		if time.Since(session.LastHeartbeat) > 60*time.Second {
			heartbeatsMissed++
			t.Logf("  Check %d: Heartbeat stale (last: %v)", i+1, session.LastHeartbeat)
		} else {
			t.Logf("  Check %d: OK (last heartbeat: %v)", i+1, session.LastHeartbeat)
		}

		time.Sleep(30 * time.Second)
	}

	// Should have very few missed heartbeats
	assert.LessOrEqual(t, heartbeatsMissed, 2,
		"Should not miss more than 2 heartbeats over 15 minutes")

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}
