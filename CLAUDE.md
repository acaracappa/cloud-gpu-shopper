# Cloud GPU Shopper - Quick Reference

## What Is This?

A Go service that provides unified inventory and orchestration over commodity GPU providers (Vast.ai, TensorDock). Acts as a "menu and provisioner" - select, provision, hand off credentials, ensure cleanup.

## Key Principle

**Menu, not middleman.** We provision and hand off direct access. We don't proxy traffic.

## Components

| Component | Purpose | Location |
|-----------|---------|----------|
| API Server | REST API for inventory, sessions, costs | `cmd/server/` |
| CLI | Operator tool | `cmd/cli/` |
| Providers | Vast.ai, TensorDock adapters | `internal/provider/` |

## Core Services

| Service | Responsibility |
|---------|---------------|
| Inventory | Fetch, cache, filter GPU offers |
| Provisioner | Create/destroy instances, credential handoff, SSH verification |
| Lifecycle | Timers, orphan detection, 12h hard max |
| Cost | Track hourly costs, per-consumer, alerts |

## Safety Rules

1. **12-hour hard max** - requires CLI override to extend
2. **Orphan detection** - alert if running past reservation
3. **Never leave running** - aggressive cleanup on shutdown

## API Endpoints

```
GET  /api/v1/inventory          # List available GPUs
POST /api/v1/sessions           # Provision a session
GET  /api/v1/sessions/{id}      # Get session details
POST /api/v1/sessions/{id}/done # Signal completion
DELETE /api/v1/sessions/{id}    # Force shutdown
GET  /api/v1/costs              # Cost summary
```

## CLI Commands

```bash
gpu-shopper inventory           # List available
gpu-shopper provision           # Provision a GPU
gpu-shopper sessions            # List active
gpu-shopper shutdown <id>       # Force shutdown
gpu-shopper costs               # Show costs
```

## Development

```bash
# Run tests
go test ./...

# Run server
go run cmd/server/main.go

# Run CLI
go run cmd/cli/main.go inventory

# Build all
go build ./cmd/...
```

## Key Files

| File | Purpose |
|------|---------|
| `PRD_MVP.md` | What we're building |
| `ARCHITECTURE.md` | How it's structured |
| `PROGRESS.md` | Current status |
| `internal/provider/interface.go` | Provider contract |

## Environment Variables

```bash
VASTAI_API_KEY=xxx
TENSORDOCK_API_KEY=xxx
DATABASE_PATH=./data/gpu-shopper.db
```
