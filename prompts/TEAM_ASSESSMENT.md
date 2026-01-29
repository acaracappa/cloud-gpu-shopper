# Agent Team Assessment - Cloud GPU Shopper

## Current Team (From Project Atlas)

| Agent | Relevance | Recommendation |
|-------|-----------|----------------|
| **Product Designer** | High | **KEEP** - Already adapted for this project |
| **Architect** | High | **KEEP** - Already adapted for this project |
| **Vast.ai Agent** | High | **ADAPT** - Core GPU orchestration knowledge, needs Go focus |
| **Infra Agent** | High | **ADAPT** - Docker/CI patterns apply, update for Go tooling |
| **QA Agent** | Medium | **ADAPT** - Testing principles apply, update for Go tooling |
| **Project Manager** | Medium | **KEEP AS-IS** - Process guidance is language-agnostic |
| **LLM Agent** | Low | **DROP** - LLM deployment is consumer's concern, not ours |
| **Control Plane Agent** | Low | **DROP** - Python/FastAPI focused |
| **Data Pipeline Agent** | None | **DROP** - Financial data pipelines not relevant |
| **Python ML Agent** | None | **DROP** - ML training is consumer's concern |
| **Dashboard Agent** | None | **DROP** - Deprecated, Streamlit-focused |
| **Frontend Agent** | None | **DROP** - No frontend in this project |

---

## Recommended Team for Cloud GPU Shopper

### Keep & Adapt (Already created or easy to port)

| Agent | Status | Notes |
|-------|--------|-------|
| Product Designer | ✅ Created | `prompts/PRODUCT_DESIGNER_AGENT.md` |
| Architect | ✅ Created | `prompts/ARCHITECT_AGENT.md` |

### Need to Create (New agents for this project)

| Agent | Priority | Role |
|-------|----------|------|
| **Go Backend Agent** | P0 | Core service implementation in Go |
| **Provider Agent** | P0 | Vast.ai + TensorDock integration specialist |
| **Infra Agent** | P1 | Docker, CI/CD, deployment |
| **QA Agent** | P1 | Go testing, integration tests |
| **Node Agent Specialist** | P2 | Agent binary that runs on GPU nodes |

---

## New Agent Definitions Needed

### 1. GO_BACKEND_AGENT.md (P0 - Create Now)

**Role**: Go Backend Engineer building the core Cloud GPU Shopper service.

**Key Skills**:
- Go (1.21+), Gin web framework, Cobra CLI
- SQLite with database/sql
- Structured logging (slog)
- Interface-based design for testability
- Goroutines and channels for concurrency
- Context-based cancellation

**Owns**:
- `cmd/server/` - API server
- `cmd/cli/` - CLI tool
- `internal/api/` - HTTP handlers
- `internal/inventory/` - Inventory service
- `internal/provisioner/` - Provisioning service
- `internal/lifecycle/` - Lifecycle manager
- `internal/cost/` - Cost tracking
- `internal/storage/` - SQLite repositories
- `pkg/models/` - Shared data models

---

### 2. PROVIDER_AGENT.md (P0 - Create Now)

**Role**: Integration Engineer specializing in GPU cloud provider APIs.

**Key Skills**:
- REST API integration
- Rate limiting and backoff strategies
- Response parsing and error handling
- Provider-specific quirks (Vast.ai, TensorDock)
- SSH key management
- Instance tagging strategies

**Owns**:
- `internal/provider/interface.go` - Provider contract
- `internal/provider/vastai/` - Vast.ai adapter
- `internal/provider/tensordock/` - TensorDock adapter

**Critical Knowledge**:
- Vast.ai API: https://vast.ai/docs/api/commands
- TensorDock API: https://documenter.getpostman.com/view/18850457/2s93JzM1Dq
- Instance tagging for orphan recovery
- Verified destruction patterns

---

### 3. INFRA_AGENT.md (P1 - Create Soon)

**Role**: DevOps Engineer for containerization and deployment.

**Key Skills**:
- Docker multi-stage builds for Go
- Docker Compose for local development
- GitHub Actions for Go CI/CD
- SQLite volume management
- Health checks and readiness probes

**Owns**:
- `deploy/Dockerfile.server`
- `deploy/Dockerfile.agent`
- `deploy/docker-compose.yml`
- `.github/workflows/`
- `scripts/`

---

### 4. QA_AGENT.md (P1 - Create Soon)

**Role**: Quality Assurance Engineer for Go testing.

**Key Skills**:
- Go testing (`go test`, table-driven tests)
- Mocking with interfaces
- testify/assert, testify/mock
- httptest for API testing
- golangci-lint for static analysis
- Test coverage with `go test -cover`

**Owns**:
- `*_test.go` files throughout
- `testdata/` fixtures
- CI test configuration
- Mock implementations

---

### 5. NODE_AGENT_SPECIALIST.md (P2 - Create Later)

**Role**: Systems Engineer for the GPU node agent.

**Key Skills**:
- Minimal Go binaries
- Heartbeat/health check patterns
- Self-destruct timers
- File transfer protocols
- SSH tunnel management
- Container optimization for GPU nodes

**Owns**:
- `cmd/agent/`
- `agent/` package
- `deploy/Dockerfile.agent`

---

## Agent Orchestration Options

### Option A: Sequential Handoff
```
Architect → Go Backend → Provider → QA → Infra
```
- One agent works at a time
- Clean handoffs with clear deliverables
- Slower but easier to manage

### Option B: Parallel Workstreams
```
Day 1-2:
  Architect (oversight)
  ├── Go Backend Agent (core services)
  └── Provider Agent (API integration)

Day 3-4:
  Architect (oversight)
  ├── Go Backend Agent (lifecycle, API, CLI)
  ├── QA Agent (tests alongside development)
  └── Provider Agent (finalize adapters)

Day 5:
  Architect (integration review)
  ├── Infra Agent (Docker, CI)
  ├── Node Agent Specialist (agent binary)
  └── QA Agent (integration tests)
```
- Faster overall
- Requires good interface definitions upfront
- Architect coordinates integration points

### Option C: Feature-Based Teams
```
Feature: Inventory
  Go Backend + Provider + QA

Feature: Provisioning
  Go Backend + Provider + QA

Feature: Lifecycle
  Go Backend + QA

Feature: API/CLI
  Go Backend + QA

Feature: Deployment
  Infra + Node Agent Specialist
```
- Complete features end-to-end
- Good for iterative development
- Requires clear feature boundaries

---

## Recommended Approach

**Use Option B (Parallel Workstreams)** with:

1. **Architect** as coordinator throughout
2. **Go Backend Agent** + **Provider Agent** working in parallel on Day 1-2
3. **QA Agent** joins on Day 2 to write tests alongside implementation
4. **Infra Agent** joins on Day 4-5 for deployment
5. **Node Agent Specialist** created if needed, or Go Backend Agent handles it

---

## Immediate Actions

1. **Create GO_BACKEND_AGENT.md** - Needed to start implementation
2. **Create PROVIDER_AGENT.md** - Needed for Day 1 provider spike
3. Defer other agents until Day 2+

---

## Agents NOT Needed

| Agent | Reason |
|-------|--------|
| LLM Agent | We don't host LLMs, consumers do |
| Control Plane Agent | Python-focused, not applicable |
| Data Pipeline Agent | Financial data, not relevant |
| Python ML Agent | ML training is consumer's concern |
| Dashboard Agent | Deprecated |
| Frontend Agent | No frontend in MVP |
