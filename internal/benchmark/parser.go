package benchmark

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RequestResult represents a single request from the benchmark log.
type RequestResult struct {
	Timestamp     int64   `json:"t"`
	RequestNum    int     `json:"n"`
	Tokens        int     `json:"tok"`
	TokensPerSec  float64 `json:"tps"`
	DurationSec   float64 `json:"dur,omitempty"`
	Error         bool    `json:"err,omitempty"`
	ErrorMsg      string  `json:"error_msg,omitempty"`
}

// GPUSample represents a GPU metrics sample.
type GPUSample struct {
	Timestamp   int64
	Utilization float64
	MemoryUsed  int
	MemoryTotal int
	Temperature float64
	PowerDraw   float64
}

// ParseResultsJSONL parses a JSONL file of request results.
func ParseResultsJSONL(path string) ([]RequestResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var results []RequestResult
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var r RequestResult
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue // Skip malformed lines
		}
		results = append(results, r)
	}
	return results, scanner.Err()
}

// ParseGPUCSV parses a CSV file of GPU metrics.
func ParseGPUCSV(path string) ([]GPUSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	// Skip header
	if _, err := reader.Read(); err != nil {
		return nil, err
	}

	var samples []GPUSample
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) < 6 {
			continue
		}

		ts, _ := strconv.ParseInt(record[0], 10, 64)
		util, _ := strconv.ParseFloat(record[1], 64)
		memUsed, _ := strconv.Atoi(record[2])
		memTotal, _ := strconv.Atoi(record[3])
		temp, _ := strconv.ParseFloat(record[4], 64)
		power, _ := strconv.ParseFloat(record[5], 64)

		samples = append(samples, GPUSample{
			Timestamp:   ts,
			Utilization: util,
			MemoryUsed:  memUsed,
			MemoryTotal: memTotal,
			Temperature: temp,
			PowerDraw:   power,
		})
	}
	return samples, nil
}

// AnalyzeResults computes performance statistics from request results.
func AnalyzeResults(results []RequestResult) PerformanceResults {
	if len(results) == 0 {
		return PerformanceResults{}
	}

	var totalTokens, totalErrors int
	var tpsValues []float64
	var durations []float64

	for _, r := range results {
		if r.Error {
			totalErrors++
			continue
		}
		totalTokens += r.Tokens
		if r.TokensPerSec > 0 {
			tpsValues = append(tpsValues, r.TokensPerSec)
		}
		if r.DurationSec > 0 {
			durations = append(durations, r.DurationSec*1000) // Convert to ms
		}
	}

	// Calculate duration from timestamps
	var durationSeconds float64
	if len(results) >= 2 {
		durationSeconds = float64(results[len(results)-1].Timestamp - results[0].Timestamp)
	}

	pr := PerformanceResults{
		TotalRequests:   len(results),
		TotalTokens:     totalTokens,
		TotalErrors:     totalErrors,
		DurationSeconds: durationSeconds,
		ErrorRate:       float64(totalErrors) / float64(len(results)),
	}

	if durationSeconds > 0 {
		pr.AvgTokensPerSecond = float64(totalTokens) / durationSeconds
		pr.RequestsPerMinute = float64(len(results)) * 60 / durationSeconds
	}

	if len(results) > 0 {
		pr.AvgTokensPerRequest = float64(totalTokens) / float64(len(results)-totalErrors)
	}

	// Calculate TPS percentiles
	if len(tpsValues) > 0 {
		sort.Float64s(tpsValues)
		pr.MinTokensPerSecond = tpsValues[0]
		pr.MaxTokensPerSecond = tpsValues[len(tpsValues)-1]
		pr.P50TokensPerSecond = percentile(tpsValues, 50)
		pr.P95TokensPerSecond = percentile(tpsValues, 95)
		pr.P99TokensPerSecond = percentile(tpsValues, 99)

		var sum float64
		for _, v := range tpsValues {
			sum += v
		}
		pr.AvgTokensPerSecond = sum / float64(len(tpsValues))
	}

	// Calculate latency percentiles
	if len(durations) > 0 {
		sort.Float64s(durations)
		pr.MinLatencyMs = durations[0]
		pr.MaxLatencyMs = durations[len(durations)-1]
		pr.P50LatencyMs = percentile(durations, 50)
		pr.P95LatencyMs = percentile(durations, 95)
		pr.P99LatencyMs = percentile(durations, 99)

		var sum float64
		for _, v := range durations {
			sum += v
		}
		pr.AvgLatencyMs = sum / float64(len(durations))
	}

	return pr
}

// AnalyzeGPUStats computes GPU statistics from samples.
func AnalyzeGPUStats(samples []GPUSample) GPUStats {
	if len(samples) == 0 {
		return GPUStats{}
	}

	var sumUtil, sumTemp, sumPower float64
	var maxUtil, maxTemp, maxPower float64
	var maxMem int

	for _, s := range samples {
		sumUtil += s.Utilization
		sumTemp += s.Temperature
		sumPower += s.PowerDraw

		if s.Utilization > maxUtil {
			maxUtil = s.Utilization
		}
		if s.Temperature > maxTemp {
			maxTemp = s.Temperature
		}
		if s.PowerDraw > maxPower {
			maxPower = s.PowerDraw
		}
		if s.MemoryUsed > maxMem {
			maxMem = s.MemoryUsed
		}
	}

	n := float64(len(samples))
	return GPUStats{
		AvgUtilizationPct: sumUtil / n,
		MaxUtilizationPct: maxUtil,
		AvgTemperatureC:   sumTemp / n,
		MaxTemperatureC:   maxTemp,
		AvgPowerDrawW:     sumPower / n,
		MaxPowerDrawW:     maxPower,
		MaxMemoryUsedMiB:  maxMem,
	}
}

// percentile calculates the p-th percentile of a sorted slice.
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted) - 1) * p / 100
	return sorted[idx]
}

// BuildBenchmarkResult creates a complete BenchmarkResult from parsed data.
func BuildBenchmarkResult(
	results []RequestResult,
	gpuSamples []GPUSample,
	hardware HardwareInfo,
	model ModelInfo,
	testConfig TestConfig,
	provider, location string,
	pricePerHour float64,
) *BenchmarkResult {
	return &BenchmarkResult{
		Timestamp:    time.Now(),
		Hardware:     hardware,
		Model:        model,
		TestConfig:   testConfig,
		Results:      AnalyzeResults(results),
		GPUStats:     AnalyzeGPUStats(gpuSamples),
		Provider:     provider,
		Location:     location,
		PricePerHour: pricePerHour,
	}
}

// CalculateCostAnalysis computes cost metrics from benchmark results.
func CalculateCostAnalysis(result *BenchmarkResult) CostAnalysis {
	if result.PricePerHour <= 0 || result.Results.AvgTokensPerSecond <= 0 {
		return CostAnalysis{}
	}

	tokensPerHour := result.Results.AvgTokensPerSecond * 3600
	tokensPerDollar := tokensPerHour / result.PricePerHour

	return CostAnalysis{
		TokensPerDollar:      tokensPerDollar,
		CostPerMillionTokens: 1000000 / tokensPerDollar,
		CostPerHour:          result.PricePerHour,
		EstimatedMonthly:     result.PricePerHour * 24 * 30,
	}
}

// BenchmarkMetadata represents the metadata.json file from benchmark runs.
type BenchmarkMetadata struct {
	BenchmarkVersion string    `json:"benchmark_version"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	Hostname         string    `json:"hostname"`
	Hardware         struct {
		GPUName       string `json:"gpu_name"`
		GPUMemoryMiB  int    `json:"gpu_memory_mib"`
		GPUCount      int    `json:"gpu_count"`
		DriverVersion string `json:"driver_version"`
		CUDAVersion   string `json:"cuda_version"`
		CPUModel      string `json:"cpu_model"`
		CPUCores      int    `json:"cpu_cores"`
		RAMGiB        int    `json:"ram_gib"`
	} `json:"hardware"`
	Model struct {
		Name           string `json:"name"`
		Runtime        string `json:"runtime"`
		RuntimeVersion string `json:"runtime_version"`
		Size           string `json:"size"`
	} `json:"model"`
	TestConfig struct {
		DurationSeconds int      `json:"duration_seconds"`
		MaxTokens       int      `json:"max_tokens"`
		Prompts         []string `json:"prompts"`
	} `json:"test_config"`
	Summary struct {
		TotalRequests   int `json:"total_requests"`
		TotalTokens     int `json:"total_tokens"`
		DurationSeconds int `json:"duration_seconds"`
	} `json:"summary,omitempty"`
}

// ParseMetadata parses a metadata.json file from a benchmark run.
func ParseMetadata(path string) (*BenchmarkMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta BenchmarkMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// LoadBenchmarkFromDirectory loads a complete benchmark from a directory
// containing metadata.json, results.jsonl, and gpu.csv files.
func LoadBenchmarkFromDirectory(dir string, provider, location string, pricePerHour float64) (*BenchmarkResult, error) {
	// Parse metadata
	metaPath := filepath.Join(dir, "metadata.json")
	meta, err := ParseMetadata(metaPath)
	if err != nil {
		return nil, fmt.Errorf("parsing metadata: %w", err)
	}

	// Parse results
	resultsPath := filepath.Join(dir, "results.jsonl")
	results, err := ParseResultsJSONL(resultsPath)
	if err != nil {
		return nil, fmt.Errorf("parsing results: %w", err)
	}

	// Parse GPU samples
	gpuPath := filepath.Join(dir, "gpu.csv")
	gpuSamples, err := ParseGPUCSV(gpuPath)
	if err != nil {
		return nil, fmt.Errorf("parsing GPU stats: %w", err)
	}

	// Build hardware info
	hardware := HardwareInfo{
		GPUName:       meta.Hardware.GPUName,
		GPUMemoryMiB:  meta.Hardware.GPUMemoryMiB,
		GPUCount:      meta.Hardware.GPUCount,
		DriverVersion: meta.Hardware.DriverVersion,
		CUDAVersion:   meta.Hardware.CUDAVersion,
		CPUModel:      meta.Hardware.CPUModel,
		CPUCores:      meta.Hardware.CPUCores,
		RAMGiB:        meta.Hardware.RAMGiB,
	}

	// Build model info
	model := ModelInfo{
		Name:           meta.Model.Name,
		Runtime:        meta.Model.Runtime,
		RuntimeVersion: meta.Model.RuntimeVersion,
	}

	// Parse model size (e.g., "19GB" -> 19.0)
	if meta.Model.Size != "" && meta.Model.Size != "unknown" {
		sizeStr := strings.TrimSuffix(strings.ToUpper(meta.Model.Size), "GB")
		sizeStr = strings.TrimSuffix(sizeStr, "B")
		if size, err := strconv.ParseFloat(sizeStr, 64); err == nil {
			model.SizeGB = size
		}
	}

	// Extract family and parameter count from model name (e.g., "deepseek-r1:32b")
	if parts := strings.Split(meta.Model.Name, ":"); len(parts) >= 2 {
		model.Family = parts[0]
		model.ParameterCount = strings.ToUpper(parts[1])
	} else {
		model.Family = meta.Model.Name
	}

	// Build test config
	testConfig := TestConfig{
		DurationMinutes: meta.TestConfig.DurationSeconds / 60,
		MaxTokens:       meta.TestConfig.MaxTokens,
		PromptTypes:     meta.TestConfig.Prompts,
		ConcurrentReqs:  1, // Current script does sequential requests
	}

	return BuildBenchmarkResult(results, gpuSamples, hardware, model, testConfig, provider, location, pricePerHour), nil
}

// FormatBenchmarkSummary creates a human-readable summary of benchmark results.
func FormatBenchmarkSummary(result *BenchmarkResult) string {
	cost := CalculateCostAnalysis(result)

	return fmt.Sprintf(`
═══════════════════════════════════════════════════════════════
                    BENCHMARK RESULTS
═══════════════════════════════════════════════════════════════

Hardware
  GPU:              %s (%d MiB)
  Driver:           %s
  CUDA:             %s

Model
  Name:             %s
  Size:             %.1f GB
  Runtime:          %s

Performance
  Avg Tokens/sec:   %.2f
  P50 Tokens/sec:   %.2f
  P95 Tokens/sec:   %.2f
  Requests:         %d
  Total Tokens:     %d
  Errors:           %d (%.1f%%)
  Duration:         %.1f minutes

GPU Utilization
  Avg Utilization:  %.1f%%
  Avg Temperature:  %.1f°C
  Max Temperature:  %.1f°C
  Avg Power:        %.1f W
  Peak VRAM:        %d MiB

Cost Analysis
  Price/Hour:       $%.3f
  Tokens/Dollar:    %.0f
  $/Million Tokens: $%.4f
  Monthly (24x7):   $%.2f

═══════════════════════════════════════════════════════════════
`,
		result.Hardware.GPUName, result.Hardware.GPUMemoryMiB,
		result.Hardware.DriverVersion, result.Hardware.CUDAVersion,
		result.Model.Name, result.Model.SizeGB, result.Model.Runtime,
		result.Results.AvgTokensPerSecond, result.Results.P50TokensPerSecond, result.Results.P95TokensPerSecond,
		result.Results.TotalRequests, result.Results.TotalTokens, result.Results.TotalErrors,
		result.Results.ErrorRate*100, result.Results.DurationSeconds/60,
		result.GPUStats.AvgUtilizationPct, result.GPUStats.AvgTemperatureC,
		result.GPUStats.MaxTemperatureC, result.GPUStats.AvgPowerDrawW,
		result.GPUStats.MaxMemoryUsedMiB,
		cost.CostPerHour, cost.TokensPerDollar, cost.CostPerMillionTokens, cost.EstimatedMonthly,
	)
}
