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
| Vast.ai | Implemented | Instance tags, spot pricing |
| TensorDock | Implemented | On-demand pricing |

## Quick Start

### Prerequisites

- Go 1.22+
- Docker (optional, for containerized deployment)

### Environment Variables

```bash
export VASTAI_API_KEY=your-vastai-key
export TENSORDOCK_API_KEY=your-tensordock-key
export TENSORDOCK_AUTH_ID=your-tensordock-auth-id
export DATABASE_PATH=./data/gpu-shopper.db
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

## CLI Reference

### inventory

List available GPU offers from all providers.

```bash
./bin/gpu-shopper inventory [flags]

Flags:
  --provider string      Filter by provider ("vastai", "tensordock")
  --gpu-type string      Filter by GPU type (e.g., "RTX 4090", "A100")
  --min-vram int         Minimum VRAM in GB
  --max-price float      Maximum price per hour in USD
  --min-gpu-count int    Minimum number of GPUs
  --json                 Output as JSON
```

Example output:
```
OFFER ID          PROVIDER    GPU TYPE    VRAM   PRICE/HR   LOCATION
vastai-12345      vastai      RTX 4090    24GB   $0.45      US-West
vastai-12346      vastai      RTX 4090    24GB   $0.48      US-East
tensordock-789    tensordock  A100        40GB   $1.20      EU-West
```

### provision

Provision a new GPU session.

```bash
./bin/gpu-shopper provision [flags]

Flags:
  --offer-id string      GPU offer ID to provision (required)
  --consumer-id string   Identifier for your application (required)
  --hours int            Reservation duration in hours, 1-12 (required)
  --workload-type string Workload type: "llm", "training", "batch" (default "llm")
```

### sessions

Manage active GPU sessions.

```bash
# List all sessions
./bin/gpu-shopper sessions list [--consumer-id string] [--status string]

# Get session details
./bin/gpu-shopper sessions get <session-id>

# Signal work complete (triggers cleanup)
./bin/gpu-shopper sessions done <session-id>

# Extend a session
./bin/gpu-shopper sessions extend <session-id> --hours 2

# Force destroy a session
./bin/gpu-shopper sessions destroy <session-id>
```

Example output for `sessions list`:
```
SESSION ID     CONSUMER      GPU TYPE    STATUS     EXPIRES AT
sess-abc123    my-app        RTX 4090    running    2026-01-29 14:00:00
sess-def456    training-1    A100        running    2026-01-29 18:00:00
```

### costs

View cost information.

```bash
./bin/gpu-shopper costs [flags]

Flags:
  --consumer-id string   Filter by consumer
  --session-id string    Get cost for specific session

# Get monthly summary
./bin/gpu-shopper costs summary [--consumer-id string]
```

Example output:
```
COST SUMMARY (my-app)
Total Cost:      $45.67
Sessions:        12
Hours Used:      98.5

By Provider:
  vastai:        $30.00
  tensordock:    $15.67

By GPU Type:
  RTX 4090:      $25.00
  A100:          $20.67
```

## API Overview

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/metrics` | GET | Prometheus metrics |
| `/api/v1/inventory` | GET | List available GPUs |
| `/api/v1/inventory/:id` | GET | Get specific offer |
| `/api/v1/sessions` | POST | Create session |
| `/api/v1/sessions` | GET | List sessions |
| `/api/v1/sessions/:id` | GET | Get session |
| `/api/v1/sessions/:id` | DELETE | Force destroy session |
| `/api/v1/sessions/:id/done` | POST | Signal session complete |
| `/api/v1/sessions/:id/extend` | POST | Extend session |
| `/api/v1/costs` | GET | Get costs |
| `/api/v1/costs/summary` | GET | Monthly cost summary |

See [docs/API.md](docs/API.md) for full API documentation with request/response examples.

## Configuration Reference

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `VASTAI_API_KEY` | Yes* | API key for Vast.ai provider |
| `TENSORDOCK_API_KEY` | Yes* | API token for TensorDock provider |
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
