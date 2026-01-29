# Cloud GPU Shopper - Progress Tracker

## Current Status

**Phase**: 1 - Foundation
**Status**: Planning Complete, Ready to Build
**Estimated Duration**: 3-5 days
**Date**: 2025-01-29

---

## Option B: Full MVP Plan

### Day 1: Foundation + Provider Spike

#### Phase 1A: Project Setup (2-3 hours)
- [x] Initialize go.mod with dependencies
- [x] Create directory structure per ARCHITECTURE.md
- [x] Set up configuration loading (viper)
- [x] Set up structured logging (slog)
- [x] Create SQLite database with migrations
- [x] Define core data models in `pkg/models/`

#### Phase 1B: Provider Interface + Spike (3-4 hours)
- [x] Define provider interface (`internal/provider/interface.go`)
- [x] Spike Vast.ai API - verify all required endpoints work
- [x] Spike TensorDock API - verify all required endpoints work
- [x] Document any API limitations discovered (see docs/PROVIDER_SPIKE.md)
- [x] Implement Vast.ai adapter with mocked tests
- [x] Implement TensorDock adapter with mocked tests

---

### Day 2: Core Services

#### Phase 2A: Inventory Service (2-3 hours)
- [ ] Implement inventory service
- [ ] Adaptive caching (1min default, 5min backoff)
- [ ] Filtering and search
- [ ] Provider aggregation
- [ ] Unit tests with mocked providers

#### Phase 2B: Storage Layer (2-3 hours)
- [ ] SQLite setup with WAL mode
- [ ] Sessions repository (CRUD + queries)
- [ ] Costs repository
- [ ] Consumers repository
- [ ] Database migrations
- [ ] Unit tests

#### Phase 2C: Provisioner Service (3-4 hours)
- [ ] Two-phase provisioning (per ARCHITECTURE.md)
- [ ] SSH key generation
- [ ] Instance tagging
- [ ] Agent environment configuration
- [ ] Heartbeat waiting logic
- [ ] Verified destruction loop
- [ ] Unit tests with mocked providers

---

### Day 3: Lifecycle + Safety Systems

#### Phase 3A: Lifecycle Manager (3-4 hours)
- [ ] Timer-based check loop
- [ ] Reservation expiry handling
- [ ] 12-hour hard max enforcement
- [ ] Heartbeat timeout detection
- [ ] Graceful shutdown on "done" signal
- [ ] Unit tests with time mocking

#### Phase 3B: Reconciliation System (2-3 hours)
- [ ] Provider reconciliation job (every 5 min)
- [ ] Orphan detection and auto-destroy
- [ ] Ghost detection and status update
- [ ] Startup recovery procedure
- [ ] Unit tests

#### Phase 3C: Cost Tracker (2 hours)
- [ ] Session-level cost calculation
- [ ] Per-consumer aggregation
- [ ] Daily/monthly summaries
- [ ] Budget threshold checking
- [ ] Unit tests

---

### Day 4: API + CLI

#### Phase 4A: REST API (3-4 hours)
- [ ] Gin router setup
- [ ] Inventory endpoints (GET /inventory)
- [ ] Session endpoints (POST, GET, DELETE /sessions)
- [ ] Cost endpoints (GET /costs)
- [ ] Health endpoint
- [ ] Middleware (logging, recovery, request ID)
- [ ] Integration tests

#### Phase 4B: CLI Tool (2-3 hours)
- [ ] Cobra setup
- [ ] `gpu-shopper inventory` command
- [ ] `gpu-shopper provision` command
- [ ] `gpu-shopper sessions` command
- [ ] `gpu-shopper shutdown` command
- [ ] `gpu-shopper costs` command
- [ ] `gpu-shopper config` command
- [ ] Integration tests

#### Phase 4C: Observability (1-2 hours)
- [ ] Prometheus metrics endpoint
- [ ] Critical safety metrics
- [ ] Structured logging for all operations
- [ ] Audit log for provisions/destructions

---

### Day 5: Agent + Integration

#### Phase 5A: Node Agent (3-4 hours)
- [ ] Agent main with self-destruct timer
- [ ] Heartbeat sender (30s interval)
- [ ] Shopper-unreachable failsafe
- [ ] Health/status endpoint
- [ ] Basic file transfer (if time permits)
- [ ] Agent Dockerfile
- [ ] Unit tests

#### Phase 5B: Integration Testing (2-3 hours)
- [ ] Docker-compose for local testing
- [ ] Mock provider server
- [ ] End-to-end provision/destroy test
- [ ] Orphan detection test
- [ ] Hard max enforcement test

#### Phase 5C: Documentation + Polish (1-2 hours)
- [ ] README.md with quickstart
- [ ] API documentation
- [ ] CLI help text
- [ ] Deployment guide
- [ ] Server Dockerfile

---

## Safety Checklist (Must Complete)

Before considering MVP done, verify:

- [ ] Two-phase provisioning implemented
- [ ] Destroy verification loop working
- [ ] Instance tagging on all provisions
- [ ] Provider reconciliation running every 5 min
- [ ] Startup recovery tested
- [ ] Agent self-destruct timer working
- [ ] Agent shopper-unreachable failsafe working
- [ ] 12-hour hard max enforced
- [ ] Orphan detection alerts firing
- [ ] All critical metrics exposed

---

## Blocked / Issues

_None currently_

---

## Session Log

### 2025-01-29 - Planning Session
- Conducted product discovery interview
- Created PRD_MVP.md
- Created ARCHITECTURE.md
- Set up prompts directory with agents
- Ran round-robin architectural review (3 agents)
- Identified critical safety gaps
- Added required safety systems to architecture
- Refreshed plan for Option B (Full MVP)
- Ready to begin implementation

### 2025-01-29 - Day 1 Implementation
- Initialized Go project with all dependencies
- Created full directory structure
- Defined core data models (GPUOffer, Session, CostRecord, InstanceTags)
- Defined provider interface with error types
- Spiked both provider APIs:
  - Vast.ai: Works, uses Bearer token auth, /bundles/ endpoint
  - TensorDock: Works with v2 API, query param auth, /locations endpoint
  - Documented findings in docs/PROVIDER_SPIKE.md
- Implemented Vast.ai adapter with full test coverage
- Implemented TensorDock adapter with full test coverage
- Completed optional Day 1 items:
  - Configuration loading with Viper (internal/config/)
  - Structured logging with slog (internal/logging/)
  - SQLite storage layer with WAL mode (internal/storage/)
    - DB connection and migrations
    - Sessions repository (Create, Get, Update, List, GetActive, GetExpired)
    - Costs repository (Record, GetSessionCost, GetConsumerCost, GetSummary)
- All 51 tests passing across all packages
