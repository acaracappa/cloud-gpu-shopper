//go:build live
// +build live

package live

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// MultiAgentCoordinator orchestrates multi-agent validation workflows
type MultiAgentCoordinator struct {
	env       *LiveTestEnv
	qaAgent   *QAAgent
	mu        sync.Mutex
	results   []ValidationResult
}

// NewMultiAgentCoordinator creates a new multi-agent coordinator
func NewMultiAgentCoordinator(env *LiveTestEnv) *MultiAgentCoordinator {
	return &MultiAgentCoordinator{
		env:     env,
		qaAgent: NewQAAgent(DefaultQAConfig()),
		results: make([]ValidationResult, 0),
	}
}

// WorkloadConfig represents configuration for an entrypoint workload
type WorkloadConfig struct {
	LaunchMode   string
	DockerImage  string
	ModelID      string
	ExposedPorts []int
	Quantization string
}

// ProvisionAndValidateSSH provisions a session and validates SSH connectivity
func (mac *MultiAgentCoordinator) ProvisionAndValidateSSH(t *testing.T, provider Provider) *CreateSessionResponse {
	// Find a suitable offer
	offer := mac.env.FindCheapestFromProvider(t, provider)
	require.NotNil(t, offer, "No available offers from %s", provider)

	// Create session
	resp := mac.env.CreateSession(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        offer.ID,
		WorkloadType:   "interactive",
		ReservationHrs: 1,
	})

	// Wait for session to be running
	session := mac.env.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)
	t.Logf("Session %s is running", session.ID)

	// Run QA validation
	task := ValidationTask{
		Type:       ValidationSSH,
		SessionID:  session.ID,
		Provider:   provider,
		Host:       session.SSHHost,
		Port:       session.SSHPort,
		User:       session.SSHUser,
		PrivateKey: resp.SSHPrivateKey,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result := mac.qaAgent.ValidateSSH(ctx, task)
	mac.recordResult(result)

	if !result.Success {
		t.Logf("SSH validation failed: %s", result.Error)
		for k, v := range result.Details {
			t.Logf("  %s: %s", k, v)
		}
	} else {
		t.Logf("SSH validation passed in %v", result.Duration)
		for k, v := range result.Details {
			t.Logf("  %s: %s", k, v)
		}
	}

	require.True(t, result.Success, "SSH validation should pass")

	return resp
}

// ProvisionAndValidateAPI provisions an entrypoint session and validates API endpoint
func (mac *MultiAgentCoordinator) ProvisionAndValidateAPI(t *testing.T, provider Provider, workload WorkloadConfig) *CreateSessionResponse {
	// Find a suitable offer
	offer := mac.env.FindCheapestFromProvider(t, provider)
	require.NotNil(t, offer, "No available offers from %s", provider)

	// Create session with entrypoint mode
	resp, err := mac.env.CreateSessionWithError(t, CreateSessionRequest{
		ConsumerID:     GenerateConsumerID(),
		OfferID:        offer.ID,
		WorkloadType:   workload.LaunchMode,
		ReservationHrs: 1,
	})
	require.NoError(t, err, "Failed to create entrypoint session")

	// Wait for session to be running (longer timeout for model loading)
	session := mac.env.WaitForStatus(t, resp.Session.ID, "running", 10*time.Minute)
	t.Logf("Session %s is running", session.ID)

	// Determine API port
	apiPort := 8000 // Default for vLLM
	if len(workload.ExposedPorts) > 0 {
		apiPort = workload.ExposedPorts[0]
	}

	// Run HTTP health check
	httpTask := ValidationTask{
		Type:      ValidationHTTP,
		SessionID: session.ID,
		Provider:  provider,
		Host:      session.SSHHost,
		Port:      apiPort,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	httpResult := mac.qaAgent.ValidateHTTPEndpoint(ctx, httpTask)
	mac.recordResult(httpResult)

	if !httpResult.Success {
		t.Logf("HTTP validation failed: %s", httpResult.Error)
	} else {
		t.Logf("HTTP validation passed in %v", httpResult.Duration)
	}

	require.True(t, httpResult.Success, "HTTP validation should pass")

	return resp
}

// ProvisionAndValidateLLM provisions an LLM workload and validates inference
func (mac *MultiAgentCoordinator) ProvisionAndValidateLLM(t *testing.T, provider Provider, workload WorkloadConfig) *CreateSessionResponse {
	// First provision and validate API
	resp := mac.ProvisionAndValidateAPI(t, provider, workload)
	session := mac.env.GetSession(t, resp.Session.ID)

	// Determine API port
	apiPort := 8000
	if len(workload.ExposedPorts) > 0 {
		apiPort = workload.ExposedPorts[0]
	}

	// Now run LLM inference validation
	modelTask := ValidationTask{
		Type:      ValidationModel,
		SessionID: session.ID,
		Provider:  provider,
		Host:      session.SSHHost,
		Port:      apiPort,
		ModelID:   workload.ModelID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	modelResult := mac.qaAgent.ValidateLLMEndpoint(ctx, modelTask)
	mac.recordResult(modelResult)

	if !modelResult.Success {
		t.Logf("LLM validation failed: %s", modelResult.Error)
		for k, v := range modelResult.Details {
			t.Logf("  %s: %s", k, v)
		}
	} else {
		t.Logf("LLM validation passed in %v", modelResult.Duration)
		for k, v := range modelResult.Details {
			t.Logf("  %s: %s", k, v)
		}
	}

	require.True(t, modelResult.Success, "LLM validation should pass")

	return resp
}

// RunParallelValidation runs validations across multiple providers in parallel
func (mac *MultiAgentCoordinator) RunParallelValidation(t *testing.T, providers []Provider) map[Provider]*ValidationResult {
	results := make(map[Provider]*ValidationResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, prov := range providers {
		if !mac.env.Config.HasProvider(prov) {
			t.Logf("Skipping %s - not configured", prov)
			continue
		}

		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()

			t.Logf("Starting parallel validation for %s", p)

			// Provision session
			offer := mac.env.FindCheapestFromProvider(t, p)
			if offer == nil {
				mu.Lock()
				results[p] = &ValidationResult{
					Type:    ValidationSSH,
					Success: false,
					Error:   "no offers available",
				}
				mu.Unlock()
				return
			}

			resp, err := mac.env.CreateSessionWithError(t, CreateSessionRequest{
				ConsumerID:     GenerateConsumerID(),
				OfferID:        offer.ID,
				WorkloadType:   "interactive",
				ReservationHrs: 1,
			})
			if err != nil {
				mu.Lock()
				results[p] = &ValidationResult{
					Type:    ValidationSSH,
					Success: false,
					Error:   fmt.Sprintf("provision failed: %v", err),
				}
				mu.Unlock()
				return
			}

			defer mac.env.Cleanup(t, resp.Session.ID)

			// Wait for running
			session := mac.env.WaitForStatus(t, resp.Session.ID, "running", 5*time.Minute)

			// Run SSH validation
			task := ValidationTask{
				Type:       ValidationSSH,
				SessionID:  session.ID,
				Provider:   p,
				Host:       session.SSHHost,
				Port:       session.SSHPort,
				User:       session.SSHUser,
				PrivateKey: resp.SSHPrivateKey,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			result := mac.qaAgent.ValidateSSH(ctx, task)

			mu.Lock()
			results[p] = &result
			mac.results = append(mac.results, result)
			mu.Unlock()

			// Cleanup
			mac.env.DestroySession(t, resp.Session.ID)
		}(prov)
	}

	wg.Wait()
	return results
}

// GetResults returns all collected validation results
func (mac *MultiAgentCoordinator) GetResults() []ValidationResult {
	mac.mu.Lock()
	defer mac.mu.Unlock()
	return append([]ValidationResult{}, mac.results...)
}

// recordResult records a validation result
func (mac *MultiAgentCoordinator) recordResult(result ValidationResult) {
	mac.mu.Lock()
	defer mac.mu.Unlock()
	mac.results = append(mac.results, result)
}

// PrintSummary prints a summary of all validation results
func (mac *MultiAgentCoordinator) PrintSummary(t *testing.T) {
	mac.mu.Lock()
	defer mac.mu.Unlock()

	t.Log("=== Validation Summary ===")

	successCount := 0
	failCount := 0
	var totalDuration time.Duration

	for _, result := range mac.results {
		status := "PASS"
		if !result.Success {
			status = "FAIL"
			failCount++
		} else {
			successCount++
		}
		totalDuration += result.Duration

		t.Logf("  [%s] %s/%s: %s (duration: %v)",
			status, result.Provider, result.Type, result.Message, result.Duration)

		if result.Error != "" {
			t.Logf("    Error: %s", result.Error)
		}
	}

	t.Logf("Total: %d passed, %d failed, total duration: %v",
		successCount, failCount, totalDuration)
}
