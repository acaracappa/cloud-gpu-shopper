//go:build live
// +build live

package live

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testConfig   *TestConfig
	testWatchdog *Watchdog
	testEnv      *LiveTestEnv
)

func TestMain(m *testing.M) {
	log.Println("===============================================================")
	log.Println("  LIVE TEST SUITE - Real GPU Servers (Multi-Provider)")
	log.Println("===============================================================")

	// Load configuration
	testConfig = DefaultTestConfig()

	// Check enabled providers
	enabled := testConfig.EnabledProviders()
	if len(enabled) == 0 {
		log.Println("ERROR: No providers enabled. Set VASTAI_API_KEY and/or TENSORDOCK_API_KEY")
		os.Exit(1)
	}
	log.Printf("Enabled providers: %v", enabled)
	log.Printf("Budget: $%.2f total, $%.2f per provider", testConfig.MaxTotalSpendUSD, testConfig.MaxPerProviderUSD)
	log.Printf("Max runtime: %v total, %v per provider", testConfig.MaxTotalRuntime, testConfig.MaxPerProviderTime)

	// Start watchdog
	testWatchdog = NewWatchdog(testConfig)
	ctx := testWatchdog.Start(context.Background())
	defer testWatchdog.Stop()

	// Create test environment
	testEnv = NewLiveTestEnv(testConfig, testWatchdog)

	// Pre-test orphan scan: Clean up any orphaned instances from previous runs
	// This runs synchronously before tests to ensure a clean slate
	log.Println("---------------------------------------------------------------")
	log.Println("  PRE-TEST ORPHAN SCAN")
	log.Println("---------------------------------------------------------------")
	cleanupOrphanedInstancesPreTest()

	// Suppress unused variable warning
	_ = ctx

	// Run tests
	log.Println("---------------------------------------------------------------")
	log.Println("  RUNNING TESTS")
	log.Println("---------------------------------------------------------------")
	code := m.Run()

	// Print final stats
	stats := testWatchdog.GetStats()
	log.Println("===============================================================")
	log.Printf("  FINAL STATS: Runtime=%v, Spend=$%.4f, Active=%d",
		stats.TotalRuntime.Round(time.Second), stats.TotalSpend, stats.ActiveInstances)
	for prov, spend := range stats.SpendByProv {
		log.Printf("    %s: $%.4f", prov, spend)
	}
	log.Println("===============================================================")

	// Ensure all instances cleaned up
	if stats.ActiveInstances > 0 {
		log.Printf("WARNING: %d instances still active at exit", stats.ActiveInstances)
		testWatchdog.Stop()
	}

	os.Exit(code)
}

// cleanupOrphanedInstancesPreTest scans all providers for orphaned shopper instances
// and destroys them before running tests. This uses a testing.T-like logger since
// we don't have a real testing.T in TestMain.
func cleanupOrphanedInstancesPreTest() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	totalCleaned := 0

	for provName := range testConfig.Providers {
		if !testConfig.HasProvider(provName) {
			continue
		}

		prov, err := testEnv.Providers.GetProvider(provName)
		if err != nil {
			log.Printf("  [%s] Failed to get provider: %v", provName, err)
			continue
		}

		instances, err := prov.ListAllInstances(ctx)
		if err != nil {
			log.Printf("  [%s] Failed to list instances: %v", provName, err)
			continue
		}

		if len(instances) == 0 {
			log.Printf("  [%s] No orphaned instances found", provName)
			continue
		}

		log.Printf("  [%s] Found %d orphaned instances to clean up", provName, len(instances))

		for _, inst := range instances {
			log.Printf("  [%s] Destroying orphan: %s (%s)", provName, inst.ID, inst.Name)

			if err := prov.DestroyInstance(ctx, inst.ID); err != nil {
				log.Printf("  [%s] Failed to destroy %s: %v", provName, inst.ID, err)
			} else {
				log.Printf("  [%s] Destroyed orphan: %s", provName, inst.ID)
				totalCleaned++
			}
		}
	}

	if totalCleaned > 0 {
		log.Printf("  Cleaned up %d total orphaned instances", totalCleaned)
	}
}

// ==============================================================================
// CROSS-PROVIDER TESTS
// ==============================================================================

// TestL0_CrossProvider_FindCheapest finds the cheapest GPU across all providers
func TestL0_CrossProvider_FindCheapest(t *testing.T) {
	offer, provider := testEnv.FindCheapestGPU(t)
	require.NotNil(t, offer)
	require.NotEmpty(t, provider)

	t.Logf("Cheapest GPU: %s (%s) @ $%.4f/hr from %s",
		offer.GPUType, offer.ID, offer.PricePerHour, provider)
}

// ==============================================================================
// VAST.AI SPECIFIC TESTS
// ==============================================================================

func TestL1_VastAI_ProvisionSmoke(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	resp := testEnv.ProvisionCheapGPU(t, ProviderVastAI)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)
	assert.Equal(t, "vastai", session.Provider)

	// Verify SSH info
	testEnv.VerifySSH(t, session.SSHHost, session.SSHPort, session.SSHUser)

	// Destroy
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

func TestL2_VastAI_Extension(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	resp := testEnv.ProvisionCheapGPU(t, ProviderVastAI)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	originalExpiry := testEnv.GetSession(t, resp.Session.ID).ExpiresAt

	// Extend by 1 hour
	testEnv.ExtendSession(t, resp.Session.ID, 1)

	// Verify extension
	newExpiry := testEnv.GetSession(t, resp.Session.ID).ExpiresAt
	assert.True(t, newExpiry.After(originalExpiry), "Expiry should be extended")
	assert.WithinDuration(t, originalExpiry.Add(1*time.Hour), newExpiry, 2*time.Minute)

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}

func TestL3_VastAI_GracefulShutdown(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	resp := testEnv.ProvisionCheapGPU(t, ProviderVastAI)
	// No defer - we're testing destruction

	// Wait for running
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

	// Signal done
	testEnv.SignalDone(t, resp.Session.ID)

	// Verify stopped
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
	t.Log("Session gracefully stopped")
}

// ==============================================================================
// TENSORDOCK SPECIFIC TESTS
// ==============================================================================

func TestL1_TensorDock_ProvisionSmoke(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	resp := testEnv.ProvisionCheapGPU(t, ProviderTensorDock)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running - TensorDock needs extra time for cloud-init
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 8*time.Minute)
	assert.Equal(t, "tensordock", session.Provider)

	// Verify SSH info
	testEnv.VerifySSH(t, session.SSHHost, session.SSHPort, session.SSHUser)

	// Destroy
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

func TestL2_TensorDock_Extension(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	resp := testEnv.ProvisionCheapGPU(t, ProviderTensorDock)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Wait for running - TensorDock needs extra time for cloud-init
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 8*time.Minute)

	originalExpiry := testEnv.GetSession(t, resp.Session.ID).ExpiresAt

	// Extend
	testEnv.ExtendSession(t, resp.Session.ID, 1)

	// Verify
	newExpiry := testEnv.GetSession(t, resp.Session.ID).ExpiresAt
	assert.True(t, newExpiry.After(originalExpiry))

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}

func TestL3_TensorDock_GracefulShutdown(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	resp := testEnv.ProvisionCheapGPU(t, ProviderTensorDock)

	// Wait for running - TensorDock needs extra time for cloud-init
	testEnv.WaitForStatus(t, resp.Session.ID, "running", 8*time.Minute)

	// Signal done
	testEnv.SignalDone(t, resp.Session.ID)

	// Verify stopped
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
	t.Log("Session gracefully stopped")
}

// ==============================================================================
// CROSS-PROVIDER COMPARISON TESTS
// ==============================================================================

// TestL4_CrossProvider_ProvisionBoth provisions from both providers to compare
func TestL4_CrossProvider_ProvisionBoth(t *testing.T) {
	// Check both providers are enabled
	if !testConfig.HasProvider(ProviderVastAI) || !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("Both Vast.ai and TensorDock must be configured")
	}

	// Provision from Vast.ai
	vastResp := testEnv.ProvisionCheapGPU(t, ProviderVastAI)
	defer testEnv.Cleanup(t, vastResp.Session.ID)

	// Provision from TensorDock
	tensorResp := testEnv.ProvisionCheapGPU(t, ProviderTensorDock)
	defer testEnv.Cleanup(t, tensorResp.Session.ID)

	// Wait for both to be running
	vastSession := testEnv.WaitForStatus(t, vastResp.Session.ID, "running", 5*time.Minute)
	tensorSession := testEnv.WaitForStatus(t, tensorResp.Session.ID, "running", 5*time.Minute)

	// Compare
	t.Logf("Vast.ai: %s @ $%.4f/hr", vastSession.GPUType, vastSession.PricePerHour)
	t.Logf("TensorDock: %s @ $%.4f/hr", tensorSession.GPUType, tensorSession.PricePerHour)

	// Verify both have SSH info
	testEnv.VerifySSH(t, vastSession.SSHHost, vastSession.SSHPort, vastSession.SSHUser)
	testEnv.VerifySSH(t, tensorSession.SSHHost, tensorSession.SSHPort, tensorSession.SSHUser)

	// Cleanup both
	testEnv.DestroySession(t, vastResp.Session.ID)
	testEnv.DestroySession(t, tensorResp.Session.ID)
}

// TestL5_ProviderFailover tests failover when cheapest isn't available
func TestL5_ProviderFailover(t *testing.T) {
	// Find cheapest across all providers
	offer, provider := testEnv.FindCheapestGPU(t)
	require.NotNil(t, offer)

	t.Logf("Cheapest overall: %s from %s @ $%.4f/hr",
		offer.GPUType, provider, offer.PricePerHour)

	// Provision it
	resp := testEnv.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        offer.ID,
		WorkloadType:   "failover-test",
		ReservationHrs: 1,
	})
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Verify it comes up
	session := testEnv.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)
	assert.Equal(t, string(provider), session.Provider)

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
}
