# Agent Prompt Templates - Cloud GPU Shopper

## Team Overview

```
YOU (Human)
    ↓
ARCHITECT AGENT (orchestrates)
    ↓
┌───────────────┬───────────────┬───────────────┬───────────────┐
│  Go Backend   │   Provider    │     QA        │    Infra      │
│    Agent      │    Agent      │    Agent      │    Agent      │
│   (P0)        │    (P0)       │    (P1)       │    (P1)       │
└───────────────┴───────────────┴───────────────┴───────────────┘
```

## Current Team

| Agent | File | Status | Priority |
|-------|------|--------|----------|
| Product Designer | `PRODUCT_DESIGNER_AGENT.md` | ✅ Ready | Planning |
| Architect | `ARCHITECT_AGENT.md` | ✅ Ready | Oversight |
| Go Backend | `GO_BACKEND_AGENT.md` | ✅ Ready | P0 |
| Provider | `PROVIDER_AGENT.md` | ✅ Ready | P0 |
| QA | `QA_AGENT.md` | ⏳ Create Day 2 | P1 |
| Infra | `INFRA_AGENT.md` | ⏳ Create Day 4 | P1 |

## Agent Responsibilities

| Agent | Owns | Delivers |
|-------|------|----------|
| **Product Designer** | PRD, user flows, scope decisions | PRD_MVP.md |
| **Architect** | System design, integration, coordination | ARCHITECTURE.md, code reviews |
| **Go Backend** | Core service implementation | cmd/, internal/, pkg/ |
| **Provider** | Vast.ai + TensorDock adapters | internal/provider/ |
| **QA** | Tests, CI pipeline | *_test.go, .github/workflows/ |
| **Infra** | Docker, deployment | deploy/, Dockerfiles |

## How to Use

### Starting a Session

1. Open Claude Code in the project directory
2. Paste the relevant agent prompt
3. Check PROGRESS.md for current tasks
4. Work on deliverables
5. Update PROGRESS.md when complete

### Example: Starting as Go Backend Agent

```
You: [Paste contents of GO_BACKEND_AGENT.md]

Claude: I'm now the Go Backend Agent. Let me check PROGRESS.md...

[Claude reads PROGRESS.md and begins work on next unchecked item]
```

### Switching Agents

```
You: Switch to Provider Agent.
You: [Paste contents of PROVIDER_AGENT.md]

Claude: I'm now the Provider Agent. Let me check what needs to be done...
```

## Orchestration Strategy

**Option B: Parallel Workstreams** (Recommended)

```
Day 1-2:
  Architect (oversight)
  ├── Go Backend Agent (core services)
  └── Provider Agent (API integration)

Day 3-4:
  Architect (oversight)
  ├── Go Backend Agent (lifecycle, API, CLI)
  ├── QA Agent (tests)
  └── Provider Agent (finalize adapters)

Day 5:
  Architect (integration review)
  ├── Infra Agent (Docker, CI)
  └── QA Agent (integration tests)
```

## Dropped from Project Atlas

These agents are NOT needed for Cloud GPU Shopper:

| Agent | Reason |
|-------|--------|
| LLM Agent | LLM deployment is consumer's concern |
| Control Plane Agent | Python-focused |
| Data Pipeline Agent | Not relevant |
| Python ML Agent | ML is consumer's concern |
| Dashboard Agent | Deprecated |
| Frontend Agent | No frontend in MVP |

## Key Documents

| Document | Purpose |
|----------|---------|
| `PRD_MVP.md` | What we're building |
| `ARCHITECTURE.md` | How it's structured |
| `PROGRESS.md` | Current status |
| `CLAUDE.md` | Quick reference |
| `TEAM_ASSESSMENT.md` | Agent team evaluation |

## Commit Convention

```
[Agent] Brief description

- Detail 1
- Detail 2

Phase: X | Progress: Y/Z items complete
```

Examples:
```
[Go] Implement inventory service with adaptive caching
[Provider] Add Vast.ai adapter with mocked tests
[Infra] Add Docker Compose for local development
[QA] Add integration tests for provisioning flow
```
