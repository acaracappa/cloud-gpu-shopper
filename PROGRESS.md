# Cloud GPU Shopper - Progress Tracker

## Current Status

**Phase**: Post-MVP Feature Development
**Status**: Active
**Date**: 2026-02-07

### Summary
- MVP complete with all safety rules, E2E tests, and live testing
- Feb 2026: Added auto-retry, global failure tracking, benchmarking infrastructure, disk estimation, Docker multi-arch builds
- Active bug tracking for provider-specific issues (RTX 5080 driver incompatibility)
- TensorDock inventory quality improved with aggressive confidence tuning and recency penalty

---

## Known Issues

| ID | Description | Status | Notes |
|----|-------------|--------|-------|
| BUG-003 | `.env` not loading provider creds | FIXED | `mapEnvFileKeys()` |
| BUG-004 | CUDA version mismatch | Provider-side — Won't Fix | Provider-side data issue; `min_cuda` filter works around it |
| BUG-005 | SSH timeout too short for heavy templates | FIXED | Client-configurable `ssh_timeout_minutes` (1-30 min) |
| BUG-006 | pip vLLM incompatible with CUDA 13.0 | Provider-side — Won't Fix | Use Docker template instead |
| BUG-008 | TensorDock port_forwards ignored | FIXED | Use `useDedicatedIp: true` |
| BUG-009 | ALL TensorDock VMs missing nvidia drivers (systemic) | Fixed | Cloud-init: dpkg fix + DKMS build + modprobe |
| BUG-010 | TensorDock "No available public IPs on hostnode" | Provider-side — Won't Fix | Stale inventory; confidence scoring suppresses bad offers |
| BUG-011 | TensorDock H100 SXM5 stops immediately | Provider-side — Won't Fix | Global failure tracking auto-suppresses offer |
| BUG-012 | RTX 5080 on Vast.ai can't provision | Provider-side — Won't Fix | Driver incompatibility; GPU-type degradation after 3+ failures |
| BUG-013 | TensorDock nvidia packages in `iU` state (DKMS never built) | Fixed | Cloud-init runs `dpkg --configure -a` + DKMS build/install |
| BUG-014 | dpkg lock contention on TensorDock VMs (2-5 min) | Fixed | Cloud-init kills unattended-upgrades, waits for lock, `DPkg::Lock::Timeout=120` |
| BUG-015 | deepseek-r1:14b cold-start >2 min (CUDA JIT) | Provider-side — Won't Fix | Inherent to CUDA JIT; benchmark script uses warmup request |
| BUG-016 | RTX 5000 ADA / RTX 5090 Chubbuck intermittent SSH/PCIe | Provider-side — Won't Fix | Hardware issue; failure tracker auto-suppresses |
| BUG-017 | Vast.ai "loading" state killed during image pull | FIXED | Added "loading" to transient states allowlist in provisioner |
| BUG-024 | No workload_type or retry_scope validation | FIXED | Validation in handlers.go + models/session.go |
| BUG-040 | Unsanitized X-Request-ID + user input in errors | FIXED | sanitizeInput() + requestID regex validation |
| BUG-047 | CLI nil map crash in sessions extend command | FIXED | Nil check before map access in sessions.go |
| BUG-064 | Cost tracker records $0 for short-lived sessions | FIXED | RecordFinalCost + CostRecorder interface in provisioner |

## Backlog

### Outstanding (Medium Priority)

_None_

### Outstanding (Low Priority)

_None_

### Deferred

_None_

### Completed (QA Remediation)
| ID | Issue | Status |
|----|-------|--------|
| C1 | Vast.ai SSH key attachment error silenced | Fixed |
| C2 | Orphan instance on DB failure | Fixed |
| C3 | Race condition in lifecycle Start/Stop | Fixed |
| C5 | ListSessions returns empty list | Fixed |
| H1 | Missing rows.Err() after SQL loops | Fixed |
| H2 | Nil panic in handleExtendSession | Fixed |
| H3 | Goroutine leak (context.Background) | Fixed |
| H4 | Integer overflow in backoff | Fixed |
| H5 | Error mapping always 404 | Fixed |
| H7 | Ready field race condition | Fixed |
| H8 | Viper BindEnv errors ignored | Fixed |
| H10 | DeploymentID not persisted | Fixed |
| M2 | Panic recovery missing stack trace | Fixed |
| M4 | No request body size limit | Fixed |
| M5 | Date parsing errors silently ignored | Fixed |
| L1 | Unused printError function | Fixed |
| L3 | Custom joinStrings instead of strings.Join | Fixed |
| M1 | TensorDock SSH wait magic number | Fixed |
| M3 | Cost aggregation duplicates | Fixed |
| M6 | SSH InsecureIgnoreHostKey not logged | Fixed |
| M7 | Missing HTTP server request metrics | Fixed |
| M8 | Inconsistent logger usage (TensorDock) | Fixed |
| M9 | Large functions need refactoring | Fixed |
| M10 | No context timeout on inventory | Fixed |
| L2 | Duplicate Session struct in CLI | Fixed |
| L4 | Magic strings for workload types | Fixed |
| L5 | Missing --gpu filter in CLI provision | Fixed |
| L6 | Error variable shadowing | Verified - none found |
| D1 | CLI integration tests | Fixed |
| D2 | Agent basic file transfer | Fixed |

---

## Implementation History

### Option B: Full MVP Plan

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
- [x] Implement inventory service
- [x] Adaptive caching (1min default, 5min backoff)
- [x] Filtering and search
- [x] Provider aggregation
- [x] Unit tests with mocked providers

#### Phase 2B: Storage Layer (2-3 hours)
- [x] SQLite setup with WAL mode
- [x] Sessions repository (CRUD + queries)
- [x] Costs repository
- [x] Database migrations
- [x] Unit tests

#### Phase 2C: Provisioner Service (3-4 hours)
- [x] Two-phase provisioning (per ARCHITECTURE.md)
- [x] SSH key generation
- [x] Instance tagging
- [x] Agent environment configuration
- [x] Heartbeat waiting logic
- [x] Verified destruction loop
- [x] Unit tests with mocked providers

---

### Day 3: Lifecycle + Safety Systems

#### Phase 3A: Lifecycle Manager (3-4 hours)
- [x] Timer-based check loop
- [x] Reservation expiry handling
- [x] 12-hour hard max enforcement
- [x] Heartbeat timeout detection
- [x] Graceful shutdown on "done" signal
- [x] Unit tests with time mocking

#### Phase 3B: Reconciliation System (2-3 hours)
- [x] Provider reconciliation job (every 5 min)
- [x] Orphan detection and auto-destroy
- [x] Ghost detection and status update
- [x] Startup recovery procedure
- [x] Unit tests

#### Phase 3C: Cost Tracker (2 hours)
- [x] Session-level cost calculation
- [x] Per-consumer aggregation
- [x] Daily/monthly summaries
- [x] Budget threshold checking
- [x] Unit tests

---

### Day 4: API + CLI

#### Phase 4A: REST API (3-4 hours)
- [x] Gin router setup
- [x] Inventory endpoints (GET /inventory, GET /inventory/:id)
- [x] Session endpoints (POST, GET, DELETE /sessions, POST /sessions/:id/done, POST /sessions/:id/extend)
- [x] Cost endpoints (GET /costs, GET /costs/summary)
- [x] Health endpoint
- [x] Middleware (logging, recovery, request ID)
- [x] Integration tests

#### Phase 4B: CLI Tool (2-3 hours)
- [x] Cobra setup
- [x] `gpu-shopper inventory` command
- [x] `gpu-shopper provision` command
- [x] `gpu-shopper sessions` command (list, get, done, extend, delete)
- [x] `gpu-shopper shutdown` command
- [x] `gpu-shopper costs` command (with summary subcommand)
- [x] `gpu-shopper config` command (show, set)

#### Phase 4C: Observability (1-2 hours)
- [x] Prometheus metrics endpoint (/metrics)
- [x] Critical safety metrics (SessionsActive, OrphansDetected, DestroyFailures, ReconciliationMismatches, HeartbeatAge, ProviderAPIErrors + extras)
- [x] Structured logging for all operations
- [x] Audit log for provisions/destructions (logging.Audit() calls in provisioner, reconciler, lifecycle manager)

---

### Day 5: Agent + Integration

#### Phase 5A: Node Agent (3-4 hours)
- [x] Agent main with self-destruct timer
- [x] Heartbeat sender (30s interval)
- [x] Shopper-unreachable failsafe
- [x] Health/status endpoint
- [x] Agent Dockerfile
- [x] Unit tests

#### Phase 5B: Integration Testing (2-3 hours)
- [x] Docker-compose for local testing
- [x] Mock provider server (test/mockprovider/)
- [x] End-to-end provision/destroy test
- [x] Orphan detection test
- [x] Ghost detection test
- [x] Hard max enforcement test
- [x] Idle shutdown test

#### Phase 5C: Documentation + Polish (1-2 hours)
- [x] README.md with quickstart
- [x] API documentation (docs/API.md)
- [x] CLI help text (built-in via Cobra)
- [x] Deployment guide (in README + docker-compose)
- [x] Server Dockerfile

---

## Safety Checklist (Must Complete)

Before considering MVP done, verify:

- [x] Two-phase provisioning implemented
- [x] Destroy verification loop working
- [x] Instance tagging on all provisions
- [x] Provider reconciliation running every 5 min
- [x] Startup recovery tested (unit tested)
- [x] Agent self-destruct timer working
- [x] Agent shopper-unreachable failsafe working
- [x] 12-hour hard max enforced
- [x] Orphan detection alerts firing
- [x] All critical metrics exposed

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

### 2025-01-29 - Day 2 Implementation (Phase 2A)
- Implemented Inventory Service (internal/service/inventory/)
  - Provider aggregation with concurrent fetching
  - Adaptive caching (1min default, 5min backoff on errors)
  - Filtering by provider, GPU type, VRAM, price, GPU count
  - Results sorted by price (lowest first)
  - Cache invalidation and status monitoring
  - GetOffer for specific offer lookup
- 23 unit tests with mocked providers covering:
  - Single/multi-provider fetching
  - All filter types
  - Cache hit/miss/expiry
  - Backoff behavior on errors
  - Partial failures (some providers down)
  - Concurrent access safety
  - Context cancellation
- All 74 tests passing across all packages

### 2025-01-29 - Day 2 Implementation (Phase 2C)
- Implemented Provisioner Service (internal/service/provisioner/)
  - Two-phase provisioning with crash recovery:
    - Phase 1: Create session record in DB (survives crashes)
    - Phase 2: Call provider to create instance
    - Phase 3: Update session with provider info
    - Phase 4: Async wait for agent heartbeat
  - RSA SSH key pair generation (4096-bit)
  - Instance tagging with session, deployment, expiry, consumer IDs
  - Agent environment configuration
  - Heartbeat waiting logic with configurable timeout
  - Verified destruction loop with retries
  - Provider registry for multi-provider support
- 21 unit tests covering:
  - Session creation with all configurations
  - SSH key generation and format validation
  - Instance tagging and agent env setup
  - Provider errors and not found scenarios
  - Destroy verification with retries
  - Heartbeat recording
- All 95 tests passing across all packages

### 2025-01-29 - Day 3 Implementation
- Implemented Lifecycle Manager (internal/service/lifecycle/manager.go)
  - Timer-based check loop with configurable interval
  - Reservation expiry handling with auto-destroy
  - 12-hour hard max enforcement (with override support)
  - Heartbeat timeout detection
  - Session extension and done signaling
  - Event handler interface for monitoring
  - 16 unit tests with time mocking
- Implemented Reconciliation System (internal/service/lifecycle/reconciler.go)
  - Provider reconciliation (compares DB vs provider state)
  - Orphan detection (provider instance without DB record) with auto-destroy
  - Ghost detection (DB record without provider instance) with status fix
  - Startup recovery for stuck sessions (provisioning/stopping)
  - Multi-provider support
  - 14 unit tests
- Implemented Cost Tracker (internal/service/cost/tracker.go)
  - Hourly cost recording for running sessions
  - Daily and monthly summaries
  - Budget threshold checking (warning at 80%, exceeded at 100%)
  - Alert sending interface for notifications
  - Consumer spend tracking
  - 13 unit tests
- All 138 tests passing across all packages

### 2025-01-29 - Day 4 Implementation (Phase 4A + 4B)
- Implemented REST API (internal/api/)
  - Gin router with versioned routes (/api/v1/...)
  - Inventory endpoints: list with filters, get by ID
  - Session endpoints: create, get, list, delete, done, extend
  - Cost endpoints: get costs with various query params, monthly summary
  - Health endpoint with service status
  - Middleware: request ID, structured logging, panic recovery
  - Integration tests with httptest
- Implemented CLI Tool (cmd/cli/)
  - Cobra-based command structure
  - `gpu-shopper inventory` - list/filter available GPUs
  - `gpu-shopper provision` - create new session
  - `gpu-shopper sessions [list|get|done|extend|delete]` - manage sessions
  - `gpu-shopper shutdown` - graceful/force shutdown
  - `gpu-shopper costs` - view costs with summary subcommand
  - `gpu-shopper config` - view/set configuration
  - JSON and table output formats
- Fixed server main.go to use correct config/logging APIs
- All 138 tests passing

### 2025-01-29 - Day 4 Implementation (Phase 4C)
- Implemented Observability (internal/metrics/)
  - Added Prometheus client dependency
  - Created metrics package with 13 critical safety metrics:
    - SessionsActive (gauge by provider/status)
    - OrphansDetected, GhostsDetected (counters)
    - DestroyFailures (counter)
    - ReconciliationMismatches (counter)
    - HeartbeatAge (gauge by session_id)
    - ProviderAPIErrors (counter by provider/operation)
    - SessionsCreated, SessionsDestroyed (counters)
    - HardMaxEnforced (counter)
    - ProvisioningDuration (histogram)
    - CostAccrued, BudgetAlerts (counters)
  - Added /metrics endpoint to API server using promhttp
- Added Audit Logging to critical operations:
  - Provisioner: session_provisioned, session_destroyed
  - Reconciler: orphan_detected, orphan_destroyed, ghost_detected, ghost_fixed
  - Lifecycle: hard_max_enforced, session_expired, heartbeat_timeout
- All 138 tests passing
- Day 4 complete - Ready for QA verification before Day 5

### 2026-01-29 - Day 5 Implementation (Phase 5A)
- QA Review completed: 172 tests passed
  - Excellent unit test quality with proper mocks
  - Good safety-critical coverage (hard-max, destroy verification, orphan detection)
  - Areas for improvement: integration tests, CLI tests, chaos testing
- Implemented Node Agent (cmd/agent/, agent/)
  - Self-destruct timer with configurable grace period (default 30min after expiry)
  - Heartbeat sender with 30s interval to shopper service
  - Shopper-unreachable failsafe (triggers after 60 consecutive failures = 30min)
  - Health (/health) and status (/status) endpoints
  - Environment-based configuration (SHOPPER_SESSION_ID, SHOPPER_AGENT_TOKEN, etc.)
- Added heartbeat endpoint to API server (POST /api/v1/sessions/:id/heartbeat)
- 17 agent tests covering:
  - Heartbeat sending and failure counting
  - Failsafe triggering after threshold
  - API health/status endpoints
  - Server shutdown
- All 174 tests passing

### 2026-01-29 - Day 5 Implementation (Phase 5B + 5C)
- Created Docker infrastructure:
  - deploy/Dockerfile.agent - Multi-stage build for agent
  - deploy/Dockerfile.server - Multi-stage build with CGO for SQLite
  - deploy/docker-compose.yml - Local dev environment with optional monitoring
  - deploy/prometheus.yml - Prometheus scrape config
- All binaries building successfully
- MVP Core Complete - Ready for integration testing and documentation

### 2026-01-29 - E2E Testing Sprint (GPU Monitoring, Idle Shutdown & E2E Testing)

#### Day 1: GPU Utilization Monitoring (Agent Side)
- [x] Created GPU monitor package (agent/gpumon/monitor.go)
  - nvidia-smi parsing with timeout handling
  - Graceful fallback when nvidia-smi unavailable
  - 9 unit tests covering parsing, errors, context cancellation
- [x] Created idle detector package (agent/idle/detector.go)
  - Tracks consecutive low-GPU-utilization samples
  - Configurable threshold (default 5%)
  - Thread-safe implementation
  - 8 unit tests including race condition testing
- [x] Updated agent main to use real GPU metrics

#### Day 2: Idle Detection Auto-Shutdown (Server Side)
- [x] Extended session storage with last_idle_seconds column
- [x] Updated heartbeat flow to pass idle_seconds
- [x] Added checkIdleSessions() to lifecycle manager
- [x] Added UpdateHeartbeatWithIdle to session store
- [x] Added OnIdleShutdown event to lifecycle manager
- [x] Unit tests for idle detection behavior

#### Day 3: Mock Provider Server
- [x] Created mock provider server (test/mockprovider/server.go)
  - Vast.ai API compatible endpoints
  - GET /bundles/ - list offers
  - PUT /asks/{id}/ - create instance
  - GET /instances/ - list instances
  - DELETE /instances/{id}/ - destroy
- [x] In-memory state management (test/mockprovider/state.go)
  - Configurable delays and failures
  - Orphan instance creation for testing
- [x] Test control endpoints (/_test/reset, /_test/config, /_test/orphan)
- [x] 24 unit tests for mock provider

#### Day 4: E2E Provision/Destroy Tests
- [x] Created E2E test helpers (test/e2e/helpers_test.go)
  - TestEnv configuration
  - API helper methods
  - Wait helpers with timeout
- [x] Provision/destroy tests (test/e2e/provision_test.go)
  - TestProvisionDestroy - full lifecycle
  - TestProvisionWithStoragePolicy
  - TestProvisionWithIdleThreshold
  - TestSignalDone
  - TestExtendSession
  - TestMultipleSessions
- [x] Heartbeat tests (test/e2e/heartbeat_test.go)
  - TestHeartbeat - basic heartbeat
  - TestHeartbeatWithIdleTracking
  - TestHeartbeatInvalidToken - 401 on bad token
  - TestHeartbeatUpdatesTimestamp
  - TestMultipleHeartbeats
  - TestHeartbeatTransitionsToRunning

#### Day 5: Final E2E Tests
- [x] Orphan detection tests (test/e2e/orphan_test.go)
  - TestOrphanDetection - creates orphan, verifies auto-destroy
  - TestOrphanDetectionWithMultipleOrphans
  - TestLegitimateSessionNotOrphan
- [x] Ghost detection tests (test/e2e/ghost_test.go)
  - TestGhostDetection - session exists, instance deleted
  - TestGhostDetectionPreservesRunningInstances
  - TestMultipleGhostDetection
- [x] Hard-max tests (test/e2e/hardmax_test.go)
  - TestHardMaxOverride
  - TestSessionExpiresAtIsSet
  - TestSessionExtensionWithinLimits
  - TestReservationHoursPreserved
- [x] Idle shutdown tests (test/e2e/idle_test.go)
  - TestIdleThresholdConfigured
  - TestHeartbeatWithIdleSeconds
  - TestActiveSessionNotIdle
  - TestNoIdleThresholdMeansNoIdleShutdown
  - TestIdleHeartbeatSequence

#### Bugs Fixed During E2E Testing
- Fixed AgentToken not being persisted to database (heartbeat auth failures)
- Fixed session extension not updating expires_at in database
- Updated MockProviderAdapter to use mock provider HTTP API

#### Test Summary
- All 174+ unit tests passing
- All 24 mock provider tests passing
- All E2E tests passing (30+ test cases)
- Full session lifecycle tested end-to-end

### 2026-01-29 - Live Testing Infrastructure (Multi-Provider)
- Created live test infrastructure for real GPU servers
- Multi-provider support: Vast.ai + TensorDock
- Safety watchdog with spend/time limits:
  - $3.00 total budget, $1.50 per provider
  - 60 minutes total runtime, 30 minutes per provider
  - Automatic cleanup on exit/timeout/limit exceeded

#### Files Created
- test/live/config.go - Provider configuration and test settings
- test/live/watchdog.go - Safety watchdog with spend tracking
- test/live/helpers.go - Test utilities and API helpers
- test/live/live_test.go - Multi-provider live tests
- scripts/run-live-tests.sh - Test runner script

#### Live Tests
| Test | Vast.ai | TensorDock | Description |
|------|---------|------------|-------------|
| L0_CrossProvider_FindCheapest | ✓ | ✓ | Find cheapest GPU across providers |
| L1_ProvisionSmoke | ✓ | ✓ | Provision, verify SSH, destroy |
| L2_Heartbeat | ✓ | ✓ | Agent heartbeat verification |
| L3_Extension | ✓ | ✓ | Session extension |
| L4_GracefulShutdown | ✓ | ✓ | Graceful done signal |
| L5_CrossProvider_ProvisionBoth | ✓ | ✓ | Compare both providers |
| L6_ProviderFailover | ✓ | ✓ | Cross-provider selection |

#### Running Live Tests
```bash
# Set API keys
export VASTAI_API_KEY=xxx
export TENSORDOCK_API_KEY=xxx
export TENSORDOCK_ORG_ID=xxx

# Run live tests
./scripts/run-live-tests.sh
```

### 2026-01-31 - Test Suite Hardening Sprint

Comprehensive QA review identified 121 issues across all test repositories. Implemented "Risk-First Sprint" to eliminate critical safety hazards and establish race-free, deterministic tests.

#### Critical Issues Fixed (CRIT-1 through CRIT-5)

| ID | Issue | Fix |
|----|-------|-----|
| CRIT-1 | TestMain lifecycle cleanup ordering | Wrapper function pattern for proper defer execution |
| CRIT-2 | Race conditions in cost tracker tests | Already fixed (deep copy in mock stores) |
| CRIT-3 | Lifecycle manager Start/Stop race | Set `running=false` in run() defer before closing doneCh |
| CRIT-4 | SSH verification goroutine leak | Added WaitGroup tracking and `WaitForVerificationComplete()` |
| CRIT-5 | E2E orphan/ghost tests resource cleanup | `require.Eventually()` patterns, defer cleanup |

#### High Priority Issues Fixed (HIGH-1 through HIGH-3)

| ID | Issue | Fix |
|----|-------|-----|
| HIGH-1 | Time-based test determinism | All services already had time injection; added demonstration tests |
| HIGH-2 | Mock provider HTTP client timeout | `http.NewRequestWithContext` for context-based timeouts |
| HIGH-3 | CLI test global state pollution | `t.Cleanup()`, parallel-safe pure function tests |

#### Files Modified
- `test/e2e/setup_test.go` - TestMain wrapper pattern, MockProviderAdapter context handling
- `internal/service/lifecycle/manager.go` - Running flag race fix
- `internal/service/lifecycle/manager_test.go` - `require.Eventually()` usage, time injection tests
- `internal/service/provisioner/service.go` - Goroutine tracking, context timeout buffer
- `internal/service/provisioner/ssh_verification_test.go` - `require.Eventually()`, cleanup
- `internal/service/cost/tracker_test.go` - Time injection demonstration tests
- `test/e2e/orphan_test.go` - Polling patterns, cleanup
- `test/e2e/ghost_test.go` - Polling patterns, cleanup
- `test/mockprovider/state.go` - Data race fix in DestroyInstance
- `cmd/cli/cmd/cli_test.go` - Global state management, parallel-safe tests

#### Test Quality Patterns Established

1. **Race-free execution**: All tests pass with `go test -race`
2. **Deterministic waits**: `require.Eventually()` instead of `time.Sleep()`
3. **Proper cleanup**: `t.Cleanup()` and defer for LIFO resource release
4. **Time injection**: Services support `WithTimeFunc()` for controlled testing
5. **Goroutine tracking**: `WaitForVerificationComplete()` ensures no leaks

#### Verification
```bash
go test -race ./...                    # All pass
go test -tags=e2e -race ./test/e2e/... # All pass
go test -race -parallel 4 ./cmd/cli/... # All pass
```

### 2026-02-02 — Vast.ai Templates, Provisioning Refactor, SSH Fix

- Implemented Vast.ai template system: list, get, compatibility checking
- Template-aware provisioning: `template_hash_id` parameter auto-applies host requirements
- Exposed `cuda_version` field in GPUOffer, added `min_cuda` filter to API
- Fixed bundle cache merge (filtered queries no longer wipe unfiltered cache)
- Fixed SSH verification for Vast.ai entrypoint-mode instances
- Tested ollama and vLLM builds on Vast.ai

### 2026-02-04 — Post-Provision Diagnostics, Disk Estimation, Auto-Retry

#### Post-Provision Runtime Diagnostics
- Added disk space check and OOM detection after SSH verification
- Session diagnostics endpoint: `GET /api/v1/sessions/:id/diagnostics`
- New metric: `gpu_session_disk_available_gb`

#### Disk Estimation
- `disk_gb` parameter on `POST /api/v1/sessions` for estimated disk needs
- Filters out offers with insufficient disk during provisioning

#### Auto-Retry Feature
- `POST /api/v1/sessions` now supports `auto_retry`, `max_retries`, `retry_scope`
- **Sync retry**: `StaleInventoryError` in `CreateInstance` triggers immediate retry with comparable offer
- **Async retry**: SSH timeout / instance stopped triggers background retry
- **Scopes**: `same_gpu` (1.2x price), `same_vram` (1.5x price), `any` (cross-provider, 2x price)
- `FindComparableOffers()` on inventory service excludes failed offers, sorts by confidence then price
- Session model extended: `RetryCount`, `RetryParentID`, `RetryChildID`, `FailedOffers`
- New DB columns via idempotent `ALTER TABLE` migrations

#### Session Model Updates
- Added retry tracking fields to sessions table
- Extended `SessionStore` with retry-related queries

### 2026-02-05 — TensorDock Integration Fixes

- **BUG-008 FIXED**: TensorDock `port_forwards` parameter ignored → use `useDedicatedIp: true`
- **BUG-009**: TensorDock `ubuntu2404` image missing nvidia drivers → manual install + reboot
- Working location: Joplin, Missouri (RTX 4090, $0.44/hr)
- After driver install: CUDA 13.0, Driver 580.126.09

### 2026-02-06 — Benchmarking Infrastructure, Global Failure Tracking, Docker Multi-Arch

#### Benchmarking Infrastructure
- Created `internal/benchmark/` package: models, store, manifest, parser
- SQLite-backed benchmark storage with `benchmarks` table
- Benchmark manifest for reproducible GPU test runs
- Ollama-based benchmark runner with TPS parsing
- API endpoints: `GET /api/v1/benchmarks`, `POST /api/v1/benchmarks`, compare, recommendations
- CLI: `gpu-shopper benchmark run`, `gpu-shopper benchmark list`
- Comprehensive documentation: `docs/BENCHMARKING.md`

#### Benchmark Results (23 total runs)

| GPU | Provider | Model | TPS | $/M tokens | Status |
|-----|----------|-------|-----|-----------|--------|
| RTX 4090 | TensorDock | deepseek-r1:14b | 93.7 | $1.30 | Success |
| RTX 4090 | Vast.ai | deepseek-r1:14b | 84.0 | $0.73 | Success |
| RTX 3090 | TensorDock | deepseek-r1:14b | 55.5 | $1.50 | Success |
| RTX 3090 | Vast.ai | deepseek-r1:14b | 60.5 | $1.38 | Success |
| RTX 5090 | Vast.ai | deepseek-r1:14b | 125.2 | $0.53 | Success |
| A100 80GB | Vast.ai | deepseek-r1:14b | 83.0 | $1.21 | Success |
| RTX 5080 | Vast.ai | deepseek-r1:14b | — | — | FAILED (BUG-012) |

#### Global Offer Failure Tracking (BUG-010, BUG-011, BUG-012)
- In-memory `OfferFailureTracker` in inventory service
- Per-offer confidence degradation: `0.7^recentFailures`
- Offer suppression: 3 failures within 30min → hidden for 30min
- GPU-type-level aggregation: 3+ distinct offers fail → 0.3× multiplier
- Time-based decay: failures expire after 1 hour
- 3 failure recording points in provisioner: CreateInstance error, instance stopped, SSH timeout
- API endpoint: `GET /api/v1/offer-health`
- Prometheus metric: `gpu_offer_failures_total`
- 14 unit tests

#### Docker Multi-Arch
- Added `amd64/arm64` support to Docker build
- Container build workflow with `gofmt` check

#### Documentation
- CONTRIBUTING.md with development setup, testing, code style
- Enhanced README with comprehensive user guide and CLI reference
- Benchmark report with GPU comparisons and recommendations
- `docs/BENCHMARKING.md` — full benchmarking infrastructure docs

### 2026-02-06 — BUG-005 Fix + TensorDock Inventory Quality

#### BUG-005: Client-Configurable SSH Timeout
- Added `ssh_timeout_minutes` (1-30 min) to `POST /api/v1/sessions`
- Client override takes priority over template-recommended timeout
- Clamped to 1-30 minutes with validation
- No provisioner changes needed — existing `TemplateRecommendedSSHTimeout` path handles it

#### TensorDock Inventory Quality Improvements
- Default confidence lowered: 50% → 30% (reflects 80%+ failure rate)
- Minimum confidence floor lowered: 10% → 5%
- Decay window shortened: 1 hour → 30 minutes (recover faster, punish faster)
- Added `lastWasSuccess` tracking to `locationStats`
- Added recency penalty: if last attempt failed, confidence halved
- Net effect examples:
  - Fresh location: 30% (was 50%)
  - After 1 failure: 5% (was 10%)
  - 1 success + 1 failure (failure last): 12.5% (was 50%)
  - 3 successes, 0 failures: 100% (unchanged)

### 2026-02-06 — TensorDock-Only Benchmark Campaign + Bug Fixes

#### Benchmark Results (TensorDock)

31 attempts, 5 successful (29% success rate). 22 failed (71%).

| GPU | Model | TPS | $/M tokens | Price/hr | Location |
|-----|-------|-----|-----------|----------|----------|
| RTX 4090 | llama3.1:8b | 169.0 | $0.65 | $0.396 | Chubbuck ID |
| RTX A6000 | llama3.1:8b | 121.9 | $0.91 | $0.400 | Chubbuck ID |
| RTX 4090 | deepseek-r1:14b | 92.3 | $1.14 | $0.377 | Manassas VA |
| RTX 3090 | deepseek-r1:14b | 80.1 | $0.69 | $0.200 | Manassas VA |
| RTX A6000 | deepseek-r1:14b | 68.0 | $1.63 | $0.400 | Chubbuck ID |

Key finding: **RTX 3090 is best value** for deepseek-r1:14b at $0.69/M tokens ($0.200/hr).

#### Failure Analysis (22/31 = 71% failure rate)

| Stage | Count | Cause |
|-------|-------|-------|
| Provision (stale inventory) | 8 | BUG-010: "No available public IPs on hostnode" |
| SSH timeout | 6 | BUG-009/013/014: nvidia driver not loading, dpkg lock |
| Instance stopped | 4 | BUG-011: instance stops immediately after provisioning |
| SSH auth failure | 2 | BUG-016: RTX 5000 ADA / RTX 5090 Chubbuck PCIe issues |
| Benchmark timeout | 2 | BUG-015: CUDA JIT cold-start >2 min |

Working locations: **Manassas VA** (43% success), **Chubbuck ID** (50% success). All other locations failed.

#### New Bugs Discovered

- **BUG-013**: TensorDock nvidia packages in `iU` state — DKMS kernel module never built, `nvidia-smi` fails even though package is "installed"
- **BUG-014**: `unattended-upgrades` holds dpkg lock for 2-5 min after boot, causing `apt` to fail in cloud-init
- **BUG-015**: deepseek-r1:14b cold-start >2 min due to CUDA JIT compilation on first inference (not a code bug — inherent to model)
- **BUG-016**: RTX 5000 ADA / RTX 5090 in Chubbuck have intermittent PCIe passthrough failures (hardware issue, won't fix)

#### Bug Fixes

- **BUG-009/013/014 (combined)**: Rewrote `buildCloudInit()` nvidia driver sequence:
  1. Kill `unattended-upgrades` and wait for dpkg lock release
  2. Run `dpkg --configure -a` to fix partially-installed packages
  3. Try DKMS build/install first (handles `iU` state without full reinstall)
  4. Fall back to full `nvidia-driver-550` install if no DKMS package present
  5. Use `modprobe nvidia` instead of reboot
  6. All `apt` commands use `DPkg::Lock::Timeout=120` for remaining lock contention

### 2026-02-07 — High-Severity Bug Fixes + Cross-Provider Benchmark Campaign

#### Bug Fixes (5 High-Severity)

| Bug | Description | Fix |
|-----|-------------|-----|
| BUG-017 | Vast.ai "loading" state treated as fatal — all Vast.ai provisioning killed during Docker image pull | Added `"loading"` to transient states allowlist in `provisioner/service.go:893` |
| BUG-040 | Unsanitized X-Request-ID and user input reflected in error responses (XSS/log injection risk) | Added `validRequestIDRegex` in `server.go`, `sanitizeInput()` utility in `handlers.go` applied at 8 locations |
| BUG-024 | No validation on `workload_type` or `retry_scope` — any string accepted | Added `ValidWorkloadTypes` map + `IsValidRetryScope()` in `models/session.go`, validation in `handlers.go` |
| BUG-047 | CLI nil map crash when extending sessions | Added nil check before map access in `cmd/cli/cmd/sessions.go` |
| BUG-064 | Cost tracker records $0 for sessions that terminate before hourly aggregation | Added `RecordFinalCost()` to `cost/tracker.go`, `CostRecorder` interface in provisioner, called on destroy/fail |

Files modified: `internal/api/server.go`, `internal/api/handlers.go`, `internal/api/benchmark_handlers.go`, `pkg/models/session.go`, `cmd/cli/cmd/sessions.go`, `internal/service/cost/tracker.go`, `internal/service/provisioner/service.go`, `cmd/server/main.go`

#### Cross-Provider Benchmark Campaign (mistral:7b)

First successful Vast.ai provisioning after BUG-017 fix. 6 nodes across 2 providers, all using Ollama template for Vast.ai.

| GPU | Provider | Location | Avg TPS | $/hr | $/M tokens |
|-----|----------|----------|---------|------|-----------|
| RTX 4090 | TensorDock | Joplin, MO | 179.0 | $0.439 | $0.68 |
| RTX 4090 | TensorDock | Orlando, FL | 178.4 | $0.377 | $0.59 |
| RTX 4090 | TensorDock | Manassas, VA | 176.0 | $0.377 | $0.59 |
| RTX 5080 | Vast.ai | California, US | 168.4 | $0.118 | $0.19 |
| RTX 3090 | Vast.ai | Quebec, CA | 159.2 | $0.082 | $0.14 |
| RTX 5060 Ti | Vast.ai | Ohio, US | 89.3 | $0.069 | $0.21 |

Key findings:
- **Best value**: RTX 3090 on Vast.ai at **$0.14/M tokens** — 4x cheaper than TensorDock RTX 4090
- **Best throughput**: RTX 4090 on TensorDock at **179 TPS**
- **RTX 5080 sweet spot**: 168 TPS at $0.19/M tokens — excellent price/performance
- **RTX 5060 Ti**: Budget Blackwell card, only 89 TPS — half the 4090's speed
- **Vast.ai dramatically cheaper**: 3-4x cheaper per hour than TensorDock for equivalent performance
- **Vast.ai now works**: BUG-017 fix confirmed — all 3 Vast.ai nodes provisioned successfully via Ollama template
- **TensorDock reliability improved**: Orlando FL and Joplin MO confirmed as new working locations (previously only Manassas VA and Chubbuck ID)

#### Key Learnings

- **SSH keys are ephemeral**: Private keys returned only in POST /sessions response, not stored in DB. Must save immediately.
- **Vast.ai Ollama template**: Hash `a8a44c7363cbca20056020397e3bf072`, image `vastai/ollama:0.15.4`, 2-4 min image pull ("loading" phase)
- **Template-based provisioning**: Provider templates should be the baseline for spinning up nodes, not raw Docker images
- **TensorDock deployment failures**: Ottawa, Winnipeg, and Chubbuck all returned "unexpected error during deployment" — only Manassas, Orlando, and Joplin succeeded
