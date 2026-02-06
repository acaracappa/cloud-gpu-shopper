# Cloud GPU Shopper

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Build Status](https://img.shields.io/badge/Build-Passing-brightgreen)]()

A unified inventory and orchestration service for commodity GPU providers (Vast.ai, TensorDock). Acts as a "menu and provisioner" - select, provision, hand off credentials, ensure cleanup.

## Table of Contents

- [Key Principle](#key-principle)
- [Why Cloud GPU Shopper?](#why-cloud-gpu-shopper)
- [Features](#features)
- [Supported Providers](#supported-providers)
- [Quick Start](#quick-start)
- [Common Use Cases](#common-use-cases)
- [GPU Benchmarking](#gpu-benchmarking)
- [CLI Reference](#cli-reference)
- [API Overview](#api-overview)
- [Configuration Reference](#configuration-reference)
- [Architecture](#architecture)
- [Safety Systems](#safety-systems)
- [Development](#development)
- [Getting Help](#getting-help)
- [License](#license)

## Key Principle

**Menu, not middleman.** We provision and hand off direct access. We don't proxy traffic.

This design philosophy means Cloud GPU Shopper acts as a catalog and orchestrator, not a gateway. Once your GPU session is provisioned, you connect directly to the instance via SSH - no intermediary, no added latency, no single point of failure. Your workloads run with full performance and you maintain complete control.

## Why Cloud GPU Shopper?

Managing GPU compute across multiple cloud providers is complex and risky:

- **Unified Interface**: Browse and compare GPU offers across Vast.ai and TensorDock from a single API. No need to learn multiple provider interfaces or maintain separate integrations.

- **Built-in Safety Systems**: Prevent runaway costs with automatic 12-hour session limits, orphan instance detection, and verified destruction. The service is designed with "zero orphaned instances" as the primary goal.

- **Simple Provisioning Workflow**: Create a session with one API call or CLI command. Get SSH credentials immediately. Signal when done and the instance is cleaned up automatically.

## Features

- **Unified Inventory**: Browse GPUs across multiple providers with filtering
- **Session Management**: Provision, monitor, and destroy GPU sessions
- **Safety Systems**: 12-hour hard max, orphan detection, verified destruction
- **Cost Tracking**: Per-session and per-consumer cost aggregation with budget alerts

## Supported Providers

| Provider | Status | Features |
|----------|--------|----------|
| Vast.ai | Implemented | Instance tags, spot pricing, Docker templates |
| TensorDock | Implemented | On-demand pricing, dedicated IPs |

**TensorDock Note:** The `ubuntu2404` image requires manual NVIDIA driver installation:
```bash
ssh user@<ip> "sudo apt-get update && sudo apt-get install -y nvidia-driver-550 && sudo reboot"
```

## Quick Start

### Prerequisites

- Go 1.22+
- Docker (optional, for containerized deployment)

### Environment Variables

Create a `.env` file in the project root (automatically loaded by the server):

```bash
VASTAI_API_KEY=your-vastai-key
TENSORDOCK_API_TOKEN=your-tensordock-token
TENSORDOCK_AUTH_ID=your-tensordock-auth-id
DATABASE_PATH=./data/gpu-shopper.db
```

Or export them directly:

```bash
export VASTAI_API_KEY=your-vastai-key
export TENSORDOCK_API_TOKEN=your-tensordock-token
export TENSORDOCK_AUTH_ID=your-tensordock-auth-id
```

### Run the Server

```bash
# Build and run
go build -o bin/server ./cmd/server
./bin/server

# Or run directly
go run ./cmd/server
```

The server starts on `http://localhost:8080`.

### Use the CLI

```bash
# Build CLI
go build -o bin/gpu-shopper ./cmd/cli

# List available GPUs
./bin/gpu-shopper inventory

# Provision a session
./bin/gpu-shopper provision --offer-id <offer-id> --consumer-id my-app --hours 2

# List active sessions
./bin/gpu-shopper sessions list

# Signal session complete
./bin/gpu-shopper sessions done <session-id>

# View costs
./bin/gpu-shopper costs --consumer-id my-app
```

### Docker Deployment

```bash
cd deploy

# Start server only
docker-compose up -d server

# Start with monitoring (Prometheus + Grafana)
docker-compose --profile monitoring up -d

# View logs
docker-compose logs -f server
```

Access points:
- API Server: http://localhost:8080
- Prometheus: http://localhost:9090 (with monitoring profile)
- Grafana: http://localhost:3000 (with monitoring profile, admin/admin)

## Common Use Cases

### Running LLM Inference Workloads

Deploy vLLM, Ollama, or Text Generation Inference on demand:

```bash
# Find an RTX 4090 with at least 24GB VRAM under $0.50/hour
./bin/gpu-shopper inventory --gpu-type "RTX 4090" --min-vram 24 --max-price 0.50

# Provision for 4 hours
./bin/gpu-shopper provision --offer-id vastai-12345 --consumer-id llm-service --hours 4

# SSH in and start your inference server
ssh -i session-key root@192.168.1.100 "docker run -d --gpus all vllm/vllm-openai ..."

# When done, signal completion for automatic cleanup
./bin/gpu-shopper sessions done sess-abc123
```

### Training ML Models

Spin up high-memory GPUs for training runs:

```bash
# Find A100s for training
./bin/gpu-shopper inventory --gpu-type "A100" --min-vram 40

# Provision with longer duration
./bin/gpu-shopper provision --offer-id tensordock-67890 --consumer-id training-job-42 --hours 8

# Your training scripts connect directly via SSH
```

### Batch GPU Processing Jobs

Process large datasets with burst GPU capacity:

```bash
# Find the cheapest available GPUs
./bin/gpu-shopper inventory --max-price 0.30

# Provision multiple sessions for parallel processing
for i in {1..4}; do
  ./bin/gpu-shopper provision --offer-id $OFFER_ID --consumer-id batch-job-$i --hours 2
done

# Sessions auto-terminate after reservation expires, or signal done when complete
```

### Real-World Benchmark: DeepSeek-R1:32b on RTX 4090

This benchmark was performed on a TensorDock RTX 4090 provisioned through Cloud GPU Shopper.

**Setup:**
```bash
# Provision an RTX 4090 in Joplin, Missouri
./bin/gpu-shopper provision \
  --offer-id tensordock-071132ae-8c07-4d6b-9c37-041a55a85047-geforcertx4090-pcie-24gb \
  --consumer-id benchmark-test \
  --hours 1 \
  --save-key ~/.ssh/benchmark_key

# SSH and install Ollama
ssh -i ~/.ssh/benchmark_key user@<ip> "curl -fsSL https://ollama.com/install.sh | sh"

# Pull the model (19GB)
ssh -i ~/.ssh/benchmark_key user@<ip> "ollama pull deepseek-r1:32b"
```

**Benchmark Results: DeepSeek-R1:32b (19GB model)**

| Metric | Value |
|--------|-------|
| **Single Request Speed** | ~44 tokens/sec |
| **Concurrent Throughput** | ~41 tokens/sec |
| **VRAM Usage** | 20.4 GB / 24 GB (83%) |
| **GPU Power Draw** | ~360W |
| **GPU Temperature** | 52°C |

**Performance by Task Type:**

| Task | Output Tokens | Speed | Time |
|------|---------------|-------|------|
| Short Q&A | 12 | 48.0 tok/s | 0.29s |
| Math Problem | 150 | 44.4 tok/s | 10.6s |
| Code Generation | 256 | 44.2 tok/s | 5.8s |
| Reasoning | 200 | 44.3 tok/s | 4.5s |
| Long Generation | 400 | 44.1 tok/s | 9.1s |

**Cost Analysis:**
- Hourly rate: $0.44/hr (TensorDock RTX 4090)
- Tokens per hour: ~158,400 (at 44 tok/s)
- Cost per 1M tokens: ~$0.003

**Key Observations:**
1. Generation speed stays consistent (~44 tok/s) regardless of output length
2. The 32B parameter model fits comfortably with 17% VRAM headroom
3. GPU thermals remain cool (52°C) under sustained load
4. Ollama serializes concurrent requests (no batching optimization)

**Extended Stress Test (7 min, 67 requests):**

The system maintained stable performance under sustained load:
- **Requests:** 67 total, 0 errors (100% success rate)
- **Tokens Generated:** ~17,000 (256 max per request)
- **Throughput:** Consistent 44.13-44.29 tok/s across all requests
- **GPU Utilization:** 95-97% average (brief dips during request transitions)
- **Temperature:** Stabilized at 64°C (never exceeded safe levels)
- **Power Draw:** Average 371W, peak 377W
- **Memory:** Stable at 20,368 MiB (83% utilization)

This demonstrates the RTX 4090 handles sustained LLM inference workloads with excellent thermal and performance stability.

## GPU Benchmarking

Cloud GPU Shopper includes comprehensive benchmarking infrastructure for evaluating LLM inference performance across different GPUs and providers. This enables data-driven hardware selection based on your specific model requirements.

### Benchmark Matrix Results

We tested 3 models across 4 GPUs on 2 providers (17 successful benchmarks):

| GPU | Provider | $/hr | qwen2:7b | deepseek-r1:14b | deepseek-r1:32b |
|-----|----------|------|----------|-----------------|-----------------|
| RTX 5090 | Vast.ai | $0.21 | **305 TPS** | **149 TPS** | **72.5 TPS** |
| A100 80GB | Vast.ai | $0.33 | 200 TPS | 86 TPS | 42 TPS |
| RTX 4090 | Vast.ai | $0.16 | 195 TPS | 97 TPS | N/A* |
| RTX 4090 | TensorDock | $0.44 | 190 TPS | 94 TPS | 13 TPS |
| RTX 3090 | Vast.ai | $0.08 | 167 TPS | 81 TPS | 3.6 TPS |
| RTX 3090 | TensorDock | $0.20 | 127 TPS | 45 TPS | 11 TPS |

*RTX 4090 on Vast.ai had VRAM pressure errors with the 32b model.

**Key Findings:**
- **RTX 5090** leads all consumer GPUs, handling 32b models 7x faster than RTX 4090
- **RTX 3090 on Vast.ai** ($0.08/hr) offers best cost efficiency for small/medium models
- **Provider variance is significant** - same GPU can differ 20-80% between providers

### Cost Efficiency (Tokens per Dollar)

| GPU | Provider | qwen2:7b | deepseek-r1:14b | deepseek-r1:32b |
|-----|----------|----------|-----------------|-----------------|
| RTX 3090 | Vast.ai | **7.5M** | **3.7M** | 161K |
| RTX 5090 | Vast.ai | 5.2M | 2.6M | **1.2M** |
| RTX 4090 | Vast.ai | 4.4M | 2.2M | N/A |
| A100 80GB | Vast.ai | 2.2M | 941K | 459K |

### Benchmark API Endpoints

Query benchmark data programmatically:

```bash
# List all benchmarks
curl http://localhost:8080/api/v1/benchmarks

# Find best performing hardware for a model
curl "http://localhost:8080/api/v1/benchmarks/best?model=deepseek-r1:32b"

# Find most cost-effective hardware
curl "http://localhost:8080/api/v1/benchmarks/cheapest?model=qwen2:7b"

# Compare all hardware for a model
curl "http://localhost:8080/api/v1/benchmarks/compare?model=deepseek-r1:14b"

# Get hardware recommendations
curl "http://localhost:8080/api/v1/benchmarks/recommendations?model=qwen2:7b"
```

### Running Your Own Benchmarks

**1. Provision a GPU and install Ollama:**
```bash
./bin/gpu-shopper provision -c benchmark-test -g RTX4090 -t 2 --save-key ~/.ssh/bench_key

ssh -i ~/.ssh/bench_key root@<ip> "curl -fsSL https://ollama.com/install.sh | sh"
```

**2. Pull models and run benchmark:**
```bash
# Pull models
ssh -i ~/.ssh/bench_key root@<ip> "ollama pull qwen2:7b && ollama pull deepseek-r1:14b"

# Run 5-minute benchmark per model
ssh -i ~/.ssh/bench_key root@<ip> 'MODEL=qwen2:7b DURATION=300 /tmp/bench.sh'
```

**3. Collect and store results:**
```bash
# Download benchmark results
scp -i ~/.ssh/bench_key -r root@<ip>:/tmp/benchmark_* ./results/

# Load into database
go run ./cmd/benchmark-loader -db ./data/gpu-shopper.db \
  -dir ./results/benchmark_qwen2_7b_* \
  -provider vastai -price 0.16 -location "US"
```

### Benchmark Methodology

- **Duration**: 5 minutes per model per GPU
- **Max Tokens**: 256 per request
- **Concurrency**: 1 (sequential requests)
- **Prompts**: 10 diverse prompts (coding, technical, creative, general)
- **Runtime**: Ollama (latest stable)
- **Metrics**: TPS, GPU utilization, temperature, power draw, error rates

See [docs/BENCHMARKING.md](docs/BENCHMARKING.md) for the complete benchmarking infrastructure documentation, collected results, and API reference. See [docs/BENCHMARK_REPORT.md](docs/BENCHMARK_REPORT.md) for the raw benchmark analysis.

## CLI Reference

### Global Flags

All commands support these global flags:

```bash
--server string    GPU Shopper server URL (default: $GPU_SHOPPER_URL or "http://localhost:8080")
-o, --output string    Output format: "table" or "json" (default: "table")
```

**Tip:** Set `GPU_SHOPPER_URL` environment variable to avoid passing `--server` repeatedly:
```bash
export GPU_SHOPPER_URL=http://gpu-shopper.internal:8080
```

---

### inventory

List available GPU offers from all providers.

```bash
./bin/gpu-shopper inventory [flags]

Flags:
  -p, --provider string   Filter by provider ("vastai", "tensordock")
  -g, --gpu string        Filter by GPU type (e.g., "RTX4090", "A100")
      --min-vram int      Minimum VRAM in GB
      --max-price float   Maximum price per hour in USD
      --min-gpus int      Minimum number of GPUs
```

**Example: Find cheap RTX 4090s**
```bash
$ ./bin/gpu-shopper inventory -g RTX4090 --max-price 0.50

ID              PROVIDER    GPU    COUNT  VRAM   PRICE/HR  LOCATION
vastai-12345    vastai      RTX4090  1    24GB   $0.42     us-west
vastai-12346    vastai      RTX4090  1    24GB   $0.45     us-east
tensordock-789  tensordock  RTX4090  1    24GB   $0.48     eu-west

Total: 3 offers
```

**Example: Find multi-GPU A100 instances**
```bash
$ ./bin/gpu-shopper inventory -g A100 --min-gpus 4 --min-vram 40

ID              PROVIDER    GPU    COUNT  VRAM   PRICE/HR  LOCATION
vastai-99001    vastai      A100     8    80GB   $8.50     us-central
tensordock-445  tensordock  A100     4    40GB   $4.80     us-east

Total: 2 offers
```

**Example: JSON output for scripting**
```bash
./bin/gpu-shopper inventory -g RTX4090 -o json | jq '.offers[0].id'
```

---

### provision

Provision a new GPU session.

```bash
./bin/gpu-shopper provision [flags]

Flags:
  -c, --consumer string     Consumer ID - identifies your application (required)
  -i, --offer string        Specific offer ID to provision
  -g, --gpu string          GPU type to auto-select cheapest offer (e.g., "RTX4090", "A100")
  -w, --workload string     Workload type (default: "llm")
                            Options: llm, llm_vllm, llm_tgi, training, batch, interactive
  -t, --hours int           Reservation hours, 1-12 (default: 2)
      --idle-timeout int    Idle timeout in minutes, 0 = disabled (default: 0)
      --storage string      Storage policy: "destroy" or "preserve" (default: "destroy")
      --save-key string     Save SSH private key to this file path
```

**Note:** Either `--offer` or `--gpu` must be provided. Using `--gpu` auto-selects the cheapest available offer of that GPU type.

**Example: Provision with auto-select**
```bash
$ ./bin/gpu-shopper provision -c my-llm-service -g RTX4090 -t 4

Auto-selected offer vastai-12345 (RTX4090, $0.42/hr)

Session provisioned successfully!

Session ID:    sess-abc123
Provider:      vastai
GPU Type:      RTX4090
Status:        provisioning
Price/Hour:    $0.42
Expires At:    2026-02-02 18:00:00

SSH Connection:
  Host: 192.168.1.100
  Port: 22
  User: root

SSH Private Key (save this, shown only once):
---BEGIN---
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAA...
-----END OPENSSH PRIVATE KEY-----
---END---

Note: The session is provisioning. Check status with:
  gpu-shopper sessions get sess-abc123
```

**Example: Provision with key file saved**
```bash
./bin/gpu-shopper provision -c training-job -i tensordock-789 -t 8 \
  -w training --save-key ~/.ssh/session_key
chmod 600 ~/.ssh/session_key
ssh -i ~/.ssh/session_key root@192.168.1.100
```

**Example: Provision for vLLM inference**
```bash
./bin/gpu-shopper provision -c vllm-api -g A100 -w llm_vllm -t 6 --idle-timeout 30
```

---

### sessions

Manage active GPU sessions.

```bash
./bin/gpu-shopper sessions <subcommand> [flags]

Subcommands:
  list      List all sessions
  get       Get session details
  done      Signal session completion (graceful shutdown)
  extend    Extend session reservation
  delete    Force delete a session
```

**sessions list**
```bash
./bin/gpu-shopper sessions list [flags]

Flags:
  -c, --consumer string   Filter by consumer ID
  -s, --status string     Filter by status (provisioning, running, stopping, terminated, failed)
```

**Example:**
```bash
$ ./bin/gpu-shopper sessions list -c my-app

ID           CONSUMER  PROVIDER    GPU       STATUS   PRICE/HR  EXPIRES
sess-abc123  my-app    vastai      RTX4090   running  $0.42     2026-02-02 18:00:00
sess-def456  my-app    tensordock  A100      running  $1.20     2026-02-02 20:00:00

Total: 2 sessions
```

**sessions get**
```bash
$ ./bin/gpu-shopper sessions get sess-abc123

Session ID:     sess-abc123
Consumer ID:    my-app
Provider:       vastai
GPU Type:       RTX4090
GPU Count:      1
Status:         running
Workload Type:  llm
Price/Hour:     $0.42
Created At:     2026-02-02 14:00:00
Expires At:     2026-02-02 18:00:00

SSH Connection:
  ssh -p 22 root@192.168.1.100
```

**sessions done**
```bash
$ ./bin/gpu-shopper sessions done sess-abc123
Session sess-abc123 shutdown initiated.
```

**sessions extend**
```bash
./bin/gpu-shopper sessions extend <session-id> [flags]

Flags:
  -t, --hours int   Additional hours to extend, 1-12 (default: 1)
```

**Example:**
```bash
$ ./bin/gpu-shopper sessions extend sess-abc123 -t 2
Session sess-abc123 extended by 2 hours.
New expiration: 2026-02-02 20:00:00
```

**sessions delete**
```bash
$ ./bin/gpu-shopper sessions delete sess-abc123
Session sess-abc123 destroyed.
```

---

### shutdown

Shutdown a GPU session (alternative to `sessions done`).

```bash
./bin/gpu-shopper shutdown <session-id> [flags]

Flags:
  -f, --force   Force immediate shutdown (skip graceful termination)
```

**Example: Graceful shutdown**
```bash
$ ./bin/gpu-shopper shutdown sess-abc123
Session sess-abc123 shutdown initiated.
The session will terminate gracefully.
```

**Example: Force shutdown**
```bash
$ ./bin/gpu-shopper shutdown sess-abc123 --force
Session sess-abc123 forcefully destroyed.
```

---

### costs

View cost information and summaries.

```bash
./bin/gpu-shopper costs [flags]

Flags:
  -c, --consumer string   Filter by consumer ID
  -s, --session string    Get cost for specific session
  -p, --period string     Time period: "daily" or "monthly"
      --start string      Start date (YYYY-MM-DD)
      --end string        End date (YYYY-MM-DD)
```

**Example: View all costs**
```bash
$ ./bin/gpu-shopper costs

Cost Summary
============

Total Cost:    $145.67
Sessions:      28
Hours Used:    298.5

By Provider:
  vastai       $95.00
  tensordock   $50.67

By GPU Type:
  RTX4090      $85.00
  A100         $60.67
```

**Example: Filter by consumer and date range**
```bash
./bin/gpu-shopper costs -c my-app --start 2026-01-01 --end 2026-01-31
```

**costs summary**
```bash
./bin/gpu-shopper costs summary [flags]

Flags:
  -c, --consumer string   Filter by consumer ID
```

---

### transfer

Transfer files to/from GPU sessions using SFTP.

```bash
./bin/gpu-shopper transfer <subcommand> [flags]

Subcommands:
  upload     Upload a file to a session
  download   Download a file from a session

Flags (all subcommands):
  -k, --key string       SSH private key file (required)
  -t, --timeout duration Transfer timeout (default: 5m)
```

**Example: Upload model weights**
```bash
./bin/gpu-shopper transfer upload ./model.bin sess-abc123:/workspace/model.bin \
  -k ~/.ssh/session_key
```

**Example: Download training results**
```bash
./bin/gpu-shopper transfer download sess-abc123:/workspace/output/checkpoint.pt \
  ./checkpoint.pt -k ~/.ssh/session_key
```

---

### cleanup-orphans

Find and destroy orphan GPU instances directly from providers. **Works without the API server.**

This is a safety command for emergency cleanup when instances may have been orphaned due to server issues.

```bash
./bin/gpu-shopper cleanup-orphans [flags]

Flags:
      --execute           Actually destroy instances (default is dry-run)
      --force             Skip confirmation prompt when destroying
  -p, --provider string   Target specific provider ("vastai", "tensordock")
```

**Requires environment variables:**
- `VASTAI_API_KEY` for Vast.ai
- `TENSORDOCK_AUTH_ID` and `TENSORDOCK_API_TOKEN` for TensorDock

**Example: Dry-run (default)**
```bash
$ ./bin/gpu-shopper cleanup-orphans

Scanning for orphan instances...

Checking vastai...
  Found 2 shopper-managed instances
Checking tensordock...
  Found 1 shopper-managed instances

PROVIDER    INSTANCE ID  NAME                  STATUS   PRICE/HR  STARTED
--------    -----------  ----                  ------   --------  -------
vastai      12345        gpu-shopper-abc123    running  $0.420    2026-02-02 10:00
vastai      12346        gpu-shopper-def456    running  $0.450    2026-02-01 22:00
tensordock  td-789       gpu-shopper-session   running  $1.200    2026-02-02 08:00

Total: 3 instances, $2.070/hr combined cost

This was a dry-run. To actually destroy these instances, run:
  gpu-shopper cleanup-orphans --execute
```

**Example: Execute cleanup**
```bash
$ ./bin/gpu-shopper cleanup-orphans --execute

Scanning for orphan instances...
[... table output ...]

WARNING: You are about to destroy 3 instance(s).
Type 'yes' to confirm: yes

Destroying instances...
  Destroying vastai/12345... OK
  Destroying vastai/12346... OK
  Destroying tensordock/td-789... OK

Cleanup complete: 3 destroyed, 0 failed
```

**Example: Target single provider with no confirmation**
```bash
./bin/gpu-shopper cleanup-orphans -p vastai --execute --force
```

---

### CLI Tips

**Filtering inventory effectively:**
```bash
# Find the absolute cheapest available GPU
./bin/gpu-shopper inventory --max-price 0.30 -o json | jq -r '.offers[0]'

# Compare prices across providers for same GPU
./bin/gpu-shopper inventory -g A100 | sort -t'$' -k6 -n

# Find high-VRAM GPUs for large models
./bin/gpu-shopper inventory --min-vram 48
```

**Automation patterns:**
```bash
# Provision and capture session ID
SESSION_ID=$(./bin/gpu-shopper provision -c batch-job -g RTX4090 -t 2 -o json | jq -r '.session.id')

# Wait for session to be running
while [ "$(./bin/gpu-shopper sessions get $SESSION_ID -o json | jq -r '.status')" != "running" ]; do
  sleep 5
done

# Run your workload...

# Clean up when done
./bin/gpu-shopper sessions done $SESSION_ID
```

**Monitor costs in real-time:**
```bash
watch -n 60 './bin/gpu-shopper costs -c my-app'
```

## API Overview

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/metrics` | GET | Prometheus metrics |
| `/api/v1/inventory` | GET | List available GPUs (supports `min_cuda`, `template_hash_id` filters) |
| `/api/v1/inventory/:id` | GET | Get specific offer |
| `/api/v1/inventory/:id/compatible-templates` | GET | Get compatible templates for offer |
| `/api/v1/templates` | GET | List available templates (Vast.ai) |
| `/api/v1/templates/:hash_id` | GET | Get specific template |
| `/api/v1/sessions` | POST | Create session (supports `template_hash_id` and `disk_gb`) |
| `/api/v1/sessions` | GET | List sessions |
| `/api/v1/sessions/:id` | GET | Get session |
| `/api/v1/sessions/:id` | DELETE | Force destroy session |
| `/api/v1/sessions/:id/done` | POST | Signal session complete |
| `/api/v1/sessions/:id/extend` | POST | Extend session |
| `/api/v1/costs` | GET | Get costs |
| `/api/v1/costs/summary` | GET | Monthly cost summary |
| `/api/v1/benchmarks` | GET | List benchmark results |
| `/api/v1/benchmarks/:id` | GET | Get specific benchmark |
| `/api/v1/benchmarks` | POST | Submit new benchmark result |
| `/api/v1/benchmarks/best` | GET | Best performing benchmark for model |
| `/api/v1/benchmarks/cheapest` | GET | Most cost-effective benchmark for model |
| `/api/v1/benchmarks/compare` | GET | Compare benchmarks for model across hardware |
| `/api/v1/benchmarks/recommendations` | GET | Hardware recommendations based on benchmarks |

See [docs/API.md](docs/API.md) for full API documentation with request/response examples.

## Configuration Reference

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `VASTAI_API_KEY` | Yes* | API key for Vast.ai provider |
| `TENSORDOCK_API_TOKEN` | Yes* | API token for TensorDock provider |
| `TENSORDOCK_AUTH_ID` | Yes* | Auth ID for TensorDock provider |
| `DATABASE_PATH` | No | SQLite database path (default: `./data/gpu-shopper.db`) |
| `SERVER_HOST` | No | Server bind address (default: `0.0.0.0`) |
| `SERVER_PORT` | No | Server port (default: `8080`) |
| `LOG_LEVEL` | No | Logging level: debug, info, warn, error (default: `info`) |

*At least one provider must be configured.

### Lifecycle Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LIFECYCLE_CHECK_INTERVAL` | `60s` | How often to check session status |
| `HARD_MAX_HOURS` | `12` | Maximum session duration before forced shutdown |
| `ORPHAN_GRACE_PERIOD` | `15m` | Grace period before orphan detection triggers |
| `RECONCILIATION_INTERVAL` | `5m` | How often to reconcile with providers |

### Inventory Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `INVENTORY_CACHE_TTL` | `60s` | How long to cache inventory responses |
| `INVENTORY_BACKOFF_TTL` | `300s` | Cache TTL when provider is rate-limited |

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    CLOUD GPU SHOPPER                         │
├─────────────────────────────────────────────────────────────┤
│  REST API (Gin)  │  CLI (Cobra)  │  Background Jobs          │
├─────────────────────────────────────────────────────────────┤
│  Inventory │ Provisioner │ Lifecycle │ Cost Tracker          │
├─────────────────────────────────────────────────────────────┤
│         Vast.ai Adapter    │    TensorDock Adapter           │
├─────────────────────────────────────────────────────────────┤
│                     SQLite Storage                           │
└─────────────────────────────────────────────────────────────┘
                              │
                    Provider API + SSH Verification
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      GPU NODE (Remote)                       │
├─────────────────────────────────────────────────────────────┤
│  Consumer Workload: vLLM, Training, Batch Jobs               │
└─────────────────────────────────────────────────────────────┘
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed design documentation.

## Safety Systems

The service is designed with "zero orphaned instances" as the primary goal:

1. **Two-Phase Provisioning**: Database record created before provider call
2. **Verified Destruction**: Retries and confirms instance is gone
3. **Instance Tagging**: All instances tagged for reconciliation
4. **Provider Reconciliation**: Compares DB vs provider every 5 minutes
5. **12-Hour Hard Max**: Automatic shutdown (CLI override available)
6. **SSH Verification**: Validates instance readiness via SSH connectivity
7. **Orphan Detection**: Alerts and auto-destroys orphaned instances

## Development

```bash
# Run tests
go test ./...

# Run tests with race detection (recommended)
go test -race ./...

# Run E2E tests
go test -tags=e2e ./test/e2e/...

# Run tests with coverage
go test -cover ./...

# Build all binaries
go build -o bin/server ./cmd/server
go build -o bin/gpu-shopper ./cmd/cli
```

### Test Quality

All tests are designed to be:
- **Race-free**: Pass with `go test -race`
- **Deterministic**: Use `require.Eventually()` instead of `time.Sleep()`
- **Isolated**: Proper cleanup with `t.Cleanup()` and deferred resource release
- **Time-injectable**: Services support `WithTimeFunc()` for controlled time testing

### Project Structure

```
├── cmd/
│   ├── server/     # API server
│   └── cli/        # CLI tool
├── internal/
│   ├── api/        # REST API handlers
│   ├── config/     # Configuration
│   ├── logging/    # Structured logging
│   ├── metrics/    # Prometheus metrics
│   ├── provider/   # Provider adapters
│   ├── service/    # Business logic
│   └── storage/    # SQLite persistence
├── pkg/models/     # Shared data models
├── deploy/         # Docker files
└── docs/           # Documentation
```

### Development Status

See [PROGRESS.md](PROGRESS.md) for detailed implementation status.

**Current Phase**: Post-MVP - QA Remediation Complete

- MVP fully implemented with all safety systems
- Comprehensive QA review completed (120+ issues addressed)
- Test suite hardened for race-free, deterministic execution
- E2E and live testing infrastructure operational

## Getting Help

- **API Reference**: See [docs/API.md](docs/API.md) for complete API documentation
- **Architecture Details**: See [ARCHITECTURE.md](ARCHITECTURE.md) for internal design
- **Contributing**: See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines
- **Bug Reports**: Open an issue on GitHub for bug reports and feature requests

## License

MIT
