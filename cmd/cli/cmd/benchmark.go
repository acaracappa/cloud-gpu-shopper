package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	benchModel  string
	benchGPU    string
	benchLimit  int
	benchMinTPS float64
)

// BenchmarkResult represents a benchmark from the API
type BenchmarkResult struct {
	ID        string       `json:"id"`
	Timestamp string       `json:"timestamp"`
	Hardware  HardwareInfo `json:"hardware"`
	Model     ModelInfo    `json:"model"`
	Results   PerfResults  `json:"results"`
	GPUStats  GPUStats     `json:"gpu_stats"`
	Provider  string       `json:"provider"`
	Location  string       `json:"location"`
	Price     float64      `json:"price_per_hour"`
}

type HardwareInfo struct {
	GPUName      string `json:"gpu_name"`
	GPUMemoryMiB int    `json:"gpu_memory_mib"`
	GPUCount     int    `json:"gpu_count"`
	CUDA         string `json:"cuda_version"`
}

type ModelInfo struct {
	Name    string  `json:"name"`
	Family  string  `json:"family"`
	Quant   string  `json:"quantization"`
	SizeGB  float64 `json:"size_gb"`
	Runtime string  `json:"runtime"`
}

type PerfResults struct {
	TotalRequests int     `json:"total_requests"`
	TotalTokens   int     `json:"total_tokens"`
	TotalErrors   int     `json:"total_errors"`
	DurationSec   float64 `json:"duration_seconds"`
	AvgTPS        float64 `json:"avg_tokens_per_second"`
	MinTPS        float64 `json:"min_tokens_per_second"`
	MaxTPS        float64 `json:"max_tokens_per_second"`
	P50TPS        float64 `json:"p50_tokens_per_second"`
	P95TPS        float64 `json:"p95_tokens_per_second"`
	AvgLatency    float64 `json:"avg_latency_ms"`
	P95Latency    float64 `json:"p95_latency_ms"`
	ErrorRate     float64 `json:"error_rate"`
}

type GPUStats struct {
	AvgUtil   float64 `json:"avg_utilization_pct"`
	MaxUtil   float64 `json:"max_utilization_pct"`
	AvgTemp   float64 `json:"avg_temperature_c"`
	MaxTemp   float64 `json:"max_temperature_c"`
	AvgPower  float64 `json:"avg_power_draw_w"`
	MaxMemMiB int     `json:"max_memory_used_mib"`
}

type CostAnalysis struct {
	TokensPerDollar      float64 `json:"tokens_per_dollar"`
	CostPerMillionTokens float64 `json:"cost_per_million_tokens"`
	CostPerHour          float64 `json:"cost_per_hour"`
	EstimatedMonthly     float64 `json:"estimated_monthly_24x7"`
}

type BenchmarkResponse struct {
	Benchmarks []*BenchmarkResult `json:"benchmarks"`
	Count      int                `json:"count"`
}

type SingleBenchmarkResponse struct {
	Benchmark    *BenchmarkResult `json:"benchmark"`
	CostAnalysis *CostAnalysis    `json:"cost_analysis"`
}

type RecommendationResponse struct {
	Model           string           `json:"model"`
	Recommendations []Recommendation `json:"recommendations"`
	Count           int              `json:"count"`
}

type Recommendation struct {
	Model           string   `json:"model"`
	MinVRAMGiB      int      `json:"min_vram_gib"`
	RecommendedGPUs []string `json:"recommended_gpus"`
	ExpectedTPS     float64  `json:"expected_tps"`
	EstimatedCost   float64  `json:"estimated_cost_per_hour"`
	Notes           string   `json:"notes"`
}

var benchmarkCmd = &cobra.Command{
	Use:   "benchmarks",
	Short: "View benchmark results",
	Long: `View GPU/model benchmark results.

Examples:
  gpu-shopper benchmarks                      # List recent benchmarks
  gpu-shopper benchmarks --model deepseek-r1  # Filter by model
  gpu-shopper benchmarks --gpu 4090           # Filter by GPU
  gpu-shopper benchmarks best --model llama   # Best benchmark for model
  gpu-shopper benchmarks recommend --model x  # Hardware recommendations`,
	RunE: runBenchmarks,
}

var benchmarkBestCmd = &cobra.Command{
	Use:   "best",
	Short: "Get best performing benchmark for a model",
	RunE:  runBenchmarkBest,
}

var benchmarkCheapestCmd = &cobra.Command{
	Use:   "cheapest",
	Short: "Get most cost-effective benchmark for a model",
	RunE:  runBenchmarkCheapest,
}

var benchmarkRecommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Get hardware recommendations for a model",
	RunE:  runBenchmarkRecommend,
}

var benchmarkCompareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare benchmarks for a model across hardware",
	RunE:  runBenchmarkCompare,
}

func init() {
	rootCmd.AddCommand(benchmarkCmd)
	benchmarkCmd.AddCommand(benchmarkBestCmd)
	benchmarkCmd.AddCommand(benchmarkCheapestCmd)
	benchmarkCmd.AddCommand(benchmarkRecommendCmd)
	benchmarkCmd.AddCommand(benchmarkCompareCmd)

	// List flags
	benchmarkCmd.Flags().StringVarP(&benchModel, "model", "m", "", "Filter by model name")
	benchmarkCmd.Flags().StringVarP(&benchGPU, "gpu", "g", "", "Filter by GPU name")
	benchmarkCmd.Flags().IntVarP(&benchLimit, "limit", "l", 20, "Maximum results to return")

	// Best/cheapest flags
	benchmarkBestCmd.Flags().StringVarP(&benchModel, "model", "m", "", "Model name (required)")
	benchmarkBestCmd.MarkFlagRequired("model")

	benchmarkCheapestCmd.Flags().StringVarP(&benchModel, "model", "m", "", "Model name (required)")
	benchmarkCheapestCmd.Flags().Float64Var(&benchMinTPS, "min-tps", 0, "Minimum tokens/sec threshold")
	benchmarkCheapestCmd.MarkFlagRequired("model")

	// Recommend flags
	benchmarkRecommendCmd.Flags().StringVarP(&benchModel, "model", "m", "", "Model name (required)")
	benchmarkRecommendCmd.MarkFlagRequired("model")

	// Compare flags
	benchmarkCompareCmd.Flags().StringVarP(&benchModel, "model", "m", "", "Model name (required)")
	benchmarkCompareCmd.MarkFlagRequired("model")
}

func runBenchmarks(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	if benchModel != "" {
		params.Set("model", benchModel)
	}
	if benchGPU != "" {
		params.Set("gpu", benchGPU)
	}
	params.Set("limit", fmt.Sprintf("%d", benchLimit))

	reqURL := fmt.Sprintf("%s/api/v1/benchmarks", serverURL)
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	var result BenchmarkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printBenchmarkList(result.Benchmarks)
	return nil
}

func runBenchmarkBest(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	params.Set("model", benchModel)

	reqURL := fmt.Sprintf("%s/api/v1/benchmarks/best?%s", serverURL, params.Encode())

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	var result SingleBenchmarkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printBenchmarkDetail(result.Benchmark, result.CostAnalysis)
	return nil
}

func runBenchmarkCheapest(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	params.Set("model", benchModel)
	if benchMinTPS > 0 {
		params.Set("min_tps", fmt.Sprintf("%.2f", benchMinTPS))
	}

	reqURL := fmt.Sprintf("%s/api/v1/benchmarks/cheapest?%s", serverURL, params.Encode())

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	var result SingleBenchmarkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printBenchmarkDetail(result.Benchmark, result.CostAnalysis)
	return nil
}

func runBenchmarkRecommend(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	params.Set("model", benchModel)

	reqURL := fmt.Sprintf("%s/api/v1/benchmarks/recommendations?%s", serverURL, params.Encode())

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	var result RecommendationResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	printRecommendations(result.Model, result.Recommendations)
	return nil
}

func runBenchmarkCompare(cmd *cobra.Command, args []string) error {
	params := url.Values{}
	params.Set("model", benchModel)

	reqURL := fmt.Sprintf("%s/api/v1/benchmarks/compare?%s", serverURL, params.Encode())

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(body))
	}

	if outputFormat == "json" {
		body, _ := io.ReadAll(resp.Body)
		fmt.Println(string(body))
		return nil
	}

	// For table output, decode and format
	var comparison map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&comparison); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Println("Benchmark Comparison")
	fmt.Println("====================")
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(comparison)
}

func printBenchmarkList(benchmarks []*BenchmarkResult) {
	if len(benchmarks) == 0 {
		fmt.Println("No benchmarks found")
		return
	}

	fmt.Printf("Found %d benchmarks\n\n", len(benchmarks))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL\tGPU\tAVG TPS\tP95 TPS\t$/HR\tPROVIDER")
	fmt.Fprintln(w, "-----\t---\t-------\t-------\t----\t--------")

	for _, b := range benchmarks {
		fmt.Fprintf(w, "%s\t%s\t%.1f\t%.1f\t$%.2f\t%s\n",
			b.Model.Name,
			b.Hardware.GPUName,
			b.Results.AvgTPS,
			b.Results.P95TPS,
			b.Price,
			b.Provider,
		)
	}
	w.Flush()
}

func printBenchmarkDetail(b *BenchmarkResult, cost *CostAnalysis) {
	if b == nil {
		fmt.Println("No benchmark found")
		return
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("                    BENCHMARK RESULT")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	fmt.Println("Hardware")
	fmt.Printf("  GPU:              %s (%d MiB)\n", b.Hardware.GPUName, b.Hardware.GPUMemoryMiB)
	fmt.Printf("  Count:            %d\n", b.Hardware.GPUCount)
	fmt.Printf("  CUDA:             %s\n", b.Hardware.CUDA)
	fmt.Println()

	fmt.Println("Model")
	fmt.Printf("  Name:             %s\n", b.Model.Name)
	if b.Model.Family != "" {
		fmt.Printf("  Family:           %s\n", b.Model.Family)
	}
	if b.Model.Quant != "" {
		fmt.Printf("  Quantization:     %s\n", b.Model.Quant)
	}
	fmt.Printf("  Size:             %.1f GB\n", b.Model.SizeGB)
	fmt.Printf("  Runtime:          %s\n", b.Model.Runtime)
	fmt.Println()

	fmt.Println("Performance")
	fmt.Printf("  Avg Tokens/sec:   %.2f\n", b.Results.AvgTPS)
	fmt.Printf("  P50 Tokens/sec:   %.2f\n", b.Results.P50TPS)
	fmt.Printf("  P95 Tokens/sec:   %.2f\n", b.Results.P95TPS)
	fmt.Printf("  Total Requests:   %d\n", b.Results.TotalRequests)
	fmt.Printf("  Total Tokens:     %d\n", b.Results.TotalTokens)
	fmt.Printf("  Errors:           %d (%.1f%%)\n", b.Results.TotalErrors, b.Results.ErrorRate*100)
	fmt.Printf("  Duration:         %.1f minutes\n", b.Results.DurationSec/60)
	fmt.Println()

	fmt.Println("GPU Utilization")
	fmt.Printf("  Avg Utilization:  %.1f%%\n", b.GPUStats.AvgUtil)
	fmt.Printf("  Max Utilization:  %.1f%%\n", b.GPUStats.MaxUtil)
	fmt.Printf("  Avg Temperature:  %.1f°C\n", b.GPUStats.AvgTemp)
	fmt.Printf("  Max Temperature:  %.1f°C\n", b.GPUStats.MaxTemp)
	fmt.Printf("  Avg Power:        %.1f W\n", b.GPUStats.AvgPower)
	fmt.Printf("  Peak VRAM:        %d MiB\n", b.GPUStats.MaxMemMiB)
	fmt.Println()

	if cost != nil {
		fmt.Println("Cost Analysis")
		fmt.Printf("  Price/Hour:       $%.3f\n", cost.CostPerHour)
		fmt.Printf("  Tokens/Dollar:    %.0f\n", cost.TokensPerDollar)
		fmt.Printf("  $/Million Tokens: $%.4f\n", cost.CostPerMillionTokens)
		fmt.Printf("  Monthly (24x7):   $%.2f\n", cost.EstimatedMonthly)
		fmt.Println()
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
}

func printRecommendations(model string, recs []Recommendation) {
	fmt.Printf("Hardware Recommendations for %s\n", model)
	fmt.Println("========================================")
	fmt.Println()

	if len(recs) == 0 {
		fmt.Println("No recommendations available (no benchmarks for this model)")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "GPU\tVRAM\tEXP TPS\t$/HR\tNOTES")
	fmt.Fprintln(w, "---\t----\t-------\t----\t-----")

	for _, r := range recs {
		gpus := ""
		if len(r.RecommendedGPUs) > 0 {
			gpus = r.RecommendedGPUs[0]
		}
		fmt.Fprintf(w, "%s\t%dGB\t%.1f\t$%.2f\t%s\n",
			gpus,
			r.MinVRAMGiB,
			r.ExpectedTPS,
			r.EstimatedCost,
			r.Notes,
		)
	}
	w.Flush()
}
