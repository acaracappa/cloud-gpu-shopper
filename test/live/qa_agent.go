//go:build live
// +build live

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ValidationType represents the type of validation to perform
type ValidationType string

const (
	ValidationSSH   ValidationType = "ssh"   // SSH connectivity + GPU validation
	ValidationHTTP  ValidationType = "http"  // HTTP health endpoint check
	ValidationModel ValidationType = "model" // LLM inference test
)

// ValidationResult contains the result of a validation check
type ValidationResult struct {
	Type      ValidationType    `json:"type"`
	Success   bool              `json:"success"`
	Duration  time.Duration     `json:"duration"`
	Message   string            `json:"message,omitempty"`
	Error     string            `json:"error,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
	SessionID string            `json:"session_id"`
	Provider  string            `json:"provider"`
	Timestamp time.Time         `json:"timestamp"`
}

// ValidationTask represents a validation task for the QA agent
type ValidationTask struct {
	Type       ValidationType
	SessionID  string
	Provider   Provider
	Host       string
	Port       int
	User       string
	PrivateKey string
	Endpoint   string // For HTTP/Model validation
	ModelID    string // For Model validation
}

// QAConfig configures the QA agent behavior
type QAConfig struct {
	SSHTimeout    time.Duration
	HTTPTimeout   time.Duration
	ModelTimeout  time.Duration
	RetryInterval time.Duration
	MaxRetries    int
}

// DefaultQAConfig returns default QA agent configuration
func DefaultQAConfig() *QAConfig {
	return &QAConfig{
		SSHTimeout:    60 * time.Second,
		HTTPTimeout:   30 * time.Second,
		ModelTimeout:  120 * time.Second,
		RetryInterval: 5 * time.Second,
		MaxRetries:    3,
	}
}

// QAAgent performs validation checks on provisioned GPU instances
type QAAgent struct {
	config     *QAConfig
	httpClient *http.Client
}

// NewQAAgent creates a new QA validation agent
func NewQAAgent(config *QAConfig) *QAAgent {
	if config == nil {
		config = DefaultQAConfig()
	}
	return &QAAgent{
		config: config,
		httpClient: &http.Client{
			Timeout: config.HTTPTimeout,
		},
	}
}

// ValidateSSH performs SSH validation on a session
// Checks: SSH connectivity, nvidia-smi output, running processes
func (qa *QAAgent) ValidateSSH(ctx context.Context, task ValidationTask) ValidationResult {
	start := time.Now()
	result := ValidationResult{
		Type:      ValidationSSH,
		SessionID: task.SessionID,
		Provider:  string(task.Provider),
		Timestamp: time.Now(),
		Details:   make(map[string]string),
	}

	// Create SSH helper
	sshHelper := NewSSHHelper(task.Host, task.Port, task.User, task.PrivateKey)
	defer sshHelper.Close()

	// Connect with timeout
	connectCtx, cancel := context.WithTimeout(ctx, qa.config.SSHTimeout)
	defer cancel()

	if err := sshHelper.Connect(connectCtx); err != nil {
		result.Error = fmt.Sprintf("SSH connection failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	result.Details["ssh_connected"] = "true"

	// Check nvidia-smi
	nvidiaSMI, err := sshHelper.GetNvidiaSMI(ctx)
	if err != nil {
		result.Details["nvidia_smi"] = fmt.Sprintf("error: %v", err)
	} else {
		// Parse GPU info from nvidia-smi
		if strings.Contains(nvidiaSMI, "NVIDIA-SMI") {
			result.Details["nvidia_smi"] = "available"
			// Extract GPU count
			gpuLines := strings.Count(nvidiaSMI, "MiB")
			result.Details["gpu_memory_lines"] = fmt.Sprintf("%d", gpuLines/2) // Each GPU has used and total
		} else {
			result.Details["nvidia_smi"] = "not available"
		}
	}

	// Get process list
	processes, err := sshHelper.GetProcessList(ctx)
	if err != nil {
		result.Details["process_list"] = fmt.Sprintf("error: %v", err)
	} else {
		result.Details["process_count"] = fmt.Sprintf("%d", strings.Count(processes, "\n"))
	}

	// Get network status
	networkStatus, err := sshHelper.GetNetworkStatus(ctx)
	if err != nil {
		result.Details["network_status"] = fmt.Sprintf("error: %v", err)
	} else {
		result.Details["listening_ports"] = fmt.Sprintf("%d", strings.Count(networkStatus, "LISTEN"))
	}

	result.Success = true
	result.Message = "SSH validation successful"
	result.Duration = time.Since(start)
	return result
}

// ValidateHTTPEndpoint performs HTTP health check validation
func (qa *QAAgent) ValidateHTTPEndpoint(ctx context.Context, task ValidationTask) ValidationResult {
	start := time.Now()
	result := ValidationResult{
		Type:      ValidationHTTP,
		SessionID: task.SessionID,
		Provider:  string(task.Provider),
		Timestamp: time.Now(),
		Details:   make(map[string]string),
	}

	endpoint := task.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("http://%s:%d/health", task.Host, task.Port)
	}
	result.Details["endpoint"] = endpoint

	// Try health check with retries
	var lastErr error
	for attempt := 0; attempt < qa.config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				result.Error = "context cancelled"
				result.Duration = time.Since(start)
				return result
			case <-time.After(qa.config.RetryInterval):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := qa.httpClient.Do(req)
		if err != nil {
			lastErr = err
			result.Details[fmt.Sprintf("attempt_%d", attempt+1)] = fmt.Sprintf("error: %v", err)
			continue
		}
		defer resp.Body.Close()

		result.Details["status_code"] = fmt.Sprintf("%d", resp.StatusCode)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			result.Success = true
			result.Message = fmt.Sprintf("HTTP endpoint responding (status %d)", resp.StatusCode)
			result.Duration = time.Since(start)
			return result
		}

		lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		result.Details[fmt.Sprintf("attempt_%d", attempt+1)] = lastErr.Error()
	}

	result.Error = fmt.Sprintf("HTTP validation failed after %d attempts: %v", qa.config.MaxRetries, lastErr)
	result.Duration = time.Since(start)
	return result
}

// ValidateLLMEndpoint performs LLM inference validation
// Tests that the model can generate a response
func (qa *QAAgent) ValidateLLMEndpoint(ctx context.Context, task ValidationTask) ValidationResult {
	start := time.Now()
	result := ValidationResult{
		Type:      ValidationModel,
		SessionID: task.SessionID,
		Provider:  string(task.Provider),
		Timestamp: time.Now(),
		Details:   make(map[string]string),
	}

	// Determine endpoint
	endpoint := task.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("http://%s:%d", task.Host, task.Port)
	}
	result.Details["base_endpoint"] = endpoint

	// First check /models endpoint to verify model is loaded
	modelsURL := endpoint + "/v1/models"
	result.Details["models_endpoint"] = modelsURL

	modelsCtx, cancel := context.WithTimeout(ctx, qa.config.HTTPTimeout)
	defer cancel()

	modelsReq, err := http.NewRequestWithContext(modelsCtx, "GET", modelsURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("failed to create models request: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	modelsResp, err := qa.httpClient.Do(modelsReq)
	if err != nil {
		result.Error = fmt.Sprintf("models endpoint check failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer modelsResp.Body.Close()

	if modelsResp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("models endpoint returned status %d", modelsResp.StatusCode)
		result.Duration = time.Since(start)
		return result
	}

	// Parse models response
	var modelsData struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(modelsResp.Body).Decode(&modelsData); err != nil {
		result.Details["models_parse_error"] = err.Error()
	} else if len(modelsData.Data) > 0 {
		result.Details["loaded_model"] = modelsData.Data[0].ID
	}

	// Now test completions endpoint
	completionsURL := endpoint + "/v1/completions"
	result.Details["completions_endpoint"] = completionsURL

	// Simple test prompt
	testReq := map[string]interface{}{
		"prompt":      "Hello, my name is",
		"max_tokens":  10,
		"temperature": 0.1,
	}

	if task.ModelID != "" {
		testReq["model"] = task.ModelID
	} else if len(modelsData.Data) > 0 {
		testReq["model"] = modelsData.Data[0].ID
	}

	reqBody, err := json.Marshal(testReq)
	if err != nil {
		result.Error = fmt.Sprintf("failed to marshal request: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	// Use model timeout for inference
	inferenceCtx, cancel2 := context.WithTimeout(ctx, qa.config.ModelTimeout)
	defer cancel2()

	req, err := http.NewRequestWithContext(inferenceCtx, "POST", completionsURL, strings.NewReader(string(reqBody)))
	if err != nil {
		result.Error = fmt.Sprintf("failed to create request: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := qa.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("completions request failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer resp.Body.Close()

	result.Details["completions_status"] = fmt.Sprintf("%d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Sprintf("completions endpoint returned status %d: %s", resp.StatusCode, string(body))
		result.Duration = time.Since(start)
		return result
	}

	// Parse response to verify we got generated text
	var completionResp struct {
		Choices []struct {
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completionResp); err != nil {
		result.Error = fmt.Sprintf("failed to parse completion response: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	if len(completionResp.Choices) == 0 {
		result.Error = "no completions returned"
		result.Duration = time.Since(start)
		return result
	}

	generatedText := completionResp.Choices[0].Text
	result.Details["generated_text_length"] = fmt.Sprintf("%d", len(generatedText))
	result.Details["generated_text_preview"] = truncateString(generatedText, 50)

	result.Success = true
	result.Message = "LLM inference successful"
	result.Duration = time.Since(start)
	return result
}

// RunValidation runs the appropriate validation based on task type
func (qa *QAAgent) RunValidation(ctx context.Context, task ValidationTask) ValidationResult {
	switch task.Type {
	case ValidationSSH:
		return qa.ValidateSSH(ctx, task)
	case ValidationHTTP:
		return qa.ValidateHTTPEndpoint(ctx, task)
	case ValidationModel:
		return qa.ValidateLLMEndpoint(ctx, task)
	default:
		return ValidationResult{
			Type:      task.Type,
			Success:   false,
			Error:     fmt.Sprintf("unknown validation type: %s", task.Type),
			Timestamp: time.Now(),
		}
	}
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
