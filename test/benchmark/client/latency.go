package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// LatencyResult holds the results of a latency benchmark
type LatencyResult struct {
	Timestamp      time.Time `json:"timestamp"`
	Endpoint       string    `json:"endpoint"`
	ModelID        string    `json:"model_id"`
	TotalRequests  int       `json:"total_requests"`
	SuccessCount   int       `json:"success_count"`
	PromptTokens   int       `json:"prompt_tokens"`
	MaxTokens      int       `json:"max_tokens"`
	TTFTMs         float64   `json:"ttft_ms"`
	TTFTMedianMs   float64   `json:"ttft_median_ms"`
	TTFTP50Ms      float64   `json:"ttft_p50_ms"`
	TTFTP90Ms      float64   `json:"ttft_p90_ms"`
	TTFTP99Ms      float64   `json:"ttft_p99_ms"`
	TTFTMinMs      float64   `json:"ttft_min_ms"`
	TTFTMaxMs      float64   `json:"ttft_max_ms"`
	TTFTStdDevMs   float64   `json:"ttft_stddev_ms"`
	AvgLatencyMs   float64   `json:"avg_latency_ms"`
	SuccessRate    float64   `json:"success_rate"`
	TTFTSamples    []float64 `json:"ttft_samples"`
	Errors         []string  `json:"errors,omitempty"`
}

// StreamCompletionRequest is the request for streaming completions
type StreamCompletionRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	Stream      bool    `json:"stream"`
}

func runLatency(cmd *cobra.Command, args []string) error {
	fmt.Fprintf(os.Stderr, "Starting latency benchmark...\n")
	fmt.Fprintf(os.Stderr, "  Endpoint: %s\n", endpoint)
	fmt.Fprintf(os.Stderr, "  Requests: %d\n", latencyRequests)

	result := &LatencyResult{
		Timestamp:     time.Now(),
		Endpoint:      endpoint,
		TotalRequests: latencyRequests,
		PromptTokens:  promptTokens,
		MaxTokens:     64, // Short responses for latency testing
	}

	// Get model ID
	modelID, err := getModelID(endpoint)
	if err != nil {
		return fmt.Errorf("failed to get model ID: %w", err)
	}
	result.ModelID = modelID
	fmt.Fprintf(os.Stderr, "  Model: %s\n\n", modelID)

	// Generate short prompt for latency testing
	prompt := "Complete this sentence in exactly 10 words: The sun rises in the"

	// Warmup with non-streaming request
	fmt.Fprintf(os.Stderr, "Warmup...\n")
	_, _ = sendCompletionRequest(endpoint, modelID, prompt, 64)

	// Measure TTFT for each request
	fmt.Fprintf(os.Stderr, "Measuring TTFT...\n")
	var ttftSamples []float64
	var totalLatency time.Duration

	for i := 0; i < latencyRequests; i++ {
		ttft, totalTime, err := measureTTFT(endpoint, modelID, prompt, result.MaxTokens)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("request %d: %v", i+1, err))
			continue
		}

		result.SuccessCount++
		ttftSamples = append(ttftSamples, ttft)
		totalLatency += totalTime

		if (i+1)%10 == 0 {
			fmt.Fprintf(os.Stderr, "  Progress: %d/%d requests\n", i+1, latencyRequests)
		}
	}

	if len(ttftSamples) == 0 {
		return fmt.Errorf("no successful requests")
	}

	// Calculate statistics
	result.TTFTSamples = ttftSamples
	result.SuccessRate = float64(result.SuccessCount) / float64(result.TotalRequests)
	result.AvgLatencyMs = float64(totalLatency.Milliseconds()) / float64(result.SuccessCount)

	// Sort samples for percentile calculations
	sort.Float64s(ttftSamples)

	result.TTFTMinMs = ttftSamples[0]
	result.TTFTMaxMs = ttftSamples[len(ttftSamples)-1]
	result.TTFTMedianMs = percentile(ttftSamples, 50)
	result.TTFTP50Ms = percentile(ttftSamples, 50)
	result.TTFTP90Ms = percentile(ttftSamples, 90)
	result.TTFTP99Ms = percentile(ttftSamples, 99)
	result.TTFTMs = mean(ttftSamples)
	result.TTFTStdDevMs = stddev(ttftSamples)

	// Output results
	fmt.Fprintf(os.Stderr, "\nResults:\n")
	fmt.Fprintf(os.Stderr, "  TTFT Mean: %.2f ms\n", result.TTFTMs)
	fmt.Fprintf(os.Stderr, "  TTFT Median: %.2f ms\n", result.TTFTMedianMs)
	fmt.Fprintf(os.Stderr, "  TTFT P90: %.2f ms\n", result.TTFTP90Ms)
	fmt.Fprintf(os.Stderr, "  TTFT P99: %.2f ms\n", result.TTFTP99Ms)
	fmt.Fprintf(os.Stderr, "  TTFT Min: %.2f ms\n", result.TTFTMinMs)
	fmt.Fprintf(os.Stderr, "  TTFT Max: %.2f ms\n", result.TTFTMaxMs)
	fmt.Fprintf(os.Stderr, "  Success rate: %.2f%%\n", result.SuccessRate*100)

	return writeResult(result)
}

func measureTTFT(endpoint, modelID, prompt string, maxTokens int) (ttftMs float64, totalTime time.Duration, err error) {
	req := StreamCompletionRequest{
		Model:       modelID,
		Prompt:      prompt,
		MaxTokens:   maxTokens,
		Temperature: 0.7,
		Stream:      true,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return 0, 0, err
	}

	httpReq, err := http.NewRequest("POST", endpoint+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 60 * time.Second}

	startTime := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read until we get the first data line
	reader := bufio.NewReader(resp.Body)
	var firstTokenTime time.Time

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, 0, err
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)

			if data == "[DONE]" {
				break
			}

			// First data line received - record TTFT
			if firstTokenTime.IsZero() {
				firstTokenTime = time.Now()
			}
		}
	}

	totalTime = time.Since(startTime)

	if firstTokenTime.IsZero() {
		return 0, totalTime, fmt.Errorf("no tokens received")
	}

	ttftMs = float64(firstTokenTime.Sub(startTime).Microseconds()) / 1000.0
	return ttftMs, totalTime, nil
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted) - 1) * p / 100
	return sorted[idx]
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func stddev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	m := mean(values)
	var sumSq float64
	for _, v := range values {
		diff := v - m
		sumSq += diff * diff
	}
	return float64(int(100*sqrt(sumSq/float64(len(values)-1)))) / 100
}

func sqrt(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x == 0 {
		return 0
	}
	z := x / 2
	for i := 0; i < 20; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}
