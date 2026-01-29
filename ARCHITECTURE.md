# Cloud GPU Shopper - Architecture

## Overview

Cloud GPU Shopper is a Go service that provides unified inventory and orchestration over commodity GPU providers. It acts as a catalog and provisioner - not a proxy - handing off direct access to consumers.

## System Components

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         CLOUD GPU SHOPPER SERVICE                        │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────────────┐  │
│  │   REST API      │  │      CLI        │  │     Background Jobs     │  │
│  │   (gin)         │  │   (cobra)       │  │  - Inventory refresh    │  │
│  │                 │  │                 │  │  - Lifecycle checks     │  │
│  │  /inventory     │  │  gpu-shopper    │  │  - Orphan detection     │  │
│  │  /sessions      │  │                 │  │  - Cost aggregation     │  │
│  │  /costs         │  │                 │  │                         │  │
│  └────────┬────────┘  └────────┬────────┘  └────────────┬────────────┘  │
│           │                    │                        │               │
│           └────────────────────┼────────────────────────┘               │
│                                │                                         │
│                                ▼                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                         CORE SERVICES                             │   │
│  ├──────────────────────────────────────────────────────────────────┤   │
│  │                                                                   │   │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ │   │
│  │  │  Inventory  │ │ Provisioner │ │  Lifecycle  │ │    Cost     │ │   │
│  │  │  Service    │ │   Service   │ │   Manager   │ │   Tracker   │ │   │
│  │  │             │ │             │ │             │ │             │ │   │
│  │  │ - Fetch     │ │ - Create    │ │ - Timers    │ │ - Sessions  │ │   │
│  │  │ - Cache     │ │ - Deploy    │ │ - Heartbeat │ │ - Consumers │ │   │
│  │  │ - Filter    │ │ - Teardown  │ │ - Orphans   │ │ - Alerts    │ │   │
│  │  │ - Adaptive  │ │ - Creds     │ │ - Idle      │ │ - Webhooks  │ │   │
│  │  └──────┬──────┘ └──────┬──────┘ └──────┬──────┘ └─────────────┘ │   │
│  │         │               │               │                        │   │
│  └─────────┼───────────────┼───────────────┼────────────────────────┘   │
│            │               │               │                            │
│            ▼               ▼               ▼                            │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                      PROVIDER ADAPTERS                            │   │
│  ├──────────────────────────────────────────────────────────────────┤   │
│  │  ┌─────────────────────────┐  ┌─────────────────────────────────┐│   │
│  │  │       Vast.ai           │  │         TensorDock              ││   │
│  │  │  - ListOffers()         │  │  - ListOffers()                 ││   │
│  │  │  - CreateInstance()     │  │  - CreateInstance()             ││   │
│  │  │  - DestroyInstance()    │  │  - DestroyInstance()            ││   │
│  │  │  - GetInstanceStatus()  │  │  - GetInstanceStatus()          ││   │
│  │  └─────────────────────────┘  └─────────────────────────────────┘│   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                         STORAGE (SQLite)                          │   │
│  │  - sessions: active GPU sessions                                  │   │
│  │  - costs: hourly cost records                                     │   │
│  │  - consumers: registered consumers + budget configs               │   │
│  │  - inventory_cache: cached provider responses                     │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘

                                    │
                     SSH + Container Deployment
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         GPU NODE (Remote)                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                     NODE AGENT (Docker)                           │   │
│  │                                                                   │   │
│  │  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ │   │
│  │  │  LLM Proxy  │ │ File Server │ │ ML Command  │ │  Heartbeat  │ │   │
│  │  │             │ │             │ │   Handler   │ │  + Status   │ │   │
│  │  │ POST /chat  │ │ POST/GET    │ │ POST /cmd   │ │ GET /health │ │   │
│  │  │             │ │ /files/*    │ │             │ │             │ │   │
│  │  └─────────────┘ └─────────────┘ └─────────────┘ └─────────────┘ │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐ │   │
│  │  │                    Idle Detector                             │ │   │
│  │  │  - Track last activity                                       │ │   │
│  │  │  - Report idle status in heartbeat                           │ │   │
│  │  └─────────────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                 GPU WORKLOAD (Consumer's)                         │   │
│  │  - vLLM / Ollama / TGI                                           │   │
│  │  - Training scripts                                               │   │
│  │  - Batch jobs                                                     │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

## Directory Structure

```
cloud-gpu-shopper/
├── cmd/
│   ├── server/           # Main API server
│   │   └── main.go
│   ├── cli/              # CLI tool
│   │   └── main.go
│   └── agent/            # Node agent (deployed to GPU nodes)
│       └── main.go
│
├── internal/
│   ├── api/              # REST API handlers
│   │   ├── handlers.go
│   │   ├── middleware.go
│   │   └── routes.go
│   │
│   ├── inventory/        # Inventory service
│   │   ├── service.go
│   │   ├── cache.go
│   │   └── types.go
│   │
│   ├── provisioner/      # Provisioning service
│   │   ├── service.go
│   │   └── types.go
│   │
│   ├── lifecycle/        # Lifecycle management
│   │   ├── manager.go
│   │   ├── timer.go
│   │   ├── orphan.go
│   │   └── idle.go
│   │
│   ├── cost/             # Cost tracking
│   │   ├── tracker.go
│   │   ├── aggregator.go
│   │   └── alerts.go
│   │
│   ├── provider/         # Provider adapters
│   │   ├── interface.go  # Common interface
│   │   ├── vastai/
│   │   │   ├── client.go
│   │   │   └── types.go
│   │   └── tensordock/
│   │       ├── client.go
│   │       └── types.go
│   │
│   └── storage/          # SQLite storage
│       ├── db.go
│       ├── sessions.go
│       ├── costs.go
│       └── migrations.go
│
├── pkg/                  # Public packages (shared with agent)
│   ├── models/           # Shared data models
│   │   ├── gpu.go
│   │   ├── session.go
│   │   └── cost.go
│   └── client/           # Go client for the API
│       └── client.go
│
├── agent/                # Node agent code
│   ├── api/
│   │   ├── handlers.go
│   │   └── routes.go
│   ├── llm/
│   │   └── proxy.go
│   ├── files/
│   │   └── server.go
│   ├── ml/
│   │   └── command.go
│   └── heartbeat/
│       └── heartbeat.go
│
├── deploy/
│   ├── Dockerfile.server
│   ├── Dockerfile.agent
│   └── docker-compose.yml
│
├── scripts/
│   └── test.sh
│
├── prompts/              # Agent prompts
│   ├── PRODUCT_DESIGNER_AGENT.md
│   ├── ARCHITECT_AGENT.md
│   └── README.md
│
├── go.mod
├── go.sum
├── ARCHITECTURE.md
├── PRD_MVP.md
├── PROGRESS.md
├── CLAUDE.md
└── README.md
```

## Data Models

### GPU Offer (Inventory Item)
```go
type GPUOffer struct {
    ID           string    `json:"id"`
    Provider     string    `json:"provider"`      // "vastai" | "tensordock"
    ProviderID   string    `json:"provider_id"`   // Provider's ID for this offer
    GPUType      string    `json:"gpu_type"`      // "RTX 4090", "A100", etc.
    GPUCount     int       `json:"gpu_count"`
    VRAM         int       `json:"vram_gb"`
    PricePerHour float64   `json:"price_per_hour"`
    Location     string    `json:"location"`
    Reliability  float64   `json:"reliability"`   // 0-1 score if available
    Available    bool      `json:"available"`
    MaxDuration  int       `json:"max_duration_hours"` // 0 = unlimited
    FetchedAt    time.Time `json:"fetched_at"`
}
```

### Session
```go
type Session struct {
    ID              string        `json:"id"`
    ConsumerID      string        `json:"consumer_id"`
    Provider        string        `json:"provider"`
    ProviderID      string        `json:"provider_instance_id"`
    GPUType         string        `json:"gpu_type"`
    Status          SessionStatus `json:"status"`  // pending, provisioning, running, stopping, stopped, failed
    SSHHost         string        `json:"ssh_host"`
    SSHPort         int           `json:"ssh_port"`
    SSHUser         string        `json:"ssh_user"`
    SSHKey          string        `json:"ssh_key,omitempty"`  // Private key (only returned once)
    AgentEndpoint   string        `json:"agent_endpoint"`
    AgentToken      string        `json:"agent_token,omitempty"`
    WorkloadType    string        `json:"workload_type"`  // "llm", "training", "batch"
    ReservationHrs  int           `json:"reservation_hours"`
    HardMaxOverride bool          `json:"hard_max_override"`
    PricePerHour    float64       `json:"price_per_hour"`
    CreatedAt       time.Time     `json:"created_at"`
    ExpiresAt       time.Time     `json:"expires_at"`
    LastHeartbeat   time.Time     `json:"last_heartbeat"`
    IdleThreshold   int           `json:"idle_threshold_minutes"`  // 0 = disabled
    StoragePolicy   string        `json:"storage_policy"`  // "preserve" | "destroy"
}

type SessionStatus string
const (
    StatusPending      SessionStatus = "pending"
    StatusProvisioning SessionStatus = "provisioning"
    StatusRunning      SessionStatus = "running"
    StatusStopping     SessionStatus = "stopping"
    StatusStopped      SessionStatus = "stopped"
    StatusFailed       SessionStatus = "failed"
)
```

### Cost Record
```go
type CostRecord struct {
    ID         string    `json:"id"`
    SessionID  string    `json:"session_id"`
    ConsumerID string    `json:"consumer_id"`
    Provider   string    `json:"provider"`
    GPUType    string    `json:"gpu_type"`
    Hour       time.Time `json:"hour"`         // Truncated to hour
    Amount     float64   `json:"amount"`       // Cost for this hour
    Currency   string    `json:"currency"`     // "USD"
}

type CostSummary struct {
    ConsumerID   string  `json:"consumer_id,omitempty"`
    TotalCost    float64 `json:"total_cost"`
    SessionCount int     `json:"session_count"`
    HoursUsed    float64 `json:"hours_used"`
    ByProvider   map[string]float64 `json:"by_provider"`
    ByGPUType    map[string]float64 `json:"by_gpu_type"`
}
```

## Provider Interface

All providers implement this interface:

```go
type Provider interface {
    // Name returns the provider identifier ("vastai", "tensordock")
    Name() string

    // ListOffers returns available GPU offers
    // Respects rate limiting and returns cached data if appropriate
    ListOffers(ctx context.Context, filter OfferFilter) ([]GPUOffer, error)

    // CreateInstance provisions a new GPU instance
    CreateInstance(ctx context.Context, req CreateInstanceRequest) (*InstanceInfo, error)

    // DestroyInstance tears down a GPU instance
    DestroyInstance(ctx context.Context, instanceID string) error

    // GetInstanceStatus returns current status of an instance
    GetInstanceStatus(ctx context.Context, instanceID string) (*InstanceStatus, error)

    // SupportsIdleDetection indicates if provider has native idle detection
    SupportsIdleDetection() bool
}

type OfferFilter struct {
    GPUType     string   // Filter by GPU type
    MinVRAM     int      // Minimum VRAM in GB
    MaxPrice    float64  // Maximum price per hour
    Location    string   // Region/location filter
    MinReliability float64 // Minimum reliability score
}

type CreateInstanceRequest struct {
    OfferID      string
    SSHPublicKey string
    DockerImage  string            // Our agent image
    EnvVars      map[string]string // Agent config
    OnStartCmd   string            // Startup command
}

type InstanceInfo struct {
    ProviderInstanceID string
    SSHHost           string
    SSHPort           int
    SSHUser           string
    Status            string
}

type InstanceStatus struct {
    Status    string    // "running", "stopped", "error"
    Running   bool
    StartedAt time.Time
    Error     string
}
```

## Adaptive Rate Limiting

The inventory service implements adaptive caching:

```go
type AdaptiveCache struct {
    defaultTTL  time.Duration  // 1 minute
    backoffTTL  time.Duration  // 5 minutes
    isBackedOff map[string]bool
    mu          sync.RWMutex
}

// On rate limit error from provider:
// 1. Mark provider as backed off
// 2. Use backoffTTL instead of defaultTTL
// 3. Retry with exponential backoff
// 4. Reset after successful call
```

## Lifecycle Management

### Timer-Based Checks
```go
type LifecycleManager struct {
    checkInterval time.Duration  // 1 minute
}

func (lm *LifecycleManager) Run(ctx context.Context) {
    ticker := time.NewTicker(lm.checkInterval)
    for {
        select {
        case <-ticker.C:
            lm.checkReservations()  // Check expired reservations
            lm.checkOrphans()       // Detect orphaned instances
            lm.checkIdleSessions()  // Check idle threshold
            lm.checkHardMax()       // Enforce 12-hour limit
        case <-ctx.Done():
            return
        }
    }
}
```

### Orphan Detection
```go
func (lm *LifecycleManager) checkOrphans() {
    // Get all sessions past their reservation time
    // That haven't received "done" signal
    // And aren't explicitly extended
    // → Mark as orphan, send alert
}
```

### 12-Hour Hard Max
```go
func (lm *LifecycleManager) checkHardMax() {
    // Get sessions running > 12 hours
    // Without HardMaxOverride = true
    // → Force shutdown with alert
}
```

## Testing Strategy

### Unit Tests
- All services have interface-based dependencies for mocking
- Provider adapters tested against recorded API responses
- Lifecycle logic tested with time mocking

### Integration Tests
- Docker-based test environment
- Mock provider endpoints
- Full API flow tests
- CLI command tests

### Provider Integration Tests
- Optional, run with real API keys
- Validate provider response parsing
- Test actual provisioning (with immediate teardown)

## Security Considerations

1. **API Keys**: Stored in environment variables, never logged
2. **SSH Keys**: Generated per-session, returned once, stored encrypted
3. **Agent Tokens**: JWT with session-scoped claims, short expiry
4. **Budget Alerts**: Webhook URLs validated, no arbitrary code execution
5. **Provider Credentials**: Never exposed via API, only used server-side

## Configuration

```yaml
# config.yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  path: "./data/gpu-shopper.db"

providers:
  vastai:
    api_key: "${VASTAI_API_KEY}"
    enabled: true
  tensordock:
    auth_id: "${TENSORDOCK_AUTH_ID}"
    api_token: "${TENSORDOCK_API_TOKEN}"
    enabled: true

inventory:
  default_cache_ttl: 60s
  backoff_cache_ttl: 300s

lifecycle:
  check_interval: 60s
  hard_max_hours: 12
  default_idle_threshold: 0  # Disabled by default
  heartbeat_timeout: 5m
  orphan_grace_period: 15m
  reconciliation_interval: 5m

agent:
  docker_image: "ghcr.io/your-org/gpu-shopper-agent:latest"
  default_port: 8081
  self_destruct_grace: 30m
```

---

## Critical Safety Systems (Required Additions)

The following systems are **mandatory** to achieve the "zero orphaned instances" goal.

### 1. Instance Tagging Strategy

Every instance provisioned MUST be tagged with metadata that enables provider-side discovery:

```go
type InstanceTags struct {
    ShopperSessionID    string    `json:"shopper_session_id"`
    ShopperDeploymentID string    `json:"shopper_deployment_id"`  // Unique per shopper instance
    ShopperExpiresAt    time.Time `json:"shopper_expires_at"`
    ShopperConsumerID   string    `json:"shopper_consumer_id"`
}

// Provider interface extension
type Provider interface {
    // ... existing methods ...

    // ListAllInstances returns ALL instances with our tags (for reconciliation)
    ListAllInstances(ctx context.Context) ([]ProviderInstance, error)

    // TagInstance adds/updates tags on an instance
    TagInstance(ctx context.Context, instanceID string, tags InstanceTags) error
}
```

### 2. Two-Phase Provisioning

Provisioning follows a crash-safe two-phase pattern:

```go
func (p *Provisioner) CreateSession(ctx context.Context, req CreateSessionRequest) (*Session, error) {
    // PHASE 1: Create intent record (survives crashes)
    session := &Session{
        ID:        uuid.New().String(),
        Status:    StatusPending,
        CreatedAt: time.Now(),
    }
    if err := p.store.CreateSession(session); err != nil {
        return nil, err
    }

    // PHASE 2: Call provider with session ID as tag
    tags := InstanceTags{
        ShopperSessionID:    session.ID,
        ShopperDeploymentID: p.deploymentID,
        ShopperExpiresAt:    time.Now().Add(time.Duration(req.ReservationHrs) * time.Hour),
        ShopperConsumerID:   req.ConsumerID,
    }

    instance, err := p.provider.CreateInstance(ctx, CreateInstanceRequest{
        OfferID:      req.OfferID,
        SSHPublicKey: session.SSHPublicKey,
        DockerImage:  p.agentImage,
        EnvVars:      p.buildAgentEnv(session, tags),
        Tags:         tags,
    })
    if err != nil {
        session.Status = StatusFailed
        session.Error = err.Error()
        p.store.UpdateSession(session)
        return nil, err
    }

    // PHASE 3: Update with provider info
    session.ProviderID = instance.ProviderInstanceID
    session.Status = StatusProvisioning
    session.SSHHost = instance.SSHHost
    session.SSHPort = instance.SSHPort
    if err := p.store.UpdateSession(session); err != nil {
        // Critical: Instance exists but we failed to record it
        // Reconciliation will catch this
        log.Error("Failed to update session after provision", "session_id", session.ID, "provider_id", instance.ProviderInstanceID)
        return nil, err
    }

    // PHASE 4: Wait for agent heartbeat
    if err := p.waitForHeartbeat(ctx, session, 5*time.Minute); err != nil {
        // Agent didn't come up - destroy and fail
        p.provider.DestroyInstance(ctx, instance.ProviderInstanceID)
        session.Status = StatusFailed
        session.Error = "Agent failed to start: " + err.Error()
        p.store.UpdateSession(session)
        return nil, err
    }

    session.Status = StatusRunning
    p.store.UpdateSession(session)
    return session, nil
}
```

### 3. Destroy Verification Loop

Destruction is not complete until verified:

```go
func (p *Provisioner) DestroySession(ctx context.Context, sessionID string) error {
    session, err := p.store.GetSession(sessionID)
    if err != nil {
        return err
    }

    session.Status = StatusStopping
    p.store.UpdateSession(session)

    // Call destroy
    if err := p.provider.DestroyInstance(ctx, session.ProviderID); err != nil {
        log.Error("Destroy call failed", "session_id", sessionID, "error", err)
        // Continue to verification - instance might still be gone
    }

    // Verify destruction with retries
    for attempt := 0; attempt < 10; attempt++ {
        status, err := p.provider.GetInstanceStatus(ctx, session.ProviderID)
        if err != nil {
            // Instance not found = successfully destroyed
            if isNotFoundError(err) {
                session.Status = StatusStopped
                p.store.UpdateSession(session)
                return nil
            }
            log.Warn("Status check failed", "attempt", attempt, "error", err)
        } else if !status.Running {
            session.Status = StatusStopped
            p.store.UpdateSession(session)
            return nil
        }

        // Still running - retry destroy
        p.provider.DestroyInstance(ctx, session.ProviderID)
        time.Sleep(time.Duration(attempt+1) * 10 * time.Second)
    }

    // Failed to verify destruction - alert operator
    p.alerter.SendAlert(Alert{
        Severity: "critical",
        Message:  fmt.Sprintf("Failed to verify destruction of session %s (provider: %s)", sessionID, session.ProviderID),
    })

    return fmt.Errorf("failed to verify instance destruction after 10 attempts")
}
```

### 4. Provider Reconciliation Job

Runs every 5 minutes to detect orphans:

```go
func (lm *LifecycleManager) runReconciliation(ctx context.Context) {
    for _, provider := range lm.providers {
        // Get all instances from provider with our tags
        providerInstances, err := provider.ListAllInstances(ctx)
        if err != nil {
            log.Error("Failed to list provider instances", "provider", provider.Name(), "error", err)
            continue
        }

        // Get all active sessions from DB for this provider
        localSessions, err := lm.store.GetActiveSessionsByProvider(provider.Name())
        if err != nil {
            log.Error("Failed to get local sessions", "provider", provider.Name(), "error", err)
            continue
        }

        localMap := make(map[string]*Session)
        for _, s := range localSessions {
            localMap[s.ProviderID] = s
        }

        providerMap := make(map[string]ProviderInstance)
        for _, p := range providerInstances {
            providerMap[p.ID] = p
        }

        // Find orphans: exist on provider but not in DB
        for providerID, instance := range providerMap {
            if _, exists := localMap[providerID]; !exists {
                log.Error("ORPHAN DETECTED: Instance exists on provider but not in local DB",
                    "provider", provider.Name(),
                    "provider_id", providerID,
                    "session_id", instance.Tags.ShopperSessionID,
                    "running_since", instance.StartedAt,
                )

                lm.metrics.OrphansDetected.Inc()

                // Auto-destroy orphan
                if err := provider.DestroyInstance(ctx, providerID); err != nil {
                    log.Error("Failed to destroy orphan", "provider_id", providerID, "error", err)
                    lm.alerter.SendAlert(Alert{
                        Severity: "critical",
                        Message:  fmt.Sprintf("Orphan detected and destroy failed: %s", providerID),
                    })
                } else {
                    log.Info("Orphan destroyed", "provider_id", providerID)
                }
            }
        }

        // Find ghosts: exist in DB but not on provider
        for providerID, session := range localMap {
            if _, exists := providerMap[providerID]; !exists {
                if session.Status == StatusRunning || session.Status == StatusProvisioning {
                    log.Warn("GHOST DETECTED: Session in DB but instance not on provider",
                        "session_id", session.ID,
                        "provider_id", providerID,
                    )
                    session.Status = StatusStopped
                    session.Error = "Instance not found on provider during reconciliation"
                    lm.store.UpdateSession(session)
                }
            }
        }
    }
}
```

### 5. Startup Recovery

On service startup, reconcile state before accepting requests:

```go
func (s *Server) Start(ctx context.Context) error {
    log.Info("Starting Cloud GPU Shopper...")

    // STEP 1: Run immediate reconciliation
    log.Info("Running startup reconciliation...")
    if err := s.lifecycle.runReconciliation(ctx); err != nil {
        log.Error("Startup reconciliation failed", "error", err)
        // Continue anyway - better to start than not
    }

    // STEP 2: Resume stuck sessions
    stuckSessions, _ := s.store.GetSessionsByStatus(StatusProvisioning, StatusStopping)
    for _, session := range stuckSessions {
        log.Warn("Found stuck session", "session_id", session.ID, "status", session.Status)

        status, err := s.getProviderForSession(session).GetInstanceStatus(ctx, session.ProviderID)
        if err != nil {
            // Instance gone - mark as stopped/failed
            session.Status = StatusFailed
            session.Error = "Instance not found after restart"
            s.store.UpdateSession(session)
        } else if status.Running {
            if session.Status == StatusProvisioning {
                // Check for heartbeat, maybe it's actually running
                session.Status = StatusRunning
            } else {
                // Was stopping - retry destroy
                go s.provisioner.DestroySession(ctx, session.ID)
            }
            s.store.UpdateSession(session)
        }
    }

    // STEP 3: Start background jobs
    go s.lifecycle.Run(ctx)
    go s.inventory.StartRefresh(ctx)
    go s.cost.StartAggregation(ctx)

    // STEP 4: Start API server
    log.Info("Starting API server", "port", s.config.Server.Port)
    return s.router.Run(fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port))
}
```

### 6. Agent Self-Destruct Timer

The node agent has an independent safety mechanism:

```go
// In agent/main.go
func main() {
    expiresAt := parseExpiresAt(os.Getenv("SHOPPER_EXPIRES_AT"))
    gracePeriod := parseDuration(os.Getenv("SHOPPER_SELF_DESTRUCT_GRACE"), 30*time.Minute)

    selfDestructAt := expiresAt.Add(gracePeriod)

    log.Info("Agent starting",
        "expires_at", expiresAt,
        "self_destruct_at", selfDestructAt,
    )

    // Start self-destruct timer (independent of shopper connectivity)
    go func() {
        timer := time.NewTimer(time.Until(selfDestructAt))
        <-timer.C
        log.Error("SELF-DESTRUCT: Expiration time reached, shutting down")
        exec.Command("shutdown", "-h", "now").Run()
    }()

    // Start heartbeat with shopper-unreachable failsafe
    go func() {
        unreachableCount := 0
        for {
            if err := sendHeartbeat(); err != nil {
                unreachableCount++
                log.Warn("Heartbeat failed", "consecutive_failures", unreachableCount)

                if unreachableCount >= 60 { // 30 minutes at 30s interval
                    log.Error("SELF-DESTRUCT: Shopper unreachable for 30 minutes")
                    exec.Command("shutdown", "-h", "now").Run()
                }
            } else {
                unreachableCount = 0
            }
            time.Sleep(30 * time.Second)
        }
    }()

    // ... rest of agent startup
}
```

### 7. Enhanced Directory Structure

Updated to include reconciliation and safety systems:

```
internal/
├── lifecycle/
│   ├── manager.go           # Main lifecycle manager
│   ├── timer.go             # Timer-based checks
│   ├── orphan.go            # Orphan detection (DB-based)
│   ├── reconciler.go        # NEW: Provider reconciliation
│   ├── recovery.go          # NEW: Startup recovery
│   └── idle.go              # Idle detection
├── provisioner/
│   ├── service.go           # Two-phase provisioning
│   ├── destroyer.go         # NEW: Verified destruction
│   └── types.go
```

### 8. Observability Requirements

Mandatory metrics for safety monitoring:

```go
var (
    // Critical safety metrics
    SessionsActive = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{Name: "gpu_sessions_active"},
        []string{"provider", "status"},
    )
    OrphansDetected = prometheus.NewCounter(
        prometheus.CounterOpts{Name: "gpu_orphans_detected_total"},
    )
    DestroyFailures = prometheus.NewCounter(
        prometheus.CounterOpts{Name: "gpu_destroy_failures_total"},
    )
    ReconciliationMismatches = prometheus.NewCounter(
        prometheus.CounterOpts{Name: "gpu_reconciliation_mismatches_total"},
    )
    HeartbeatAge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{Name: "gpu_heartbeat_age_seconds"},
        []string{"session_id"},
    )
    ProviderAPIErrors = prometheus.NewCounterVec(
        prometheus.CounterOpts{Name: "gpu_provider_api_errors_total"},
        []string{"provider", "operation"},
    )
)
```

### 9. Critical Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| OrphanDetected | `gpu_orphans_detected_total` increases | Critical |
| DestroyFailed | `gpu_destroy_failures_total` increases | Critical |
| ReconciliationMismatch | `gpu_reconciliation_mismatches_total` increases | Critical |
| HeartbeatStale | `gpu_heartbeat_age_seconds > 1800` | Warning |
| ProviderAPIDown | `gpu_provider_api_errors_total` rate > 10/5m | Warning |
