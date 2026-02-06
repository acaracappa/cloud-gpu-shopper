// Package benchmark provides infrastructure for GPU/LLM performance benchmarking.
// It enables comparing model performance across different hardware configurations
// to help consumers choose appropriate resources for their workloads.
package benchmark

import (
	"time"
)

// BenchmarkResult represents the results of a single benchmark run.
type BenchmarkResult struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`

	// Hardware configuration
	Hardware HardwareInfo `json:"hardware"`

	// Model configuration
	Model ModelInfo `json:"model"`

	// Test configuration
	TestConfig TestConfig `json:"test_config"`

	// Results
	Results PerformanceResults `json:"results"`

	// GPU statistics during the test
	GPUStats GPUStats `json:"gpu_stats"`

	// Provider information
	Provider     string  `json:"provider"`
	Location     string  `json:"location"`
	PricePerHour float64 `json:"price_per_hour"`
}

// HardwareInfo describes the hardware used for the benchmark.
type HardwareInfo struct {
	GPUName       string `json:"gpu_name"`
	GPUMemoryMiB  int    `json:"gpu_memory_mib"`
	GPUCount      int    `json:"gpu_count"`
	DriverVersion string `json:"driver_version"`
	CUDAVersion   string `json:"cuda_version"`
	CPUModel      string `json:"cpu_model"`
	CPUCores      int    `json:"cpu_cores"`
	RAMGiB        int    `json:"ram_gib"`
}

// ModelInfo describes the model being benchmarked.
type ModelInfo struct {
	Name           string  `json:"name"`            // e.g., "deepseek-r1:32b"
	Family         string  `json:"family"`          // e.g., "deepseek-r1"
	ParameterCount string  `json:"parameter_count"` // e.g., "32B"
	Quantization   string  `json:"quantization"`    // e.g., "Q4_K_M", "FP16"
	SizeGB         float64 `json:"size_gb"`         // Model size on disk
	Runtime        string  `json:"runtime"`         // e.g., "ollama", "vllm", "tgi"
	RuntimeVersion string  `json:"runtime_version"`
}

// TestConfig describes the benchmark test configuration.
type TestConfig struct {
	DurationMinutes int      `json:"duration_minutes"`
	MaxTokens       int      `json:"max_tokens"`      // Max tokens per request
	PromptTypes     []string `json:"prompt_types"`    // Types of prompts used
	ConcurrentReqs  int      `json:"concurrent_reqs"` // Number of concurrent requests
	WarmupRequests  int      `json:"warmup_requests"` // Requests before measuring
}

// PerformanceResults contains the measured performance metrics.
type PerformanceResults struct {
	TotalRequests     int     `json:"total_requests"`
	TotalTokens       int     `json:"total_tokens"`
	TotalPromptTokens int     `json:"total_prompt_tokens"`
	TotalErrors       int     `json:"total_errors"`
	DurationSeconds   float64 `json:"duration_seconds"`

	// Throughput metrics
	AvgTokensPerSecond float64 `json:"avg_tokens_per_second"`
	MinTokensPerSecond float64 `json:"min_tokens_per_second"`
	MaxTokensPerSecond float64 `json:"max_tokens_per_second"`
	P50TokensPerSecond float64 `json:"p50_tokens_per_second"`
	P95TokensPerSecond float64 `json:"p95_tokens_per_second"`
	P99TokensPerSecond float64 `json:"p99_tokens_per_second"`

	// Latency metrics (in milliseconds)
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	MinLatencyMs float64 `json:"min_latency_ms"`
	MaxLatencyMs float64 `json:"max_latency_ms"`
	P50LatencyMs float64 `json:"p50_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms"`

	// Request metrics
	RequestsPerMinute   float64 `json:"requests_per_minute"`
	AvgTokensPerRequest float64 `json:"avg_tokens_per_request"`
	ErrorRate           float64 `json:"error_rate"`

	// Time to First Token (TTFT) - important for interactive use
	AvgTTFTMs float64 `json:"avg_ttft_ms"`
	P50TTFTMs float64 `json:"p50_ttft_ms"`
	P95TTFTMs float64 `json:"p95_ttft_ms"`
}

// GPUStats contains GPU utilization statistics during the benchmark.
type GPUStats struct {
	AvgUtilizationPct float64 `json:"avg_utilization_pct"`
	MaxUtilizationPct float64 `json:"max_utilization_pct"`
	AvgMemoryUsedMiB  int     `json:"avg_memory_used_mib"`
	MaxMemoryUsedMiB  int     `json:"max_memory_used_mib"`
	AvgTemperatureC   float64 `json:"avg_temperature_c"`
	MaxTemperatureC   float64 `json:"max_temperature_c"`
	AvgPowerDrawW     float64 `json:"avg_power_draw_w"`
	MaxPowerDrawW     float64 `json:"max_power_draw_w"`
}

// CostAnalysis provides cost-efficiency metrics.
type CostAnalysis struct {
	TokensPerDollar      float64 `json:"tokens_per_dollar"`
	CostPerMillionTokens float64 `json:"cost_per_million_tokens"`
	CostPerHour          float64 `json:"cost_per_hour"`
	EstimatedMonthly     float64 `json:"estimated_monthly_24x7"`
}

// BenchmarkComparison compares benchmark results across configurations.
type BenchmarkComparison struct {
	Baseline    *BenchmarkResult   `json:"baseline"`
	Comparisons []*ComparisonEntry `json:"comparisons"`
}

// ComparisonEntry represents a single comparison against the baseline.
type ComparisonEntry struct {
	Result           *BenchmarkResult `json:"result"`
	SpeedupFactor    float64          `json:"speedup_factor"`    // vs baseline
	CostEfficiency   float64          `json:"cost_efficiency"`   // tokens/$ vs baseline
	MemoryEfficiency float64          `json:"memory_efficiency"` // tokens/GB vs baseline
}

// HardwareRecommendation suggests hardware for a workload.
type HardwareRecommendation struct {
	Model           string   `json:"model"`
	MinVRAMGiB      int      `json:"min_vram_gib"`
	RecommendedGPUs []string `json:"recommended_gpus"`
	ExpectedTPS     float64  `json:"expected_tps"`
	EstimatedCost   float64  `json:"estimated_cost_per_hour"`
	Notes           string   `json:"notes"`
}
