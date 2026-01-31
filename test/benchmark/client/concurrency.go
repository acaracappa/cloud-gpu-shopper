package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

// ConcurrencyResult holds the results of a concurrency benchmark
type ConcurrencyResult struct {
	Timestamp            time.Time            `json:"timestamp"`
	Endpoint             string               `json:"endpoint"`
	ModelID              string               `json:"model_id"`
	MaxTestedConcurrency int                  `json:"max_tested_concurrency"`
	OptimalConcurrency   int                  `json:"optimal_concurrency"`
	OptimalThroughput    float64              `json:"optimal_throughput_tps"`
	DegradationPoint     int                  `json:"degradation_point"`
	Results              []ConcurrencyLevel   `json:"results"`
	Errors               []string             `json:"errors,omitempty"`
}

// ConcurrencyLevel holds results for a specific concurrency level
type ConcurrencyLevel struct {
	Concurrency      int     `json:"concurrency"`
	TotalRequests    int     `json:"total_requests"`
	SuccessCount     int     `json:"success_count"`
	ErrorCount       int     `json:"error_count"`
	TotalTokens      int     `json:"total_tokens"`
	DurationMs       float64 `json:"duration_ms"`
	ThroughputTPS    float64 `json:"throughput_tps"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	ErrorRate        float64 `json:"error_rate"`
}

func runConcurrency(cmd *cobra.Command, args []string) error {
	fmt.Fprintf(os.Stderr, "Starting concurrency benchmark...\n")
	fmt.Fprintf(os.Stderr, "  Endpoint: %s\n", endpoint)
	fmt.Fprintf(os.Stderr, "  Max concurrency: %d\n", maxConcurrent)

	result := &ConcurrencyResult{
		Timestamp:            time.Now(),
		Endpoint:             endpoint,
		MaxTestedConcurrency: maxConcurrent,
	}

	// Get model ID
	modelID, err := getModelID(endpoint)
	if err != nil {
		return fmt.Errorf("failed to get model ID: %w", err)
	}
	result.ModelID = modelID
	fmt.Fprintf(os.Stderr, "  Model: %s\n\n", modelID)

	// Prompt for concurrency testing
	prompt := "Write a short poem about the ocean in exactly 50 words."

	// Test concurrency levels: 1, 2, 4, 8, 16, 32...
	concurrencyLevels := []int{1, 2, 4, 8, 16}
	for c := 24; c <= maxConcurrent; c += 8 {
		concurrencyLevels = append(concurrencyLevels, c)
	}

	var maxThroughput float64
	var optimalConcurrency int
	var prevThroughput float64
	degradationDetected := false

	for _, concurrency := range concurrencyLevels {
		if concurrency > maxConcurrent {
			break
		}

		fmt.Fprintf(os.Stderr, "Testing concurrency level: %d\n", concurrency)

		levelResult := testConcurrencyLevel(endpoint, modelID, prompt, concurrency)
		result.Results = append(result.Results, levelResult)

		fmt.Fprintf(os.Stderr, "  Throughput: %.2f tok/s, Error rate: %.2f%%\n",
			levelResult.ThroughputTPS, levelResult.ErrorRate*100)

		// Track optimal concurrency
		if levelResult.ThroughputTPS > maxThroughput && levelResult.ErrorRate < 0.05 {
			maxThroughput = levelResult.ThroughputTPS
			optimalConcurrency = concurrency
		}

		// Detect degradation (throughput drops or error rate increases)
		if !degradationDetected && prevThroughput > 0 {
			throughputDrop := (prevThroughput - levelResult.ThroughputTPS) / prevThroughput
			if throughputDrop > 0.1 || levelResult.ErrorRate > 0.1 {
				degradationDetected = true
				result.DegradationPoint = concurrency
				fmt.Fprintf(os.Stderr, "  Degradation detected at concurrency %d\n", concurrency)
			}
		}

		prevThroughput = levelResult.ThroughputTPS

		// Stop if error rate is too high
		if levelResult.ErrorRate > 0.5 {
			fmt.Fprintf(os.Stderr, "  Stopping due to high error rate\n")
			break
		}
	}

	result.OptimalConcurrency = optimalConcurrency
	result.OptimalThroughput = maxThroughput

	// Output results
	fmt.Fprintf(os.Stderr, "\nResults:\n")
	fmt.Fprintf(os.Stderr, "  Optimal concurrency: %d\n", result.OptimalConcurrency)
	fmt.Fprintf(os.Stderr, "  Peak throughput: %.2f tok/s\n", result.OptimalThroughput)
	if result.DegradationPoint > 0 {
		fmt.Fprintf(os.Stderr, "  Degradation point: %d\n", result.DegradationPoint)
	}

	return writeResult(result)
}

func testConcurrencyLevel(endpoint, modelID, prompt string, concurrency int) ConcurrencyLevel {
	result := ConcurrencyLevel{
		Concurrency:   concurrency,
		TotalRequests: concurrency * 3, // 3 requests per concurrent worker
	}

	requestsPerWorker := 3
	var wg sync.WaitGroup
	var totalTokens int64
	var successCount int64
	var errorCount int64
	var totalLatency int64

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < requestsPerWorker; j++ {
				reqStart := time.Now()
				resp, err := sendCompletionRequestWithTimeout(endpoint, modelID, prompt, 64, 30*time.Second)
				latency := time.Since(reqStart)

				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					continue
				}

				atomic.AddInt64(&successCount, 1)
				atomic.AddInt64(&totalTokens, int64(resp.Usage.CompletionTokens))
				atomic.AddInt64(&totalLatency, latency.Milliseconds())
			}
		}()
	}

	wg.Wait()
	duration := time.Since(startTime)

	result.SuccessCount = int(successCount)
	result.ErrorCount = int(errorCount)
	result.TotalTokens = int(totalTokens)
	result.DurationMs = float64(duration.Milliseconds())
	result.ErrorRate = float64(errorCount) / float64(result.TotalRequests)

	if successCount > 0 {
		result.ThroughputTPS = float64(totalTokens) / duration.Seconds()
		result.AvgLatencyMs = float64(totalLatency) / float64(successCount)
	}

	return result
}

func sendCompletionRequestWithTimeout(endpoint, modelID, prompt string, maxTokens int, timeout time.Duration) (*CompletionResponse, error) {
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

	client := &http.Client{Timeout: timeout}
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
