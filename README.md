# Cloud GPU Shopper

A unified inventory and orchestration service for commodity GPU cloud providers. Acts as a "menu and orchestrator" - provisions GPU instances, hands off direct access to consumers, and ensures safe lifecycle management.

## Features

- **Unified Inventory**: Aggregates GPU offerings from multiple providers (Vast.ai, TensorDock)
- **Smart Provisioning**: Two-phase provisioning with crash recovery
- **Lifecycle Management**: Automatic spin-down, 12-hour hard max, heartbeat monitoring
- **Cost Tracking**: Per-session and per-consumer cost aggregation with budget alerts
- **Safety First**: Orphan detection, verified destruction, provider reconciliation

## Supported Providers

| Provider | Status | Features |
|----------|--------|----------|
| Vast.ai | Implemented | Instance tags, spot pricing |
| TensorDock | Implemented | On-demand pricing |

## Quick Start

### Prerequisites

- Go 1.21+
- SQLite 3

### Configuration

Set environment variables or create a `.env` file:

```bash
# Provider Credentials
VASTAI_API_KEY=your_vastai_api_key
TENSORDOCK_API_ID=your_tensordock_api_id
TENSORDOCK_API_TOKEN=your_tensordock_api_token

# Server Configuration
SERVER_PORT=8080
LOG_LEVEL=info
LOG_FORMAT=json

# Database
DATABASE_PATH=./data/shopper.db
```

### Build

```bash
go build -o bin/gpu-shopper ./cmd/server
go build -o bin/gpu-cli ./cmd/cli
```

### Run Tests

```bash
go test ./...
```

## Architecture

```
cloud-gpu-shopper/
├── cmd/
│   ├── server/       # Main API server
│   └── cli/          # CLI tool
├── internal/
│   ├── config/       # Configuration loading
│   ├── logging/      # Structured logging
│   ├── provider/     # Provider adapters
│   │   ├── vastai/
│   │   └── tensordock/
│   ├── storage/      # SQLite persistence
│   └── service/      # Business logic (coming soon)
├── pkg/
│   └── models/       # Shared data models
└── docs/             # Documentation
```

## API Overview

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/inventory` | GET | List available GPU offers |
| `/sessions` | POST | Provision a new GPU session |
| `/sessions/:id` | GET | Get session details |
| `/sessions/:id` | DELETE | Terminate a session |
| `/costs` | GET | Get cost summary |
| `/health` | GET | Health check |

## Safety Mechanisms

1. **Two-Phase Provisioning**: Database record created before provider call
2. **Verified Destruction**: Polls until instance confirmed destroyed
3. **Instance Tagging**: All instances tagged with `shopper-{sessionID}`
4. **Provider Reconciliation**: Every 5 minutes, detects orphans and ghosts
5. **Startup Recovery**: Reconciles state on service restart
6. **Agent Self-Destruct**: Independent failsafe timer on provisioned nodes
7. **Hard Max Enforcement**: 12-hour default limit, CLI override available

## Development Status

See [PROGRESS.md](PROGRESS.md) for detailed implementation status.

**Current Phase**: Day 1 Complete - Foundation + Provider Adapters

## License

MIT
