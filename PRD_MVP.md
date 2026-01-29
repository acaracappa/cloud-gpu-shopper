# Cloud GPU Shopper - MVP Product Requirements Document

**Version**: 1.0
**Date**: 2025-01-29
**Status**: Discovery In Progress

---

## Problem Statement

Applications that need on-demand GPU compute (LLM inference, ML training, batch jobs) face friction when provisioning cloud GPU resources: managing multiple provider APIs, tracking costs across sessions, ensuring instances don't run unattended, and handling credential handoff securely.

Cloud GPU Shopper solves this by providing a unified inventory and orchestration layer over commodity GPU providers (Vast.ai, TensorDock), acting as a "menu and provisioner" that selects, provisions, and hands off GPU resources to consuming applications - then ensures they spin down properly.

---

## Target User

**Primary**: Applications/services that need on-demand GPU compute
- Call via REST API
- Browse available inventory ("menu")
- Request provisioning with workload requirements
- Receive credentials and API access to provisioned node
- Signal completion when done

**Secondary**: Human operators via CLI
- Inspect current inventory
- Manual provisioning for debugging/testing
- Monitor active sessions and costs
- Force shutdown orphaned instances

---

## Success Metrics

### Primary Metric
**Zero orphaned instances** - no GPU left running unattended beyond its reservation

### Secondary Metrics
- Provisioning success rate (target: >95%)
- Time from request to ready (target: <3 minutes)
- Cost tracking accuracy (±1% of actual provider billing)
- API uptime (target: >99%)

---

## Core Use Cases (MVP Scope)

### UC1: Browse Available Inventory
**Application can** query the menu of available GPU nodes with:
- GPU type (A100, 4090, H100, etc.)
- VRAM amount
- Price per hour
- Location/region
- Reliability score (if available)
- Provider (Vast.ai, TensorDock)
- Estimated availability duration

### UC2: Provision a GPU Node
**Application can** request provisioning by:
- Selecting an inventory item
- Specifying reservation duration (1-12 hours, extendable with override)
- Providing workload type (LLM hosting, ML training, batch job)
- Receiving back: SSH credentials, node API endpoint + token, session ID

### UC3: Interact with Provisioned Node
**Application can** via the node API:
- Send requests to hosted LLM
- Transfer files to/from the node
- Send commands to ML training process
- Query node status and resource usage

### UC4: Signal Completion
**Application can** signal "done" to:
- Trigger graceful shutdown
- Mark associated storage as preserve/destroy
- Release the reservation
- Finalize cost tracking for the session

### UC5: Track Costs
**Application can** query:
- Current session cost
- Cost per consumer/application
- Hourly/daily/monthly aggregates
- Budget limit status and alerts

### UC6: CLI Operations
**Operator can** via CLI:
- List current inventory (`gpu-shopper inventory`)
- Provision manually (`gpu-shopper provision --gpu=4090 --hours=2`)
- List active sessions (`gpu-shopper sessions`)
- Check costs (`gpu-shopper costs --consumer=atlas`)
- Force shutdown (`gpu-shopper shutdown <session-id>`)

---

## Out of Scope (v1)

| Feature | Reason |
|---------|--------|
| Providers beyond Vast.ai + TensorDock | Future release |
| Web dashboard | CLI + API sufficient for MVP |
| Multi-tenant auth | Single deployment, consumer identification via API key |
| Spot instance bidding | Use fixed pricing for predictability |
| GPU sharing / fractional allocation | Full node allocation only |
| Persistent storage management | Mark preserve/destroy, but actual storage lifecycle is consumer's responsibility |

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                    CONSUMING APPLICATIONS                        │
│         (Atlas, ML Pipelines, LLM Services, etc.)               │
└─────────────────────────────────────────────────────────────────┘
                              │
                    REST API + CLI
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     CLOUD GPU SHOPPER                            │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────────┐ │
│  │   Inventory  │  │  Provisioner │  │   Lifecycle Manager    │ │
│  │    Service   │  │              │  │  - Reservation timer   │ │
│  │  - Vast.ai   │  │  - Deploy    │  │  - Orphan detection    │ │
│  │  - TensorDock│  │  - Creds     │  │  - Idle detection      │ │
│  │  - Cache     │  │  - Handoff   │  │  - 12h hard max        │ │
│  └──────────────┘  └──────────────┘  └────────────────────────┘ │
│  ┌──────────────┐  ┌──────────────────────────────────────────┐ │
│  │    Cost      │  │         Provider Adapters                │ │
│  │   Tracker    │  │  ┌───────────┐  ┌─────────────────────┐  │ │
│  │  - Hourly    │  │  │  Vast.ai  │  │     TensorDock      │  │ │
│  │  - Per-app   │  │  └───────────┘  └─────────────────────┘  │ │
│  │  - Alerts    │  └──────────────────────────────────────────┘ │
│  └──────────────┘                                               │
└─────────────────────────────────────────────────────────────────┘
                              │
                    Provider APIs (SSH + container deploy)
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      GPU NODE (Provisioned)                      │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  Node Agent Container (our Docker image)                    │ │
│  │  - REST API for consumer access                            │ │
│  │  - LLM proxy / file transfer / ML commands                 │ │
│  │  - Heartbeat to Shopper                                    │ │
│  │  - Idle detection                                          │ │
│  └────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

---

## Technical Requirements

### Stack
| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Go | Efficient, good concurrency, easy containerization |
| API | REST (gin/echo) | Simple, well-understood |
| CLI | cobra | Standard Go CLI framework |
| Database | SQLite | Portable, no external deps for MVP |
| Container | Docker | Standard, works with providers |
| Node Agent | Go binary in Docker | Lightweight, same language |

### Provider Integration
| Provider | API | Auth |
|----------|-----|------|
| Vast.ai | REST API | API key |
| TensorDock | REST API | API key |

### Lifecycle Rules
| Rule | Behavior |
|------|----------|
| Hard max duration | 12 hours, requires explicit override |
| Reservation timer | Check job completion at reservation end |
| Orphan detection | Alert if node running past reservation without extension |
| Idle detection | Configurable per-workload type |
| Graceful shutdown | On "done" signal, cleanup and deprovision |
| Override approval | Human-in-the-loop for extensions beyond 12h |

### Cost Tracking
| Metric | Granularity |
|--------|-------------|
| Session cost | Per-session, real-time |
| Consumer cost | Per API key / consumer ID |
| Time aggregates | Hourly, daily, monthly, total |
| Budget alerts | Configurable thresholds, webhook to consumer |

---

## API Endpoints (Draft)

### Inventory
- `GET /api/v1/inventory` - List available GPU nodes
- `GET /api/v1/inventory?gpu=4090&min_vram=24` - Filtered search

### Sessions
- `POST /api/v1/sessions` - Provision a new GPU session
- `GET /api/v1/sessions` - List active sessions
- `GET /api/v1/sessions/{id}` - Get session details
- `POST /api/v1/sessions/{id}/done` - Signal completion
- `POST /api/v1/sessions/{id}/extend` - Request extension (may require override)
- `DELETE /api/v1/sessions/{id}` - Force shutdown

### Costs
- `GET /api/v1/costs` - Get cost summary
- `GET /api/v1/costs?consumer={id}` - Per-consumer costs
- `POST /api/v1/budgets` - Set budget alert threshold

### Node Agent (runs on GPU node)
- `POST /node/v1/llm/chat` - Send LLM request
- `POST /node/v1/files/upload` - Upload file
- `GET /node/v1/files/download/{path}` - Download file
- `POST /node/v1/ml/command` - Send ML training command
- `GET /node/v1/status` - Node health and resource usage

---

## CLI Commands (Draft)

```bash
# Inventory
gpu-shopper inventory                     # List all available
gpu-shopper inventory --gpu=4090          # Filter by GPU type
gpu-shopper inventory --max-price=0.50    # Filter by price

# Sessions
gpu-shopper provision --gpu=4090 --hours=2 --consumer=atlas
gpu-shopper sessions                      # List active
gpu-shopper session <id>                  # Details
gpu-shopper shutdown <id>                 # Force shutdown
gpu-shopper extend <id> --hours=2         # Extend (may prompt for override)

# Costs
gpu-shopper costs                         # Summary
gpu-shopper costs --consumer=atlas        # Per-consumer
gpu-shopper costs --period=month          # Monthly total

# Config
gpu-shopper config set vastai-key <key>
gpu-shopper config set tensordock-key <key>
```

---

## Decisions Made

| Question | Decision |
|----------|----------|
| Node agent scope | Full API: LLM proxy, file transfer, ML commands, heartbeat, idle detection |
| Budget alerts | Both: webhook for real-time, API/CLI for querying |
| Override approval | CLI prompt - operator must run command to approve extension |
| Inventory cache TTL | Adaptive: 1 minute ideal, fallback to 5 minutes if rate limited |

---

## MVP Definition of Done

MVP is complete when:
- [ ] Can query inventory from Vast.ai and TensorDock
- [ ] Can provision a GPU node via API and CLI
- [ ] Provisioned node runs our agent container
- [ ] Consumer receives credentials and can access node API
- [ ] Can signal "done" and node spins down gracefully
- [ ] Cost tracking works with hourly granularity
- [ ] 12-hour hard max enforced with override mechanism
- [ ] Orphan detection alerts when node outlives reservation
- [ ] Idle detection available (configurable)
- [ ] Full unit test coverage
- [ ] Integration tests against provider APIs (mocked for CI, real for validation)
- [ ] CLI tool functional for all core operations
- [ ] Runs in container
- [ ] Documentation: README, API docs, CLI help

---

*Document generated from Product Discovery Session on 2025-01-29*
