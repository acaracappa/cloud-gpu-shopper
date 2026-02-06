package benchmark

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseResultsJSONL(t *testing.T) {
	// Create temp file with test data
	tmpDir := t.TempDir()
	resultsPath := filepath.Join(tmpDir, "results.jsonl")

	testData := `{"t":1770345866,"n":0,"tok":256,"tps":44.29}
{"t":1770345872,"n":1,"tok":256,"tps":44.21}
{"t":1770345878,"n":2,"tok":256,"tps":44.21}
{"t":1770345885,"n":3,"err":true,"error_msg":"timeout"}
{"t":1770345891,"n":4,"tok":128,"tps":42.50,"dur":3.0}
`
	if err := os.WriteFile(resultsPath, []byte(testData), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	results, err := ParseResultsJSONL(resultsPath)
	if err != nil {
		t.Fatalf("ParseResultsJSONL failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}

	// Verify first result
	if results[0].Tokens != 256 {
		t.Errorf("expected 256 tokens, got %d", results[0].Tokens)
	}
	if results[0].TokensPerSec != 44.29 {
		t.Errorf("expected 44.29 tps, got %f", results[0].TokensPerSec)
	}

	// Verify error result
	if !results[3].Error {
		t.Error("expected result 3 to be an error")
	}
}

func TestParseGPUCSV(t *testing.T) {
	tmpDir := t.TempDir()
	gpuPath := filepath.Join(tmpDir, "gpu.csv")

	testData := `ts,util,mem_used,mem_total,temp,power
1770345855,0,1,24564,33,19.71
1770345860,95,20366,24564,40,105.73
1770345865,96,20366,24564,44,354.02
`
	if err := os.WriteFile(gpuPath, []byte(testData), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	samples, err := ParseGPUCSV(gpuPath)
	if err != nil {
		t.Fatalf("ParseGPUCSV failed: %v", err)
	}

	if len(samples) != 3 {
		t.Errorf("expected 3 samples, got %d", len(samples))
	}

	// Verify first sample
	if samples[0].Utilization != 0 {
		t.Errorf("expected 0 utilization, got %f", samples[0].Utilization)
	}

	// Verify second sample
	if samples[1].Utilization != 95 {
		t.Errorf("expected 95 utilization, got %f", samples[1].Utilization)
	}
	if samples[1].MemoryUsed != 20366 {
		t.Errorf("expected 20366 memory, got %d", samples[1].MemoryUsed)
	}
	if samples[1].PowerDraw != 105.73 {
		t.Errorf("expected 105.73 power, got %f", samples[1].PowerDraw)
	}
}

func TestAnalyzeResults(t *testing.T) {
	results := []RequestResult{
		{Timestamp: 1000, Tokens: 100, TokensPerSec: 40.0, DurationSec: 2.5},
		{Timestamp: 1005, Tokens: 120, TokensPerSec: 48.0, DurationSec: 2.5},
		{Timestamp: 1010, Tokens: 80, TokensPerSec: 32.0, DurationSec: 2.5},
		{Timestamp: 1015, Error: true, ErrorMsg: "timeout"},
		{Timestamp: 1020, Tokens: 100, TokensPerSec: 44.0, DurationSec: 2.3},
	}

	pr := AnalyzeResults(results)

	if pr.TotalRequests != 5 {
		t.Errorf("expected 5 total requests, got %d", pr.TotalRequests)
	}

	if pr.TotalErrors != 1 {
		t.Errorf("expected 1 error, got %d", pr.TotalErrors)
	}

	expectedTokens := 100 + 120 + 80 + 100
	if pr.TotalTokens != expectedTokens {
		t.Errorf("expected %d tokens, got %d", expectedTokens, pr.TotalTokens)
	}

	// Duration should be last timestamp - first timestamp
	if pr.DurationSeconds != 20 {
		t.Errorf("expected 20 second duration, got %f", pr.DurationSeconds)
	}

	// Error rate should be 1/5 = 0.2
	if pr.ErrorRate != 0.2 {
		t.Errorf("expected 0.2 error rate, got %f", pr.ErrorRate)
	}

	// Min/Max TPS
	if pr.MinTokensPerSecond != 32.0 {
		t.Errorf("expected 32.0 min tps, got %f", pr.MinTokensPerSecond)
	}
	if pr.MaxTokensPerSecond != 48.0 {
		t.Errorf("expected 48.0 max tps, got %f", pr.MaxTokensPerSecond)
	}
}

func TestAnalyzeGPUStats(t *testing.T) {
	samples := []GPUSample{
		{Utilization: 90, Temperature: 50, PowerDraw: 200, MemoryUsed: 10000},
		{Utilization: 95, Temperature: 55, PowerDraw: 250, MemoryUsed: 12000},
		{Utilization: 100, Temperature: 60, PowerDraw: 300, MemoryUsed: 15000},
	}

	stats := AnalyzeGPUStats(samples)

	// Avg utilization = (90+95+100)/3 = 95
	expectedAvgUtil := 95.0
	if stats.AvgUtilizationPct != expectedAvgUtil {
		t.Errorf("expected %f avg utilization, got %f", expectedAvgUtil, stats.AvgUtilizationPct)
	}

	if stats.MaxUtilizationPct != 100 {
		t.Errorf("expected 100 max utilization, got %f", stats.MaxUtilizationPct)
	}

	if stats.MaxTemperatureC != 60 {
		t.Errorf("expected 60 max temp, got %f", stats.MaxTemperatureC)
	}

	if stats.MaxMemoryUsedMiB != 15000 {
		t.Errorf("expected 15000 max memory, got %d", stats.MaxMemoryUsedMiB)
	}
}

func TestCalculateCostAnalysis(t *testing.T) {
	result := &BenchmarkResult{
		Results: PerformanceResults{
			AvgTokensPerSecond: 100, // 100 tok/s = 360,000 tok/hr
		},
		PricePerHour: 0.36, // $0.36/hr
	}

	cost := CalculateCostAnalysis(result)

	// Tokens per hour = 100 * 3600 = 360,000
	// Tokens per dollar = 360,000 / 0.36 = 1,000,000
	if cost.TokensPerDollar != 1000000 {
		t.Errorf("expected 1000000 tokens per dollar, got %f", cost.TokensPerDollar)
	}

	// Cost per million = 1,000,000 / 1,000,000 = $1.00
	if cost.CostPerMillionTokens != 1.0 {
		t.Errorf("expected $1.00 per million tokens, got %f", cost.CostPerMillionTokens)
	}

	// Monthly = 0.36 * 24 * 30 = $259.20
	expectedMonthly := 259.2
	if cost.EstimatedMonthly < expectedMonthly-0.01 || cost.EstimatedMonthly > expectedMonthly+0.01 {
		t.Errorf("expected ~$259.20 monthly, got %f", cost.EstimatedMonthly)
	}
}

func TestParseMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	metaPath := filepath.Join(tmpDir, "metadata.json")

	testData := `{
  "benchmark_version": "1.0",
  "started_at": "2026-02-05T22:00:00Z",
  "hostname": "test-host",
  "hardware": {
    "gpu_name": "NVIDIA GeForce RTX 4090",
    "gpu_memory_mib": 24564,
    "gpu_count": 1,
    "driver_version": "550.127.05",
    "cuda_version": "12.4",
    "cpu_model": "AMD EPYC 7763",
    "cpu_cores": 16,
    "ram_gib": 64
  },
  "model": {
    "name": "deepseek-r1:32b",
    "runtime": "ollama",
    "runtime_version": "0.5.7",
    "size": "19GB"
  },
  "test_config": {
    "duration_seconds": 600,
    "max_tokens": 256,
    "prompts": ["general_knowledge", "coding", "technical", "creative"]
  }
}`
	if err := os.WriteFile(metaPath, []byte(testData), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	meta, err := ParseMetadata(metaPath)
	if err != nil {
		t.Fatalf("ParseMetadata failed: %v", err)
	}

	if meta.BenchmarkVersion != "1.0" {
		t.Errorf("expected version 1.0, got %s", meta.BenchmarkVersion)
	}

	if meta.Hardware.GPUName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("expected RTX 4090, got %s", meta.Hardware.GPUName)
	}

	if meta.Model.Name != "deepseek-r1:32b" {
		t.Errorf("expected deepseek-r1:32b, got %s", meta.Model.Name)
	}

	if meta.TestConfig.DurationSeconds != 600 {
		t.Errorf("expected 600 duration, got %d", meta.TestConfig.DurationSeconds)
	}
}

func TestLoadBenchmarkFromDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Write metadata.json
	metaData := `{
  "benchmark_version": "1.0",
  "started_at": "2026-02-05T22:00:00Z",
  "hostname": "test-host",
  "hardware": {
    "gpu_name": "NVIDIA GeForce RTX 4090",
    "gpu_memory_mib": 24564,
    "gpu_count": 1,
    "driver_version": "550.127.05",
    "cuda_version": "12.4",
    "cpu_model": "AMD EPYC 7763",
    "cpu_cores": 16,
    "ram_gib": 64
  },
  "model": {
    "name": "deepseek-r1:32b",
    "runtime": "ollama",
    "runtime_version": "0.5.7",
    "size": "19GB"
  },
  "test_config": {
    "duration_seconds": 600,
    "max_tokens": 256,
    "prompts": ["general_knowledge", "coding"]
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "metadata.json"), []byte(metaData), 0644); err != nil {
		t.Fatal(err)
	}

	// Write results.jsonl
	resultsData := `{"t":1000,"n":0,"tok":256,"tps":44.21}
{"t":1005,"n":1,"tok":256,"tps":44.15}
{"t":1010,"n":2,"tok":256,"tps":44.08}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "results.jsonl"), []byte(resultsData), 0644); err != nil {
		t.Fatal(err)
	}

	// Write gpu.csv
	gpuData := `ts,util,mem_used,mem_total,temp,power
1000,95,20368,24564,64,375.0
1005,96,20368,24564,65,376.0
1010,97,20368,24564,66,377.0
`
	if err := os.WriteFile(filepath.Join(tmpDir, "gpu.csv"), []byte(gpuData), 0644); err != nil {
		t.Fatal(err)
	}

	// Load the benchmark
	result, err := LoadBenchmarkFromDirectory(tmpDir, "vastai", "US-West", 0.44)
	if err != nil {
		t.Fatalf("LoadBenchmarkFromDirectory failed: %v", err)
	}

	// Verify hardware info
	if result.Hardware.GPUName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("expected RTX 4090, got %s", result.Hardware.GPUName)
	}
	if result.Hardware.GPUMemoryMiB != 24564 {
		t.Errorf("expected 24564 MiB, got %d", result.Hardware.GPUMemoryMiB)
	}

	// Verify model info
	if result.Model.Name != "deepseek-r1:32b" {
		t.Errorf("expected deepseek-r1:32b, got %s", result.Model.Name)
	}
	if result.Model.Family != "deepseek-r1" {
		t.Errorf("expected family deepseek-r1, got %s", result.Model.Family)
	}
	if result.Model.ParameterCount != "32B" {
		t.Errorf("expected 32B params, got %s", result.Model.ParameterCount)
	}
	if result.Model.SizeGB != 19 {
		t.Errorf("expected 19 GB size, got %f", result.Model.SizeGB)
	}

	// Verify results
	if result.Results.TotalRequests != 3 {
		t.Errorf("expected 3 requests, got %d", result.Results.TotalRequests)
	}

	// Verify GPU stats
	if result.GPUStats.AvgUtilizationPct < 95 || result.GPUStats.AvgUtilizationPct > 97 {
		t.Errorf("expected ~96%% utilization, got %f", result.GPUStats.AvgUtilizationPct)
	}

	// Verify provider info
	if result.Provider != "vastai" {
		t.Errorf("expected vastai provider, got %s", result.Provider)
	}
	if result.PricePerHour != 0.44 {
		t.Errorf("expected $0.44/hr, got %f", result.PricePerHour)
	}
}
