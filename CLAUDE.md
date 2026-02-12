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
GET    /health                          # Health check
GET    /ready                           # Readiness check
GET    /metrics                         # Prometheus metrics

GET    /api/v1/inventory                # List GPUs (?min_cuda=12.9&template_hash_id=...&provider=vastai)
GET    /api/v1/inventory/:id            # Get specific offer
GET    /api/v1/inventory/:id/compatible-templates

GET    /api/v1/templates                # List templates (?use_ssh=true&name=vllm)
GET    /api/v1/templates/:hash_id       # Get specific template

POST   /api/v1/sessions                 # Provision (supports template_hash_id, disk_gb, auto_retry)
GET    /api/v1/sessions                 # List sessions
GET    /api/v1/sessions/:id             # Get session (no private key - returned only at creation)
GET    /api/v1/sessions/:id/diagnostics # Post-provision runtime diagnostics
POST   /api/v1/sessions/:id/done        # Signal completion
POST   /api/v1/sessions/:id/extend      # Extend session
DELETE /api/v1/sessions/:id             # Force shutdown

GET    /api/v1/costs                    # Get costs
GET    /api/v1/costs/summary            # Monthly cost summary
GET    /api/v1/offer-health             # Offer failure tracking status

GET    /api/v1/benchmarks               # List benchmark results
GET    /api/v1/benchmarks/:id           # Get specific benchmark
POST   /api/v1/benchmarks               # Submit benchmark result
GET    /api/v1/benchmarks/best           # Best performing for model
GET    /api/v1/benchmarks/cheapest       # Most cost-effective for model
GET    /api/v1/benchmarks/compare        # Compare across hardware
GET    /api/v1/benchmarks/recommendations

POST   /api/v1/benchmark-runs           # Start automated benchmark run
GET    /api/v1/benchmark-runs/:id        # Get run status
DELETE /api/v1/benchmark-runs/:id        # Cancel run

POST   /api/v1/benchmark-schedules      # Create schedule
GET    /api/v1/benchmark-schedules       # List schedules
PUT    /api/v1/benchmark-schedules/:id   # Update schedule
DELETE /api/v1/benchmark-schedules/:id   # Delete schedule
```

## CLI Commands

```bash
gpu-shopper inventory              # List available GPUs
gpu-shopper provision              # Provision a GPU
gpu-shopper sessions               # List/get/done/extend/delete sessions
gpu-shopper shutdown <id>          # Force shutdown
gpu-shopper costs                  # Show costs
gpu-shopper benchmark              # Run/list benchmarks
gpu-shopper transfer               # Upload/download files via SCP
gpu-shopper cleanup-orphans        # Find/destroy orphan instances (works without server)
gpu-shopper config                 # View/set configuration
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
# Set in .env file (auto-loaded) or export to environment
VASTAI_API_KEY=xxx
TENSORDOCK_API_TOKEN=xxx
TENSORDOCK_AUTH_ID=xxx
DATABASE_PATH=./data/gpu-shopper.db
```
