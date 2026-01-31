package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	endpoint string
	output   string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "benchmark-client",
	Short: "Benchmark client for vLLM inference testing",
	Long: `A benchmark client that runs performance tests against a vLLM endpoint.

This client is deployed to benchmark nodes and executed via Ansible.
It measures:
- Throughput (tokens per second)
- Latency (time to first token)
- Concurrency (max parallel requests)

Results are written to JSON files for collection.`,
}

var throughputCmd = &cobra.Command{
	Use:   "throughput",
	Short: "Run throughput benchmark",
	Long: `Measure maximum token generation throughput.

Sends multiple requests sequentially and measures total tokens
generated per second across all requests.`,
	RunE: runThroughput,
}

var latencyCmd = &cobra.Command{
	Use:   "latency",
	Short: "Run latency benchmark",
	Long: `Measure time to first token (TTFT) latency.

Sends requests one at a time and measures the time from request
start to receiving the first token of the response.`,
	RunE: runLatency,
}

var concurrencyCmd = &cobra.Command{
	Use:   "concurrency",
	Short: "Run concurrency benchmark",
	Long: `Find maximum sustainable concurrent requests.

Gradually increases the number of parallel requests until
throughput degrades or errors occur, identifying the optimal
concurrency level.`,
	RunE: runConcurrency,
}

var (
	throughputRequests int
	latencyRequests    int
	maxConcurrent      int
	promptTokens       int
	maxTokens          int
	warmupRequests     int
)

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&endpoint, "endpoint", "http://localhost:8000", "vLLM endpoint URL")
	rootCmd.PersistentFlags().StringVar(&output, "output", "", "Output file for results (default: stdout)")

	// Throughput flags
	throughputCmd.Flags().IntVar(&throughputRequests, "requests", 100, "Number of requests to send")
	throughputCmd.Flags().IntVar(&promptTokens, "prompt-tokens", 128, "Approximate prompt token count")
	throughputCmd.Flags().IntVar(&maxTokens, "max-tokens", 256, "Maximum tokens to generate")
	throughputCmd.Flags().IntVar(&warmupRequests, "warmup", 5, "Warmup requests before measurement")

	// Latency flags
	latencyCmd.Flags().IntVar(&latencyRequests, "requests", 50, "Number of requests for latency measurement")

	// Concurrency flags
	concurrencyCmd.Flags().IntVar(&maxConcurrent, "max-concurrent", 32, "Maximum concurrent requests to test")

	rootCmd.AddCommand(throughputCmd)
	rootCmd.AddCommand(latencyCmd)
	rootCmd.AddCommand(concurrencyCmd)
}
