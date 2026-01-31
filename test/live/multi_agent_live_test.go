//go:build live
// +build live

package live

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==============================================================================
// MULTI-AGENT VALIDATION TESTS
// ==============================================================================

// TestL6_MultiAgent_SSHValidation tests SSH provisioning with full QA validation
func TestL6_MultiAgent_SSHValidation(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	mac := NewMultiAgentCoordinator(testEnv)

	// Provision and validate SSH for Vast.ai
	resp := mac.ProvisionAndValidateSSH(t, ProviderVastAI)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Verify session details
	session := testEnv.GetSession(t, resp.Session.ID)
	assert.Equal(t, "running", session.Status)
	assert.NotEmpty(t, session.SSHHost)
	assert.Greater(t, session.SSHPort, 0)

	// Print summary
	mac.PrintSummary(t)

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

// TestL6_MultiAgent_SSHValidation_TensorDock tests SSH with QA validation on TensorDock
func TestL6_MultiAgent_SSHValidation_TensorDock(t *testing.T) {
	if !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("TensorDock not configured")
	}

	mac := NewMultiAgentCoordinator(testEnv)

	// Provision and validate SSH for TensorDock
	resp := mac.ProvisionAndValidateSSH(t, ProviderTensorDock)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Verify session details
	session := testEnv.GetSession(t, resp.Session.ID)
	assert.Equal(t, "running", session.Status)
	assert.NotEmpty(t, session.SSHHost)
	assert.Greater(t, session.SSHPort, 0)

	// Print summary
	mac.PrintSummary(t)

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

// TestL7_MultiAgent_EntrypointMode tests entrypoint mode provisioning with vLLM
// NOTE: This test requires HF_TOKEN environment variable for model download
func TestL7_MultiAgent_EntrypointMode(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	// This test is expensive and slow - skip unless explicitly enabled
	t.Skip("Entrypoint mode test requires HF_TOKEN and is expensive - run manually with: go test -tags=live -run TestL7 ./test/live/...")

	mac := NewMultiAgentCoordinator(testEnv)

	workload := WorkloadConfig{
		LaunchMode:   "llm_vllm",
		ModelID:      "TinyLlama/TinyLlama-1.1B-Chat-v1.0",
		ExposedPorts: []int{8000},
	}

	// Provision and validate API endpoint
	resp := mac.ProvisionAndValidateAPI(t, ProviderVastAI, workload)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Print summary
	mac.PrintSummary(t)

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

// TestL7_MultiAgent_LLMInference tests full LLM inference validation
// NOTE: This test requires HF_TOKEN environment variable for model download
func TestL7_MultiAgent_LLMInference(t *testing.T) {
	if !testConfig.HasProvider(ProviderVastAI) {
		t.Skip("Vast.ai not configured")
	}

	// This test is expensive and slow - skip unless explicitly enabled
	t.Skip("LLM inference test requires HF_TOKEN and is expensive - run manually with: go test -tags=live -run TestL7 ./test/live/...")

	mac := NewMultiAgentCoordinator(testEnv)

	workload := WorkloadConfig{
		LaunchMode:   "llm_vllm",
		ModelID:      "TinyLlama/TinyLlama-1.1B-Chat-v1.0",
		ExposedPorts: []int{8000},
	}

	// Provision and validate LLM inference
	resp := mac.ProvisionAndValidateLLM(t, ProviderVastAI, workload)
	defer testEnv.Cleanup(t, resp.Session.ID)

	// Print summary
	mac.PrintSummary(t)

	// Cleanup
	testEnv.DestroySession(t, resp.Session.ID)
	testEnv.WaitForStatus(t, resp.Session.ID, "stopped", 2*time.Minute)
}

// TestL8_MultiAgent_ParallelValidation runs validation across multiple providers in parallel
func TestL8_MultiAgent_ParallelValidation(t *testing.T) {
	// Check we have at least one provider
	enabledProviders := testConfig.EnabledProviders()
	if len(enabledProviders) == 0 {
		t.Skip("No providers configured")
	}

	mac := NewMultiAgentCoordinator(testEnv)

	// Run parallel validation across all enabled providers
	results := mac.RunParallelValidation(t, enabledProviders)

	// Check results
	for provider, result := range results {
		if result.Success {
			t.Logf("[%s] Validation PASSED in %v", provider, result.Duration)
		} else {
			t.Logf("[%s] Validation FAILED: %s", provider, result.Error)
		}
	}

	// Print full summary
	mac.PrintSummary(t)

	// At least one provider should pass
	successCount := 0
	for _, result := range results {
		if result.Success {
			successCount++
		}
	}

	require.Greater(t, successCount, 0, "At least one provider should pass validation")
}

// TestL8_MultiAgent_CrossProviderComparison compares validation results across providers
func TestL8_MultiAgent_CrossProviderComparison(t *testing.T) {
	// Require both providers
	if !testConfig.HasProvider(ProviderVastAI) || !testConfig.HasProvider(ProviderTensorDock) {
		t.Skip("Both Vast.ai and TensorDock must be configured")
	}

	mac := NewMultiAgentCoordinator(testEnv)

	// Run parallel validation
	results := mac.RunParallelValidation(t, []Provider{ProviderVastAI, ProviderTensorDock})

	// Compare results
	vastResult, vastOK := results[ProviderVastAI]
	tensorResult, tensorOK := results[ProviderTensorDock]

	require.True(t, vastOK, "Vast.ai result should exist")
	require.True(t, tensorOK, "TensorDock result should exist")

	t.Log("=== Cross-Provider Comparison ===")
	t.Logf("Vast.ai:    success=%v, duration=%v", vastResult.Success, vastResult.Duration)
	t.Logf("TensorDock: success=%v, duration=%v", tensorResult.Success, tensorResult.Duration)

	// Log GPU details if available
	if vastResult.Details != nil {
		t.Logf("Vast.ai GPU details:")
		for k, v := range vastResult.Details {
			t.Logf("  %s: %s", k, v)
		}
	}
	if tensorResult.Details != nil {
		t.Logf("TensorDock GPU details:")
		for k, v := range tensorResult.Details {
			t.Logf("  %s: %s", k, v)
		}
	}

	// Print summary
	mac.PrintSummary(t)

	// Both should succeed
	assert.True(t, vastResult.Success, "Vast.ai validation should pass")
	assert.True(t, tensorResult.Success, "TensorDock validation should pass")
}

// TestL9_MultiAgent_QAAgentDirect tests QA agent directly without provisioning
func TestL9_MultiAgent_QAAgentDirect(t *testing.T) {
	// This test validates the QA agent's validation logic without provisioning
	qa := NewQAAgent(DefaultQAConfig())

	t.Run("ValidationResult_Structure", func(t *testing.T) {
		result := ValidationResult{
			Type:      ValidationSSH,
			Success:   true,
			Duration:  5 * time.Second,
			Message:   "test message",
			Details:   map[string]string{"key": "value"},
			SessionID: "test-session-123",
			Provider:  "test-provider",
		}

		assert.Equal(t, ValidationSSH, result.Type)
		assert.True(t, result.Success)
		assert.Equal(t, "test-session-123", result.SessionID)
		assert.Equal(t, "value", result.Details["key"])
	})

	t.Run("QAConfig_Defaults", func(t *testing.T) {
		config := DefaultQAConfig()
		assert.Equal(t, 60*time.Second, config.SSHTimeout)
		assert.Equal(t, 30*time.Second, config.HTTPTimeout)
		assert.Equal(t, 120*time.Second, config.ModelTimeout)
		assert.Equal(t, 3, config.MaxRetries)
	})

	t.Run("QAAgent_Creation", func(t *testing.T) {
		assert.NotNil(t, qa)
		assert.NotNil(t, qa.httpClient)
	})

	// Note: Actual validation tests require running instances
	t.Log("QA Agent direct tests passed")
}
