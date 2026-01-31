package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ThroughputResult holds the results of a throughput benchmark
type ThroughputResult struct {
	Timestamp       time.Time `json:"timestamp"`
	Endpoint        string    `json:"endpoint"`
	ModelID         string    `json:"model_id"`
	TotalRequests   int       `json:"total_requests"`
	WarmupRequests  int       `json:"warmup_requests"`
	PromptTokens    int       `json:"prompt_tokens"`
	MaxTokens       int       `json:"max_tokens"`
	TotalTokens     int       `json:"total_tokens"`
	TotalDurationMs float64   `json:"total_duration_ms"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	AvgLatencyMs    float64   `json:"avg_latency_ms"`
	SuccessRate     float64   `json:"success_rate"`
	Errors          []string  `json:"errors,omitempty"`
}

// CompletionRequest is the OpenAI-compatible completion request
type CompletionRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	Stream      bool    `json:"stream"`
}

// CompletionResponse is the OpenAI-compatible completion response
type CompletionResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Text         string `json:"text"`
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// ModelsResponse is the response from /v1/models endpoint
type ModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func runThroughput(cmd *cobra.Command, args []string) error {
	fmt.Fprintf(os.Stderr, "Starting throughput benchmark...\n")
	fmt.Fprintf(os.Stderr, "  Endpoint: %s\n", endpoint)
	fmt.Fprintf(os.Stderr, "  Requests: %d (+ %d warmup)\n", throughputRequests, warmupRequests)
	fmt.Fprintf(os.Stderr, "  Prompt tokens: ~%d\n", promptTokens)
	fmt.Fprintf(os.Stderr, "  Max tokens: %d\n", maxTokens)

	result := &ThroughputResult{
		Timestamp:      time.Now(),
		Endpoint:       endpoint,
		TotalRequests:  throughputRequests,
		WarmupRequests: warmupRequests,
		PromptTokens:   promptTokens,
		MaxTokens:      maxTokens,
	}

	// Get model ID
	modelID, err := getModelID(endpoint)
	if err != nil {
		return fmt.Errorf("failed to get model ID: %w", err)
	}
	result.ModelID = modelID
	fmt.Fprintf(os.Stderr, "  Model: %s\n\n", modelID)

	// Generate prompt of approximate token length
	prompt := generatePrompt(promptTokens)

	// Warmup
	fmt.Fprintf(os.Stderr, "Running warmup requests...\n")
	for i := 0; i < warmupRequests; i++ {
		_, err := sendCompletionRequest(endpoint, modelID, prompt, maxTokens)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warmup %d: error - %v\n", i+1, err)
		} else {
			fmt.Fprintf(os.Stderr, "  Warmup %d: OK\n", i+1)
		}
	}

	// Benchmark
	fmt.Fprintf(os.Stderr, "\nRunning benchmark...\n")
	var totalTokens int
	var successCount int
	var totalLatency time.Duration

	startTime := time.Now()

	for i := 0; i < throughputRequests; i++ {
		reqStart := time.Now()
		resp, err := sendCompletionRequest(endpoint, modelID, prompt, maxTokens)
		reqDuration := time.Since(reqStart)

		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("request %d: %v", i+1, err))
			continue
		}

		successCount++
		totalLatency += reqDuration
		tokens := resp.Usage.CompletionTokens
		totalTokens += tokens

		if (i+1)%10 == 0 {
			fmt.Fprintf(os.Stderr, "  Progress: %d/%d requests, %d tokens\n", i+1, throughputRequests, totalTokens)
		}
	}

	totalDuration := time.Since(startTime)

	// Calculate results
	result.TotalTokens = totalTokens
	result.TotalDurationMs = float64(totalDuration.Milliseconds())
	result.TokensPerSecond = float64(totalTokens) / totalDuration.Seconds()
	result.SuccessRate = float64(successCount) / float64(throughputRequests)

	if successCount > 0 {
		result.AvgLatencyMs = float64(totalLatency.Milliseconds()) / float64(successCount)
	}

	// Output results
	fmt.Fprintf(os.Stderr, "\nResults:\n")
	fmt.Fprintf(os.Stderr, "  Total tokens: %d\n", result.TotalTokens)
	fmt.Fprintf(os.Stderr, "  Duration: %.2f seconds\n", result.TotalDurationMs/1000)
	fmt.Fprintf(os.Stderr, "  Throughput: %.2f tokens/second\n", result.TokensPerSecond)
	fmt.Fprintf(os.Stderr, "  Avg latency: %.2f ms\n", result.AvgLatencyMs)
	fmt.Fprintf(os.Stderr, "  Success rate: %.2f%%\n", result.SuccessRate*100)

	return writeResult(result)
}

func getModelID(endpoint string) (string, error) {
	resp, err := http.Get(endpoint + "/v1/models")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var models ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return "", err
	}

	if len(models.Data) == 0 {
		return "", fmt.Errorf("no models available")
	}

	return models.Data[0].ID, nil
}

func generatePrompt(approxTokens int) string {
	// Rough approximation: 4 characters per token
	words := []string{
		"The", "quick", "brown", "fox", "jumps", "over", "the", "lazy", "dog.",
		"A", "journey", "of", "a", "thousand", "miles", "begins", "with", "a", "single", "step.",
		"To", "be", "or", "not", "to", "be,", "that", "is", "the", "question.",
		"All", "that", "glitters", "is", "not", "gold.",
	}

	var sb strings.Builder
	sb.WriteString("Please continue the following text with exactly 256 tokens:\n\n")

	targetChars := approxTokens * 4
	for sb.Len() < targetChars {
		for _, word := range words {
			sb.WriteString(word)
			sb.WriteString(" ")
			if sb.Len() >= targetChars {
				break
			}
		}
	}

	return sb.String()
}

func sendCompletionRequest(endpoint, modelID, prompt string, maxTokens int) (*CompletionResponse, error) {
	req := CompletionRequest{
		Model:       modelID,
		Prompt:      prompt,
		MaxTokens:   maxTokens,
		Temperature: 0.7,
		Stream:      false,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", endpoint+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result CompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func writeResult(result interface{}) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}

	if output == "" {
		fmt.Println(string(data))
		return nil
	}

	return os.WriteFile(output, data, 0644)
}
