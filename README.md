# Cloud GPU Shopper

A unified inventory and orchestration service for commodity GPU providers (Vast.ai, TensorDock). Acts as a "menu and provisioner" - select, provision, hand off credentials, ensure cleanup.

## Key Principle

**Menu, not middleman.** We provision and hand off direct access. We don't proxy traffic.

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

# Filter by GPU type and max price
./bin/gpu-shopper inventory --gpu-type "RTX 4090" --max-price 0.50

# Provision a session
./bin/gpu-shopper provision --offer-id <offer-id> --consumer-id my-app --hours 2

# List active sessions
./bin/gpu-shopper sessions list

# Get session details
./bin/gpu-shopper sessions get <session-id>

# Signal session complete
./bin/gpu-shopper sessions done <session-id>

# View costs
./bin/gpu-shopper costs --consumer-id my-app
./bin/gpu-shopper costs summary
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

See [docs/API.md](docs/API.md) for full API documentation.

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

## Project Structure

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

## Development Status

See [PROGRESS.md](PROGRESS.md) for detailed implementation status.

**Current Phase**: Post-MVP - QA Remediation Complete

- MVP fully implemented with all safety systems
- Comprehensive QA review completed (120+ issues addressed)
- Test suite hardened for race-free, deterministic execution
- E2E and live testing infrastructure operational

## License

MIT
