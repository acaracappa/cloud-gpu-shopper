# GPU Benchmarking Infrastructure

This document covers the complete benchmarking system: infrastructure architecture, collected results, API reference, and how to run your own benchmarks.

## Table of Contents

- [Infrastructure Overview](#infrastructure-overview)
- [Benchmark Results](#benchmark-results)
- [GPU Analysis](#gpu-analysis)
- [Provider Comparison](#provider-comparison)
- [Recommendations](#recommendations)
- [Architecture Details](#architecture-details)
- [API Reference](#api-reference)
- [CLI Reference](#cli-reference)
- [Running Your Own Benchmarks](#running-your-own-benchmarks)
- [Test Design & Failure Modes](#test-design--failure-modes)
- [Methodology](#methodology)

---

## Infrastructure Overview

The benchmarking system consists of several components that work together to execute, store, query, and compare LLM inference benchmarks across GPU hardware and cloud providers.

| Component | Location | Purpose |
|-----------|----------|---------|
| Data Models | `internal/benchmark/models.go` | Core structs: `BenchmarkResult`, `HardwareInfo`, `ModelInfo`, `PerformanceResults`, `GPUStats`, `CostAnalysis` |
| Result Store | `internal/benchmark/store.go` | SQLite persistence with query methods (by model, GPU, best, cheapest, recommendations) |
| Parser | `internal/benchmark/parser.go` | Parses raw benchmark output (JSONL request logs, GPU CSV metrics, metadata JSON) |
| Test Manifest | `internal/benchmark/manifest.go` | Tracks benchmark test runs with status (pending/running/success/failed/timeout/skipped), worker assignment, cost tracking |
| REST API | `internal/api/benchmark_handlers.go` | 7 endpoints for listing, querying, comparing, and submitting benchmarks |
| CLI | `cmd/cli/cmd/benchmark.go` | `benchmarks`, `benchmarks best`, `benchmarks cheapest`, `benchmarks recommend`, `benchmarks compare` |
| Loader | `cmd/benchmark-loader/main.go` | Imports benchmark results from directories into the database |

### Data Flow

```
GPU Instance → benchmark script → results.jsonl + gpu.csv + metadata.json
     ↓
SSH collect (cat redirect, not SCP)
     ↓
benchmark-loader → SQLite (benchmarks table)
     ↓
REST API / CLI → query, compare, recommend
```

---

## Benchmark Results

**Date**: February 6, 2026
**Total Benchmarks**: 23
**GPUs Tested**: 9 (RTX 3090, RTX 4090, RTX 5060 Ti, RTX 5070, RTX 5070 Ti, RTX 5090, A100 80GB, H200 NVL)
**Models Tested**: 6
**Providers**: Vast.ai, TensorDock

### phi3:mini (3.8B)

| GPU | Provider | TPS | $/hr | Tokens/$ | Notes |
|-----|----------|-----|------|----------|-------|
| RTX 5070 Ti | Vast.ai | **284.7** | $0.094 | **10.9M** | Exceptional consistency (282.8-286.7 TPS range) |

### qwen2:1.5b

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| RTX 3090 | TensorDock | **235.7** | $0.20 | 4.2M |
| RTX 5060 Ti | Vast.ai | 214.0 | $0.15 | **5.1M** |
| RTX 5070 | Vast.ai | 173.5 | $0.18 | 3.5M |

### qwen2:7b

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| RTX 5090 | Vast.ai | **304.8** | $0.21 | 5.2M |
| A100 80GB | Vast.ai | 199.9 | $0.33 | 2.2M |
| RTX 4090 | Vast.ai | 195.3 | $0.16 | 4.4M |
| RTX 4090 | TensorDock | 189.5 | $0.44 | 1.6M |
| RTX 3090 | Vast.ai | 167.4 | $0.08 | **7.5M** |
| RTX 3090 | TensorDock | 126.7 | $0.20 | 2.3M |

### deepseek-r1:14b

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| RTX 5090 | Vast.ai | **149.2** | $0.21 | 2.6M |
| RTX 4090 | Vast.ai | 96.7 | $0.16 | 2.2M |
| RTX 4090 | TensorDock | 93.8 | $0.44 | 767K |
| A100 80GB | Vast.ai | 86.3 | $0.33 | 941K |
| RTX 3090 | Vast.ai | 81.1 | $0.08 | **3.7M** |
| RTX 3090 | TensorDock | 44.7 | $0.20 | 804K |

### deepseek-r1:32b

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| RTX 5090 | Vast.ai | **72.5** | $0.21 | **1.2M** |
| A100 80GB | Vast.ai | 42.1 | $0.33 | 459K |
| RTX 4090 | TensorDock | 13.0 | $0.44 | 107K |
| RTX 3090 | TensorDock | 11.3 | $0.20 | 204K |
| RTX 3090 | Vast.ai | 3.6 | $0.08 | 161K |

### deepseek-r1:70b

| GPU | Provider | TPS | $/hr | Tokens/$ | Notes |
|-----|----------|-----|------|----------|-------|
| H200 NVL | Vast.ai | **36.3** | $2.00 | 65K | 10x faster than A100 (144GB HBM3e) |
| A100 80GB | Vast.ai | 3.5 | $0.33 | 38K | Memory pressure limits throughput |

### Cost Efficiency Summary (Tokens per Dollar)

| GPU | Provider | qwen2:7b | deepseek-r1:14b | deepseek-r1:32b |
|-----|----------|----------|-----------------|-----------------|
| RTX 3090 | Vast.ai | **7.5M** | **3.7M** | 161K |
| RTX 5090 | Vast.ai | 5.2M | 2.6M | **1.2M** |
| RTX 4090 | Vast.ai | 4.4M | 2.2M | N/A |
| A100 80GB | Vast.ai | 2.2M | 941K | 459K |

---

## GPU Analysis

| GPU | VRAM | $/hr | Best For | Key Strength | Key Limitation |
|-----|------|------|----------|--------------|----------------|
| H200 NVL | 144GB HBM3e | $2.00 | 70B+ models | 10x faster than A100 on 70B | Expensive; only worth it for 70B+ |
| RTX 5090 | 32GB | $0.21 | Best consumer overall | 72.5 TPS on 32B (7x faster than RTX 4090) | Consumer-grade reliability |
| A100 80GB | 80GB HBM2e | $0.33 | Large models (>24GB VRAM) | Datacenter reliability; ECC memory | Higher base cost |
| RTX 4090 | 24GB | $0.16-0.44 | Production <24GB | Strong small/medium performance | Struggles with 32B (13 TPS); provider variance |
| RTX 5070 Ti | 16GB | $0.094 | Best value small models | 285 TPS on phi3:mini; 10.9M tok/$ | 16GB limits model size |
| RTX 5070 | 12GB | $0.18 | Entry Blackwell | 173 TPS on 1.5B | 12GB VRAM limits to small models |
| RTX 5060 Ti | 16GB | $0.15 | Mid-range small models | 214 TPS on 1.5B | Limited benchmark coverage |
| RTX 3090 | 24GB | $0.08-0.20 | Best cost efficiency | 7.5M tok/$ on qwen2:7b at $0.08/hr | Older arch; Vast.ai 2x faster than TensorDock |

---

## Provider Comparison

| Dimension | Vast.ai | TensorDock |
|-----------|---------|------------|
| Pricing | Significantly cheaper (RTX 3090 $0.08 vs $0.20, RTX 4090 $0.16 vs $0.44) | 2-3x more expensive for same GPU |
| Performance | Higher and more consistent TPS | Lower TPS with higher variance |
| Consistency | Tight TPS ranges (e.g. RTX 3090 qwen2:7b: 167.2-168.4) | Wide TPS ranges (e.g. RTX 3090 qwen2:7b: 88.6-169.5) |
| Provisioning | Generally faster, more reliable | 80%+ stale inventory; frequent failures |
| GPU Selection | Consumer + datacenter (H200, 50-series, A100) | Mostly consumer GPUs |
| RTX 3090 qwen2:7b | 167.4 TPS @ $0.08/hr = **7.5M tok/$** | 126.7 TPS @ $0.20/hr = 2.3M tok/$ |
| RTX 4090 deepseek-r1:14b | 96.7 TPS @ $0.16/hr = **2.2M tok/$** | 93.7 TPS @ $0.44/hr = 767K tok/$ |

---

## Recommendations

### By Model Size

| Model Size | Budget Pick | Performance Pick |
|------------|-------------|------------------|
| 1.5-4B | RTX 5070 Ti @ $0.094/hr | RTX 5070 Ti @ $0.094/hr |
| 7B | RTX 3090 Vast @ $0.08/hr | RTX 5090 @ $0.21/hr |
| 14B | RTX 3090 Vast @ $0.08/hr | RTX 5090 @ $0.21/hr |
| 32B | RTX 5090 @ $0.21/hr | A100 80GB @ $0.33/hr |
| 70B | A100 80GB @ $0.33/hr | H200 NVL @ $2.00/hr |

### By Use Case

| Use Case | GPU | Provider | $/hr | Why |
|----------|-----|----------|------|-----|
| Dev/Test (small models) | RTX 5070 Ti | Vast.ai | $0.094 | Best tokens per dollar (10.9M/dollar) |
| Dev/Test (medium models) | RTX 3090 | Vast.ai | $0.08 | Best value for 7B+ models |
| Production (<24GB models) | RTX 5090 | Vast.ai | $0.21 | Best performance/price balance |
| Production (32B-70B) | A100 80GB | Vast.ai | $0.33 | Required VRAM headroom |
| High-throughput (70B+) | H200 NVL | Vast.ai | $2.00 | 10x faster than A100 |

---

## Architecture Details

### Data Models

The `BenchmarkResult` struct captures the full benchmark context:

```
BenchmarkResult
├── Hardware: GPUName, GPUMemoryMiB, GPUCount, DriverVersion, CUDAVersion, CPU, RAM
├── Model: Name, Family, ParameterCount, Quantization, SizeGB, Runtime
├── TestConfig: DurationMinutes, MaxTokens, ConcurrentReqs, WarmupRequests
├── Results: TPS (avg/min/max/p50/p95/p99), Latency, RequestsPerMinute, TTFT
├── GPUStats: Utilization, Temperature, PowerDraw, MemoryUsed
└── Provider, Location, PricePerHour
```

### Storage Schema

```sql
CREATE TABLE benchmarks (
    id TEXT PRIMARY KEY,
    timestamp DATETIME NOT NULL,
    gpu_name TEXT NOT NULL,
    gpu_memory_mib INTEGER NOT NULL,
    model_name TEXT NOT NULL,
    runtime TEXT NOT NULL,
    avg_tokens_per_second REAL NOT NULL,
    price_per_hour REAL,
    provider TEXT,
    -- ... 30+ columns for detailed metrics
    full_result_json TEXT  -- Complete result for API responses
);

CREATE INDEX idx_benchmarks_model ON benchmarks(model_name);
CREATE INDEX idx_benchmarks_gpu ON benchmarks(gpu_name);
```

### Test Manifest

The manifest system tracks multi-GPU benchmark runs:

```sql
CREATE TABLE benchmark_manifest (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,          -- Groups tests from same batch
    gpu_type TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    status TEXT NOT NULL,          -- pending, running, success, failed, timeout, skipped
    worker_id TEXT,                -- Which agent is running this test
    session_id TEXT,               -- Cloud GPU session
    tokens_per_second REAL,
    failure_reason TEXT,
    failure_stage TEXT             -- provision, setup, load, benchmark, collect
);
```

### Result Parser

The parser handles three file formats from raw benchmark output:

| File | Format | Content |
|------|--------|---------|
| `results.jsonl` | JSONL | Per-request results: `{t, n, tok, tps, dur, err}` |
| `gpu.csv` | CSV | GPU samples: timestamp, utilization, memory, temperature, power |
| `metadata.json` | JSON | Test config, hardware info, model info |

The parser computes:
- TPS percentiles (p50, p95, p99)
- Latency statistics
- GPU utilization aggregates
- Cost analysis (tokens/dollar, cost per million tokens)

---

## API Reference

### List Benchmarks

```
GET /api/v1/benchmarks
```

Query parameters:

| Param | Type | Description |
|-------|------|-------------|
| `model` | string | Filter by model name (e.g. `deepseek-r1:14b`) |
| `gpu` | string | Filter by GPU name (partial match) |
| `provider` | string | Filter by provider |
| `runtime` | string | Filter by runtime |
| `limit` | int | Max results (default 50, max 200) |

### Get Benchmark

```
GET /api/v1/benchmarks/:id
```

Returns full benchmark result with cost analysis.

### Best Performance

```
GET /api/v1/benchmarks/best?model=deepseek-r1:32b
```

Returns the highest TPS benchmark for the specified model.

### Most Cost-Effective

```
GET /api/v1/benchmarks/cheapest?model=qwen2:7b&min_tps=100
```

Returns the benchmark with the best tokens-per-dollar ratio.

### Compare Hardware

```
GET /api/v1/benchmarks/compare?model=deepseek-r1:14b
```

Returns all benchmarks for a model with speedup factors, cost efficiency, and memory efficiency relative to the best performer.

### Hardware Recommendations

```
GET /api/v1/benchmarks/recommendations?model=qwen2:7b
```

Returns GPU recommendations ranked by average TPS, with expected performance and cost.

### Submit Benchmark

```
POST /api/v1/benchmarks
Content-Type: application/json

{
  "hardware": {"gpu_name": "RTX 4090", "gpu_memory_mib": 24576, ...},
  "model": {"name": "deepseek-r1:14b", "runtime": "ollama", ...},
  "results": {"avg_tokens_per_second": 93.7, "total_requests": 67, ...},
  "provider": "tensordock",
  "price_per_hour": 0.44
}
```

---

## CLI Reference

```bash
# List recent benchmarks
gpu-shopper benchmarks [--model MODEL] [--gpu GPU] [--limit N]

# Find best performing hardware for a model
gpu-shopper benchmarks best --model deepseek-r1:32b

# Find most cost-effective hardware
gpu-shopper benchmarks cheapest --model qwen2:7b [--min-tps 100]

# Get hardware recommendations
gpu-shopper benchmarks recommend --model deepseek-r1:14b

# Compare all hardware for a model
gpu-shopper benchmarks compare --model qwen2:7b
```

Output formats: `--output table` (default) or `--output json`.

---

## Running Your Own Benchmarks

### Step 1: Provision a GPU

```bash
./bin/gpu-shopper provision -c benchmark-test -g RTX4090 -t 2 --save-key ~/.ssh/bench_key
```

Wait for session to reach `running` status.

### Step 2: Setup Instance

```bash
# Install Ollama
ssh -i ~/.ssh/bench_key root@<ip> "curl -fsSL https://ollama.com/install.sh | sh"

# Verify GPU
ssh -i ~/.ssh/bench_key root@<ip> "nvidia-smi"

# Pull model
ssh -i ~/.ssh/bench_key root@<ip> "ollama pull qwen2:7b"
```

### Step 3: Run Benchmark

```bash
# 5-minute benchmark
ssh -i ~/.ssh/bench_key root@<ip> 'MODEL=qwen2:7b DURATION=300 /tmp/bench.sh'
```

### Step 4: Collect Results

```bash
# Use SSH cat redirect (works without SCP permissions)
ssh -i ~/.ssh/bench_key root@<ip> "cat /tmp/benchmark_results.json" > ./results/bench.json
```

### Step 5: Import to Database

```bash
go run ./cmd/benchmark-loader \
  -db ./data/gpu-shopper.db \
  -dir ./results/benchmark_qwen2_7b_* \
  -provider vastai \
  -price 0.16 \
  -location "US"
```

### Step 6: Query Results

```bash
curl "http://localhost:8080/api/v1/benchmarks/best?model=qwen2:7b"
```

---

## Test Design & Failure Modes

### Planned Test Matrix

| GPU | Provider | Models |
|-----|----------|--------|
| RTX 3090 | Vast.ai, TensorDock | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| RTX 4090 | Vast.ai, TensorDock | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| RTX 5090 | Vast.ai | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| A100 80GB | Vast.ai | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| H200 NVL | Vast.ai | deepseek-r1:70b |
| RTX 5060 Ti | Vast.ai | qwen2:1.5b |
| RTX 5070 | Vast.ai | qwen2:1.5b |

### Known Failure Modes

#### Provisioning Failures

| Error | Cause | Mitigation |
|-------|-------|------------|
| "No available public IPs" | TensorDock hostnode exhausted | Retry with different location |
| "Instance stopped" | Provider killed instance | Detect early, fail fast |
| Stale inventory | TensorDock shows unavailable GPUs | Global failure tracking degrades offers |
| SSH timeout | Heavy image pull | Template-aware timeouts |

#### Execution Failures

| Error | Cause | Mitigation |
|-------|-------|------------|
| OOM during model load | Model too large for VRAM | Check VRAM before loading |
| Ollama not responding | Service crashed | Health check before benchmark |
| Slow/stuck inference | Memory pressure, swapping | Monitor TPS during run |
| Connection lost | Instance terminated | Periodic heartbeat check |

#### Collection Failures

| Error | Cause | Mitigation |
|-------|-------|------------|
| SCP permission denied | Subagent lacks permissions | Use SSH cat redirect instead |
| Results file missing | Benchmark didn't complete | Check exit status first |
| Partial results | Benchmark interrupted | Save incrementally with checkpoints |

### Benchmark Phases

1. **Setup & Validation**: Provision instance, verify nvidia-smi, install Ollama, health check
2. **Model Loading**: Check VRAM, pull model, verify loaded, warmup request
3. **Execution**: 5-minute timed run with per-request logging and periodic health checks
4. **Collection**: SSH cat results, validate JSON, import to database, update manifest

---

## Methodology

### Test Configuration

| Parameter | Value |
|-----------|-------|
| Duration | 5 minutes per model per GPU |
| Max Tokens | 256 per request |
| Concurrency | 1 (sequential requests) |
| Prompts | 10 diverse (coding, technical, creative, general) |
| Runtime | Ollama (latest stable) |

### Metrics Collected

| Category | Metrics |
|----------|---------|
| Throughput | TPS (avg, min, max, p50, p95, p99), requests/minute |
| Latency | Per-request latency (avg, min, max, p50, p95, p99), TTFT |
| GPU | Utilization %, temperature, power draw, memory used |
| Cost | Tokens per dollar, cost per million tokens, estimated monthly |

### Raw Data Access

All benchmark data is stored in SQLite and queryable:

```sql
SELECT gpu_name, model_name, provider, avg_tokens_per_second, price_per_hour
FROM benchmarks ORDER BY model_name, avg_tokens_per_second DESC;
```

Or via the API:
```bash
curl http://localhost:8080/api/v1/benchmarks
```
