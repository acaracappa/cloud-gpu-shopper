# GPU Shopper Bug Report - 2026-01-31 (Monitoring Session)

## Executive Summary

During live monitoring of the GPU Shopper service with both Vast.ai and TensorDock providers, a team of QA/Ops agents identified **45 bugs** across multiple categories.

**Breakdown by Severity:**
- **P0 (Critical):** 0
- **P1 (High):** 7
- **P2 (Medium):** 19
- **P3 (Low):** 19

---

## Session Details

- **Date:** 2026-01-31
- **Duration:** ~5 minutes of active monitoring
- **Test Session:** `3581e4f1-682a-418c-9ebb-bec7db6d0b81` (Vast.ai RTX 5060 Ti)
- **Result:** Successfully provisioned, SSH verified after 3 attempts (56s), then destroyed

---

## API Issues (Found by API Stress Testing)

### P1 - Wrong HTTP Status for Non-Existent Sessions

**Endpoints Affected:**
- `POST /api/v1/sessions/{id}/done` - Returns 500 instead of 404
- `POST /api/v1/sessions/{invalid-id}/done` - Returns 500 instead of 404

**Example:**
```bash
curl -X POST http://localhost:8080/api/v1/sessions/00000000-0000-0000-0000-000000000000/done
# Response: {"error":"record not found"} with HTTP 500
```

**File:** `internal/api/handlers.go:381-398`

---

### P1 - Invalid Provider Returns 500 Instead of 400

**Endpoint:** `GET /api/v1/inventory?provider=invalid_provider`

**Response:**
```json
{"error":"provider not found: invalid_provider"}
HTTP_STATUS: 500
```

**Expected:** HTTP 400 Bad Request

**File:** `internal/api/handlers.go`

---

### P2 - Internal Field Names Leaked in Validation Errors

**Endpoint:** `POST /api/v1/sessions`

**Response:**
```json
{"error":"Key: 'CreateSessionRequest.ConsumerID' Error:Field validation for 'ConsumerID' failed on the 'required' tag"}
```

**Problem:** Leaks Go struct field names (`ConsumerID` vs `consumer_id`)

**File:** `internal/api/handlers.go:237-276`

---

### P2 - Costs Endpoint Returns Invalid Zero Dates

**Endpoint:** `GET /api/v1/costs`

**Response:**
```json
{"period_start":"0001-01-01T00:00:00Z","period_end":"0001-01-01T00:00:00Z"}
```

**File:** `internal/api/handlers.go` (cost handler)

---

### P2 - Pagination Parameters Silently Ignored

**Endpoint:** `GET /api/v1/inventory?limit=5&offset=10&sort=price`

**Problem:** All parameters ignored; always returns full 88 offers

**File:** `internal/api/handlers.go:117-147`

---

### P2 - Invalid Query Parameter Values Silently Ignored

**Examples:**
- `?min_vram=invalid` - Returns full inventory
- `?max_price=not-a-number` - Returns full inventory
- `?gpu_count=abc` - Returns full inventory

**Expected:** HTTP 400 for invalid numeric values

---

### P2 - Negative Values Accepted Without Validation

**Example:** `GET /api/v1/inventory?min_vram=-10`

**Problem:** Returns all offers (all have vram >= -10)

---

### P2 - max_price=0 Returns All Results

**Endpoint:** `GET /api/v1/inventory?max_price=0`

**Expected:** Empty results or validation error

---

### P2 - Invalid Session Status Filter Silently Ignored

**Endpoint:** `GET /api/v1/sessions?status=invalid_status`

**Problem:** Returns empty results instead of 400

---

### P3 - Inconsistent 404 Response Formats

**Router 404:** `404 page not found` (plain text)
**API 404:** `{"error":"record not found"}` (JSON)

---

### P3 - Can Call `/done` on Already Stopped Session

**Problem:** Returns 200 OK; should return 409 Conflict

---

### P3 - HEAD Request Timeout

**Endpoint:** `HEAD /api/v1/inventory`

**Problem:** Takes 60 seconds and returns 404

---

### P3 - Missing CORS Headers

**Endpoint:** `OPTIONS /api/v1/inventory`

**Problem:** Returns 404 instead of CORS headers

---

### P3 - Location Filter Does Not Match Substring

**Endpoint:** `GET /api/v1/inventory?location=US`

**Problem:** Returns empty even with offers like "Texas, US"

---

### P3 - Metrics Endpoint Publicly Accessible

**Endpoint:** `GET /metrics`

**Problem:** Prometheus metrics exposed without authentication

---

## Code-Level Issues (Found by Code Review)

### P1 - Race Condition in Reconciler.Stop()

**File:** `internal/service/lifecycle/reconciler.go:170-188`

```go
func (r *Reconciler) Stop() {
    r.mu.Lock()
    if !r.running {
        r.mu.Unlock()
        return
    }
    r.mu.Unlock()  // Lock released here

    close(r.stopCh)  // May be different channel if Start() called
    <-r.doneCh       // May be different channel

    r.mu.Lock()
    r.running = false  // Set AFTER wait
    r.mu.Unlock()
}
```

**Problem:** Channel references can change between lock release and close

---

### P1 - Context Leak in destroyWithVerification

**File:** `internal/service/provisioner/service.go:726-786`

```go
for attempt := 0; attempt < s.destroyRetries; attempt++ {
    // ...
    time.Sleep(time.Duration(attempt+1) * 5 * time.Second)  // Ignores ctx
}
```

**Problem:** With default 10 retries, can block for 275 seconds ignoring context cancellation

---

### P1 - SQLite Single Connection Bottleneck

**File:** `internal/storage/db.go:40-41`

```go
db.SetMaxOpenConns(1)
db.SetMaxIdleConns(1)
```

**Problem:** Lifecycle manager, reconciler, provisioner, and cost tracker all compete for one connection

---

### P2 - Reconciler run() Does Not Set running=false on Exit

**File:** `internal/service/lifecycle/reconciler.go:191-210`

**Problem:** If context cancelled, `running` stays `true` but `doneCh` is closed. Next `Stop()` may behave incorrectly.

---

### P2 - lastSSHHealthCheck Not Protected by Mutex

**File:** `internal/service/lifecycle/manager.go:73,276-280`

```go
if now.Sub(m.lastSSHHealthCheck) >= m.sshHealthCheckInterval {
    m.checkSSHHealth(ctx)
    m.lastSSHHealthCheck = now  // Not protected
}
```

---

### P2 - handleExtendSession Error Handling Inconsistent

**File:** `internal/api/handlers.go:400-438`

**Problem:** Returns 500 for all errors; should return 400/409 for terminal session, 404 for not found

---

### P2 - Background Refresh Goroutine Leak Potential

**File:** `internal/service/inventory/service.go:243-295`

```go
go func() {
    ctx, cancel := context.WithTimeout(context.Background(), s.providerTimeout)
    defer cancel()
    // ... refresh logic (no way to stop during graceful shutdown)
}()
```

---

### P2 - Double Cache Read Can See Inconsistent State

**File:** `internal/service/inventory/service.go:202-240`

**Problem:** Reads cache under RLock, then triggerBackgroundRefresh acquires Lock. Cache can change between.

---

### P2 - Progressive Backoff Not Thread-Safe

**File:** `internal/service/provisioner/service.go:67-106`

**Problem:** `ProgressiveBackoff.current` modified without synchronization

---

### P3 - Session State Race During Destroy

**File:** `internal/service/lifecycle/manager.go:453-480`

**Problem:** Session can be modified by another goroutine between fetch and update

---

### P3 - Insecure SSH Host Key Verification

**File:** `internal/ssh/verifier.go:184`

**Problem:** Uses `ssh.InsecureIgnoreHostKey()` - MITM possible (documented/intentional but noteworthy)

---

### P3 - Missing WorkloadType Validation

**File:** `internal/api/handlers.go:257-261`

```go
WorkloadType: models.WorkloadType(req.WorkloadType),  // No validation
```

---

### P3 - handleGetSessionDiagnostics Returns 404 for All Errors

**File:** `internal/api/handlers.go:569-577`

**Problem:** Should differentiate between not-found and internal errors

---

## Observed Warnings During Monitoring

```
WARN: DEPLOYMENT_ID not set; orphan detection may incorrectly claim instances from other deployments
WARN: using insecure host key verification for commodity GPU instance
WARN: deploymentID is empty; all provider instances will be considered ours
```

---

## Priority Fix Order

### Immediate (P1)
1. Fix HTTP status codes for not-found errors
2. Fix race condition in Reconciler.Stop()
3. Add context cancellation check in destroyWithVerification retry loop
4. Consider increasing SQLite connection pool

### High Priority (P2)
5. Sanitize validation error messages
6. Fix costs endpoint date handling
7. Add input validation for query parameters
8. Implement pagination for inventory endpoint
9. Fix reconciler running flag handling

### Medium Priority (P2-P3)
10. Fix handleExtendSession error handling
11. Add graceful shutdown for background refresh goroutines
12. Add mutex protection for lastSSHHealthCheck
13. Standardize 404 response format

### Low Priority (P3)
14. Add WorkloadType validation
15. Consider CORS support
16. Consider metrics endpoint authentication

---

## Test Session Timeline

| Time | Event |
|------|-------|
| 15:29:33 | Session created (pending) |
| 15:29:34 | Instance created (provider_id: 30802335) |
| 15:29:34 | SSH verification started |
| 15:29:50 | SSH attempt 1 failed (connection_refused) |
| 15:30:05 | SSH attempt 2 failed (connection_refused) |
| 15:30:31 | SSH attempt 3 succeeded (56s total) |
| 15:31:03 | Session destroyed |

---

## Files Requiring Changes

| File | Issues |
|------|--------|
| `internal/api/handlers.go` | 9 issues |
| `internal/service/lifecycle/reconciler.go` | 2 issues |
| `internal/service/lifecycle/manager.go` | 2 issues |
| `internal/service/provisioner/service.go` | 3 issues |
| `internal/service/inventory/service.go` | 2 issues |
| `internal/storage/db.go` | 1 issue |
| `internal/ssh/verifier.go` | 1 issue (documented behavior) |

---

## Recommendations

1. **Add integration tests for error responses** - Many issues are incorrect HTTP status codes
2. **Add race detector to CI** - Several race conditions identified
3. **Consider context-aware sleeps** - Use `select` with `ctx.Done()` instead of `time.Sleep()`
4. **Add request validation middleware** - Centralize numeric parameter validation
5. **Standardize error response format** - Create error types that map to HTTP status codes

---

## Session 2: Extended Monitoring (Bugs 32-42)

*Additional monitoring session with focus on edge cases and provider-specific issues.*

### TensorDock Provider Issues

#### #32 - P2: `min_reliability` Filter Not Implemented

**File:** `internal/api/handlers.go:149-195`

**Problem:** The `min_reliability` query parameter is defined in `OfferFilter` but never parsed from query parameters.

```bash
curl "http://localhost:8080/api/v1/inventory?min_reliability=0.5&provider=tensordock"
# Returns 24 offers (all TensorDock) - filter ignored
curl "http://localhost:8080/api/v1/inventory?min_reliability=2.0&provider=tensordock"
# Still returns 24 offers - impossible value not rejected
```

---

#### #33 - P3: TensorDock GPU Names Not Normalized

**File:** `internal/provider/tensordock/types.go` (normalizeGPUName)

**Problem:** Some GPU names come through raw: `a40-pcie-48gb` instead of `A40`, `geforcertx5090-pcie-32gb` instead of `RTX 5090`

**Impact:** Cross-provider filtering by `gpu_type` doesn't work consistently.

---

#### #34 - P3: Reliability Values Exceed 1.0 for TensorDock

**File:** `internal/provider/tensordock/client.go` (locationGPUToOffer)

**Problem:** Calculation `float64(loc.Tier) / 3.0` produces values > 1.0 when Tier is 4.

```json
{"id": "tensordock-...-rtxa5000-pcie-24gb", "reliability": 1.3333333333333333}
```

**Expected:** Reliability should be clamped to [0, 1].

---

#### #35 - P3: Duplicate Region in TensorDock Location

**File:** `internal/provider/tensordock/client.go`

**Problem:** Some locations have duplicate info: `"Texas, Texas, United States"`

---

### Race Conditions

#### #36 - P1: Session Destroy Race Condition - No Idempotency

**File:** `internal/service/provisioner/service.go` (DestroySession)

**Problem:** Multiple concurrent DELETE requests all succeed, causing:
- 5+ redundant provider API calls
- 5+ duplicate AUDIT "session_destroyed" events
- No mutex/lock protection on destroy operation

**Evidence from logs:**
```
Multiple concurrent "destroying session" logs for same session_id
Multiple DELETE requests returning 200 (latencies 5.2-8.2 seconds)
```

---

### API Issues (Continued)

#### #37 - P2: Intermittent 500 on GET /api/v1/inventory

**Evidence:**
```json
{"status":500,"latency":21500,"path":"/api/v1/inventory"}
```

**Problem:** Random 500 errors on inventory endpoint (21ms latency - not timeout). Unhandled error condition.

---

#### #38 - P2: `gpu_count` Filter Does Not Work

**Endpoint:** `GET /api/v1/inventory?gpu_count=N`

**Problem:** All values (0, 1, 2, 100) return the same count of offers.

```bash
curl "http://localhost:8080/api/v1/inventory?gpu_count=1" | jq '.count'   # 88
curl "http://localhost:8080/api/v1/inventory?gpu_count=100" | jq '.count' # 88
```

---

#### #39 - P3: Costs Endpoint Accepts Invalid Date Range

**Endpoint:** `GET /api/v1/costs?start_date=2024-12-31&end_date=2024-01-01`

**Problem:** Accepts end_date < start_date without validation error.

---

#### #40 - P3: Accept Header Ignored

**Problem:** API always returns JSON regardless of Accept header. No 406 Not Acceptable for unsupported types.

```bash
curl -H "Accept: application/xml" http://localhost:8080/api/v1/inventory
# Returns JSON, not 406
```

---

#### #41 - P3: `gpu_type` Requires Exact Match

**Endpoint:** `GET /api/v1/inventory?gpu_type=RTX`

**Problem:** Returns 0 results. No partial/prefix matching. Must use exact type like "RTX 3090".

---

#### #42 - P3: Malformed Location Data

**Problem:** Some offers have empty or incomplete location: `""`, `", CN"`, `", US"`

```bash
curl http://localhost:8080/api/v1/inventory | jq '.offers | map(.location) | unique | .[:5]'
# ["", ", CN", ", US", "Australia, AU", ...]
```

---

## Updated Files Requiring Changes

| File | Issues |
|------|--------|
| `internal/api/handlers.go` | 12 issues |
| `internal/service/lifecycle/reconciler.go` | 2 issues |
| `internal/service/lifecycle/manager.go` | 2 issues |
| `internal/service/provisioner/service.go` | 5 issues |
| `internal/service/inventory/service.go` | 2 issues |
| `internal/provider/tensordock/client.go` | 2 issues |
| `internal/provider/tensordock/types.go` | 1 issue |
| `internal/storage/db.go` | 1 issue |
| `internal/ssh/verifier.go` | 1 issue (documented) |
| `pkg/models/gpu.go` | 1 issue |

---

## Full Bug Index

| # | Severity | Description | File |
|---|----------|-------------|------|
| 1 | P1 | HTTP 500 for not-found on /done | handlers.go |
| 2 | P1 | Invalid provider returns 500 | handlers.go |
| 3 | P1 | Race in Reconciler.Stop() | reconciler.go |
| 4 | P1 | Context leak in destroyWithVerification | service.go |
| 5 | P1 | SQLite single connection | db.go |
| 6 | P1 | Session destroy race - no idempotency | service.go |
| 7 | P2 | Internal field names in validation | handlers.go |
| 8 | P2 | Costs returns zero dates | handlers.go |
| 9 | P2 | Pagination ignored | handlers.go |
| 10 | P2 | Invalid params silently ignored | handlers.go |
| 11 | P2 | Negative values accepted | handlers.go |
| 12 | P2 | max_price=0 returns all | handlers.go |
| 13 | P2 | Invalid status filter ignored | handlers.go |
| 14 | P2 | Reconciler running flag | reconciler.go |
| 15 | P2 | lastSSHHealthCheck unprotected | manager.go |
| 16 | P2 | handleExtendSession errors | handlers.go |
| 17 | P2 | Background refresh leak | inventory/service.go |
| 18 | P2 | Double cache read race | inventory/service.go |
| 19 | P2 | ProgressiveBackoff not thread-safe | service.go |
| 20 | P2 | min_reliability not implemented | handlers.go |
| 21 | P2 | Intermittent inventory 500 | handlers.go |
| 22 | P2 | gpu_count filter broken | handlers.go |
| 23 | P3 | Inconsistent 404 formats | handlers.go |
| 24 | P3 | /done on stopped session | handlers.go |
| 25 | P3 | HEAD request timeout | router |
| 26 | P3 | Missing CORS | router |
| 27 | P3 | Location filter exact match | models/gpu.go |
| 28 | P3 | Metrics publicly accessible | api |
| 29 | P3 | Session state race during destroy | manager.go |
| 30 | P3 | Insecure SSH host key | verifier.go |
| 31 | P3 | Missing WorkloadType validation | handlers.go |
| 32 | P3 | Diagnostics returns 404 for all | handlers.go |
| 33 | P3 | TensorDock GPU names not normalized | tensordock/types.go |
| 34 | P3 | Reliability > 1.0 | tensordock/client.go |
| 35 | P3 | Duplicate region in location | tensordock/client.go |
| 36 | P3 | Costs accepts invalid date range | handlers.go |
| 37 | P3 | Accept header ignored | handlers.go |
| 38 | P3 | gpu_type exact match only | models/gpu.go |
| 39 | P3 | Malformed location data | providers |

---

## Session 3: Deep Testing (Bugs 43-45)

*Focused testing on state machine, concurrency, and failure recovery.*

### P1 - Critical Safety/Reliability Issues

#### #43 - P1: Extend Session Bypasses 12-Hour Hard Max

**File:** `internal/service/lifecycle/manager.go:500-519`

**Problem:** Multiple extensions can exceed the 12-hour safety limit. The API validates single extension (max=12) but not cumulative duration.

**Evidence:**
```bash
# Create 11-hour session
POST /api/v1/sessions -> session_id: 5ff03cde-6e8d-40e1-afce-320e980560a6

# Extend by 2 hours
POST /api/v1/sessions/{id}/extend {"additional_hours": 2}
# Result: HTTP 200 - session now has 13 hours (exceeds 12h limit)
```

**Impact:** Sessions can run indefinitely via repeated small extensions, bypassing the safety rule that requires CLI override for >12 hours.

**Suggested Fix:**
```go
totalDuration := session.ReservationHrs + additionalHours
if !session.HardMaxOverride && totalDuration > m.hardMaxHours {
    return errors.New("extension would exceed 12-hour hard max")
}
```

---

#### #44 - P1: Orphaned Pending Sessions on Provider Failure

**File:** `internal/service/provisioner/service.go` (CreateSession)

**Problem:** When provider instance creation fails AFTER the DB session record is created, the error path doesn't clean up the orphaned session.

**Evidence from logs:**
```
Session cc7e7482-6d95-4fdd-a632-019014c3866e created status="pending"
POST /api/v1/sessions returns 500 (493ms)
→ Session remains in "pending" state forever
```

**Impact:**
- Database accumulates zombie "pending" sessions
- Not caught by orphan detection (which looks for running provider instances)
- Resource accounting issues

**Root cause:** Transaction not rolled back on provider API failure.

---

### P2 - Reliability Issues

#### #45 - P2: Connection Resets Under Burst Traffic

**File:** `internal/api/server.go`

**Problem:** Server cannot handle high rate of new TCP connections. Default `http.Server` settings + OS TCP backlog (~128) get overwhelmed.

**Evidence:**
```
Batch 400: Success=238, Errors=162 (40.5%)
Errors: "connection reset by peer" (82x), "broken pipe" (6x)
```

**Note:** With HTTP keep-alive (connection reuse), 500 concurrent requests succeed with 0 errors.

**Impact:** Burst traffic from multiple clients during deployments/autoscaling will see connection failures.

---

## Complete Bug Index (45 Total)

| # | Sev | Description | File |
|---|-----|-------------|------|
| 1 | P1 | HTTP 500 for not-found on /done | handlers.go |
| 2 | P1 | Invalid provider returns 500 | handlers.go |
| 3 | P1 | Race in Reconciler.Stop() | reconciler.go |
| 4 | P1 | Context leak in destroyWithVerification | service.go |
| 5 | P1 | SQLite single connection | db.go |
| 6 | P1 | Session destroy race - no idempotency | service.go |
| 7 | P1 | Extend bypasses 12-hour hard max | manager.go |
| 8 | P1 | Orphaned pending sessions on provider fail | service.go |
| 9 | P2 | Internal field names in validation | handlers.go |
| 10 | P2 | Costs returns zero dates | handlers.go |
| 11 | P2 | Pagination ignored | handlers.go |
| 12 | P2 | Invalid params silently ignored | handlers.go |
| 13 | P2 | Negative values accepted | handlers.go |
| 14 | P2 | max_price=0 returns all | handlers.go |
| 15 | P2 | Invalid status filter ignored | handlers.go |
| 16 | P2 | Reconciler running flag | reconciler.go |
| 17 | P2 | lastSSHHealthCheck unprotected | manager.go |
| 18 | P2 | handleExtendSession errors | handlers.go |
| 19 | P2 | Background refresh leak | inventory/service.go |
| 20 | P2 | Double cache read race | inventory/service.go |
| 21 | P2 | ProgressiveBackoff not thread-safe | service.go |
| 22 | P2 | min_reliability not implemented | handlers.go |
| 23 | P2 | Intermittent inventory 500 | handlers.go |
| 24 | P2 | gpu_count filter broken | handlers.go |
| 25 | P2 | Connection resets under burst | server.go |
| 26 | P3 | Inconsistent 404 formats | handlers.go |
| 27 | P3 | /done on stopped session | handlers.go |
| 28 | P3 | HEAD request timeout | router |
| 29 | P3 | Missing CORS | router |
| 30 | P3 | Location filter exact match | models/gpu.go |
| 31 | P3 | Metrics publicly accessible | api |
| 32 | P3 | Session state race during destroy | manager.go |
| 33 | P3 | Insecure SSH host key | verifier.go |
| 34 | P3 | Missing WorkloadType validation | handlers.go |
| 35 | P3 | Diagnostics returns 404 for all | handlers.go |
| 36 | P3 | TensorDock GPU names not normalized | tensordock/types.go |
| 37 | P3 | Reliability > 1.0 | tensordock/client.go |
| 38 | P3 | Duplicate region in location | tensordock/client.go |
| 39 | P3 | Costs accepts invalid date range | handlers.go |
| 40 | P3 | Accept header ignored | handlers.go |
| 41 | P3 | gpu_type exact match only | models/gpu.go |
| 42 | P3 | Malformed location data | providers |
| 43 | P3 | Extend by 0 treated as missing | handlers.go |
| 44 | P3 | /audit endpoint not implemented | api |
| 45 | P3 | High latency variance under load | db.go |

---

## Session 4: Extended Monitoring (Bugs 46-51)

*Additional monitoring with focus on metrics, cost tracking, and provider consistency.*

### P0 - Critical Bugs

#### #46 - P0: Missing UpdateSessionStatus Causes Negative Gauge Values

**File:** `internal/service/provisioner/service.go:635, 682, 976`

**Problem:** The `metrics.UpdateSessionStatus()` function is not called when sessions transition to "running" or "stopping" states. This causes the Prometheus gauge `gpu_sessions_active` to report negative values since it decrements status that was never incremented.

**Evidence:**
```go
// Line 635: Session marked running after SSH verification
session.Status = models.SessionStatusRunning
// MISSING: metrics.UpdateSessionStatus(provider, "ssh_verification", "running")

// Line 682: Session marked stopping before destroy
session.Status = models.SessionStatusStopping
// MISSING: metrics.UpdateSessionStatus(provider, "running", "stopping")

// Line 976: Session marked running after API verification
session.Status = models.SessionStatusRunning
// MISSING: metrics.UpdateSessionStatus call
```

**Impact:** Prometheus dashboards and alerts show impossible negative values. Monitoring/alerting systems become unreliable.

**Live Evidence (observed during monitoring):**
```
gpu_sessions_active{provider="vastai",status="running"} -1
gpu_sessions_active{provider="vastai",status="stopping"} -3
```

---

#### #47 - P1: Race Condition in Duplicate Session Detection

**File:** `internal/service/provisioner/service.go:299-350`

**Problem:** Between checking for existing sessions and creating a new one, another request can create a session with the same consumer_id + offer_id, leading to duplicate sessions.

**Sequence:**
1. Request A: `checkDuplicateSession()` returns nil (no duplicate)
2. Request B: `checkDuplicateSession()` returns nil (no duplicate)
3. Request A: Creates session record
4. Request B: Creates duplicate session record
5. Both proceed to provision

**Impact:** Same user can have multiple active sessions for the same GPU offer, double-spending and wasting resources.

**Suggested Fix:** Use database-level unique constraint on (consumer_id, offer_id, status='running') or implement distributed locking.

---

### P2 - High Priority Bugs

#### #48 - P2: No Circuit Breaker in Vast.ai Provider

**File:** `internal/provider/vastai/client.go`

**Problem:** Unlike TensorDock which has `ProviderCircuitBreakerState` metric support, Vast.ai provider has no circuit breaker pattern implemented. When Vast.ai API is down or rate-limited, every request hits the failing endpoint.

**Comparison:**
- TensorDock: Circuit breaker state tracked via `UpdateProviderCircuitBreakerState()`
- Vast.ai: No circuit breaker, no backoff, no state tracking

**Impact:** Provider failures cascade to users with slow timeouts instead of fast fails.

---

#### #49 - P2: Cost Endpoint Returns $0 for Nonexistent Sessions

**File:** `internal/api/handlers.go:481-495`

**Problem:** When querying costs for a session that doesn't exist, the API returns a 200 OK with all zeros instead of 404 Not Found.

```bash
curl http://localhost:8080/api/v1/costs?session_id=00000000-0000-0000-0000-000000000000
# Returns: {"total_cost": 0, "hours_used": 0, ...}  HTTP 200
# Expected: {"error": "session not found"} HTTP 404
```

---

#### #50 - P2: Non-Atomic Session Create After Provider Success

**File:** `internal/service/provisioner/service.go:350-400`

**Problem:** After provider successfully creates an instance, if the database update fails, the instance is orphaned. The session creation and DB update should be in a transaction, or have rollback logic.

**Sequence:**
1. DB: Create session record (status=pending)
2. Provider: Create instance (SUCCESS - instance now running)
3. DB: Update session with provider_id (FAILS - database error)
4. Result: Running instance with no corresponding session record

**Note:** This is related to but distinct from bug #8 (orphaned pending sessions). This covers the opposite case where the provider succeeds but DB fails.

---

### P3 - Low Priority Bugs

#### #51 - P3: hours_used Type Mismatch in Cost Response

**File:** `internal/api/handlers.go` (cost response struct)

**Problem:** The `hours_used` field is returned as a float but documented as integer hours. Clients may truncate or round incorrectly.

```json
{"hours_used": 1.5833333333}  // Actual
{"hours_used": 2}  // Expected per docs
```

---

## Updated Statistics

**Total Bugs Found:** 51

**Breakdown by Severity:**
- **P0 (Critical):** 1
- **P1 (High):** 9
- **P2 (Medium):** 22
- **P3 (Low):** 19

---

## Updated Complete Bug Index (51 Total)

| # | Sev | Description | File |
|---|-----|-------------|------|
| 1 | P1 | HTTP 500 for not-found on /done | handlers.go |
| 2 | P1 | Invalid provider returns 500 | handlers.go |
| 3 | P1 | Race in Reconciler.Stop() | reconciler.go |
| 4 | P1 | Context leak in destroyWithVerification | service.go |
| 5 | P1 | SQLite single connection | db.go |
| 6 | P1 | Session destroy race - no idempotency | service.go |
| 7 | P1 | Extend bypasses 12-hour hard max | manager.go |
| 8 | P1 | Orphaned pending sessions on provider fail | service.go |
| 9 | P2 | Internal field names in validation | handlers.go |
| 10 | P2 | Costs returns zero dates | handlers.go |
| 11 | P2 | Pagination ignored | handlers.go |
| 12 | P2 | Invalid params silently ignored | handlers.go |
| 13 | P2 | Negative values accepted | handlers.go |
| 14 | P2 | max_price=0 returns all | handlers.go |
| 15 | P2 | Invalid status filter ignored | handlers.go |
| 16 | P2 | Reconciler running flag | reconciler.go |
| 17 | P2 | lastSSHHealthCheck unprotected | manager.go |
| 18 | P2 | handleExtendSession errors | handlers.go |
| 19 | P2 | Background refresh leak | inventory/service.go |
| 20 | P2 | Double cache read race | inventory/service.go |
| 21 | P2 | ProgressiveBackoff not thread-safe | service.go |
| 22 | P2 | min_reliability not implemented | handlers.go |
| 23 | P2 | Intermittent inventory 500 | handlers.go |
| 24 | P2 | gpu_count filter broken | handlers.go |
| 25 | P2 | Connection resets under burst | server.go |
| 26 | P3 | Inconsistent 404 formats | handlers.go |
| 27 | P3 | /done on stopped session | handlers.go |
| 28 | P3 | HEAD request timeout | router |
| 29 | P3 | Missing CORS | router |
| 30 | P3 | Location filter exact match | models/gpu.go |
| 31 | P3 | Metrics publicly accessible | api |
| 32 | P3 | Session state race during destroy | manager.go |
| 33 | P3 | Insecure SSH host key | verifier.go |
| 34 | P3 | Missing WorkloadType validation | handlers.go |
| 35 | P3 | Diagnostics returns 404 for all | handlers.go |
| 36 | P3 | TensorDock GPU names not normalized | tensordock/types.go |
| 37 | P3 | Reliability > 1.0 | tensordock/client.go |
| 38 | P3 | Duplicate region in location | tensordock/client.go |
| 39 | P3 | Costs accepts invalid date range | handlers.go |
| 40 | P3 | Accept header ignored | handlers.go |
| 41 | P3 | gpu_type exact match only | models/gpu.go |
| 42 | P3 | Malformed location data | providers |
| 43 | P3 | Extend by 0 treated as missing | handlers.go |
| 44 | P3 | /audit endpoint not implemented | api |
| 45 | P3 | High latency variance under load | db.go |
| 46 | P0 | Missing UpdateSessionStatus (negative gauges) | service.go |
| 47 | P1 | Race in duplicate session detection | service.go |
| 48 | P2 | No circuit breaker in Vast.ai | vastai/client.go |
| 49 | P2 | Cost returns $0 for nonexistent session | handlers.go |
| 50 | P2 | Non-atomic session create after provider | service.go |
| 51 | P3 | hours_used type mismatch | handlers.go |
| 52 | P2 | GetOffer skips staleness degradation | inventory/service.go |
| 53 | P2 | Cost tracker Stop() has same race as Reconciler | cost/tracker.go |
| 54 | P3 | No verification goroutine wait on shutdown | service.go |
| 55 | P3 | Unbounded slice growth in session listing | sessions.go |
| 56 | P3 | Invalid `period` param silently falls through | handlers.go |
| 57 | P2 | Provisioning duration metric never recorded | service.go |
| 58 | P3 | 0 reservation hours treated as missing not min | handlers.go |
| 59 | P3 | Vast.ai error messages not length-sanitized | vastai/client.go |
| 60 | P2 | Vast.ai offer ID prefix case-sensitive | vastai/client.go |
| 61 | P2 | TensorDock offer ID without prefix fails | tensordock/client.go |
| 62 | P1 | TensorDock HTTP 401 during provisioning | tensordock/client.go |
| 63 | P3 | stopped_at not exposed in API response | session.go |
| 64 | P2 | Cost and budget metrics never recorded | metrics.go |
| 65 | P3 | TOCTOU race in SSH verification goroutine | service.go |
| 66 | P3 | Silent error swallowing in migrations | db.go |

---

## Session 5: Cache Staleness Agent Findings (Bug 52)

#### #52 - P2: GetOffer Skips Staleness Degradation

**File:** `internal/service/inventory/service.go:395-424`

**Problem:** The `GetOffer()` method returns offers directly from cache without applying `applyStalenessDegradation()`. When inventory data is stale (>2 minutes old), the `ListOffers()` endpoint applies confidence degradation but `GetOffer()` returns the original confidence value.

**Code:**
```go
func (s *Service) GetOffer(ctx context.Context, offerID string) (*models.GPUOffer, error) {
    // ...
    for _, offer := range cached.offers {
        if offer.ID == offerID {
            s.mu.RUnlock()
            return &offer, nil  // BUG: No staleness degradation applied
        }
    }
    // ...
}
```

**Impact:** Clients using `/api/v1/inventory/:id` see artificially high confidence values for stale data, while clients using `/api/v1/inventory` see correctly degraded values. This inconsistency can lead to provisioning failures.

**Fix:** Apply staleness degradation before returning:
```go
adjusted := s.applyStalenessDegradation(offer)
return &adjusted, nil
```

---

## Session 6: Memory Leak Agent Findings (Bugs 53-55)

#### #53 - P2: Cost Tracker Stop() Has Same Race as Reconciler

**File:** `internal/service/cost/tracker.go:182-199`

**Problem:** Same race condition pattern as Reconciler.Stop() (bug #3). The function releases the lock before closing the channel, allowing a potential race with concurrent `Start()` calls.

**Fix:** Capture channel references while holding lock (same pattern as Manager.Stop()).

---

#### #54 - P3: No Verification Goroutine Wait on Shutdown

**File:** `internal/service/provisioner/service.go:470-493` and `cmd/server/main.go`

**Problem:** The provisioner spawns verification goroutines with `s.verifyWg.Add(1)` but `WaitForVerificationComplete()` is never called during server shutdown. Verification goroutines could still be running when the server stops.

**Impact:** Potential resource leaks or incomplete state during shutdown. Sessions may remain in provisioning state.

**Fix:** Add `provService.WaitForVerificationComplete(shutdownTimeout)` to the shutdown sequence in `cmd/server/main.go`.

---

#### #55 - P3: Unbounded Slice Growth in Session Listing

**File:** `internal/storage/sessions.go:211,241`

**Problem:** The `ListInternal` function appends to a slice without pre-allocation when no limit is specified. For large databases, this causes multiple reallocations.

**Impact:** Performance degradation with large session counts.

**Fix:** Pre-allocate with estimated capacity or query COUNT first.

---

## Updated Statistics

**Total Bugs Found:** 55

**Breakdown by Severity:**
- **P0 (Critical):** 1
- **P1 (High):** 9
- **P2 (Medium):** 26
- **P3 (Low):** 23

---

## Session 7: Cost Tracking Agent Finding (Bug 56)

#### #56 - P3: Invalid `period` Parameter Silently Falls Through

**File:** `internal/api/handlers.go:501-536`

**Problem:** The GET `/api/v1/costs` endpoint accepts a `period` query parameter with values "daily" or "monthly". Any other value (e.g., "invalid", "weekly", "yearly") silently falls through to the default case which treats it as a custom date range query with empty dates.

**Evidence:**
```bash
curl -s "http://localhost:8080/api/v1/costs?period=invalid"
# Returns all costs with zero period_start/end (same as no filter)
```

**Expected:** Return 400 Bad Request for unknown period values.

**Impact:** Users may expect an error for invalid periods but get unexpected results instead.

---

## Session 8: Metrics Agent Confirmation (Bug 57)

The metrics agent confirmed bug #46 with exact code locations:
- Line 635: SSH verification success (missing UpdateSessionStatus)
- Line 682: Transition to stopping (missing UpdateSessionStatus)
- Line 976: API verification success (missing UpdateSessionStatus)
- Lines 799-808: failSession function (missing UpdateSessionStatus)

#### #57 - P2: Provisioning Duration Metric Never Recorded

**File:** `internal/service/provisioner/service.go`

**Problem:** The metric `gpu_provisioning_duration_seconds` is defined in metrics.go with `RecordProvisioningDuration()` helper, but it is never called after successful provisioning.

**Impact:** Prometheus dashboards showing provisioning time trends will always show empty data. Unable to track how long provisioning takes across providers.

**Fix:** Add call to `metrics.RecordProvisioningDuration(provider, duration)` after successful session provisioning.

---

## Session 9: Provider Error Path Agent Findings (Bugs 58-59)

#### #58 - P3: Reservation Hours 0 Treated as Missing, Not Min Violation

**File:** `internal/api/handlers.go`

**Problem:** When `reservation_hours: 0` is sent in a create session request, the error says "failed on 'required' tag" instead of "failed on 'min' tag". This is because Go's `omitempty` treats 0 as the zero value.

**Evidence:**
```bash
curl -X POST ... -d '{"...","reservation_hours":0}'
# Returns: "Field validation for 'ReservationHrs' failed on the 'required' tag"
# Should return: validation failed on 'min' tag
```

**Impact:** Confusing error message for API consumers.

---

#### #59 - P3: Vast.ai Error Messages Not Length-Sanitized

**File:** `internal/provider/vastai/client.go:518-534`

**Problem:** The Vast.ai client reads the entire error response body without length limits:
```go
func (c *Client) handleError(resp *http.Response, operation string) error {
    body, _ := io.ReadAll(resp.Body)
    message := string(body)  // No sanitization!
}
```

TensorDock properly sanitizes with `sanitizeErrorMessage()`.

**Impact:** A malicious or buggy provider API could return massive error messages causing memory issues.

---

---

## Session 10: Failure Pattern Analysis (Bugs 60-62)

Analysis of 45 failed sessions revealed three new bugs related to offer ID handling and provider authentication.

#### #60 - P2: Vast.ai Offer ID Prefix Matching is Case-Sensitive

**File:** `internal/provider/vastai/client.go:218-221`

**Problem:** The offer ID prefix check uses exact string matching:
```go
if strings.HasPrefix(offerID, "vastai-") {  // Only matches lowercase!
    offerID = strings.TrimPrefix(offerID, "vastai-")
}
bundleID, err := strconv.Atoi(offerID)
```

If an offer ID arrives with a different case (e.g., "Vastai-30032902", "VASTAI-30032902") or hidden whitespace, the prefix is not stripped and strconv.Atoi fails.

**Live Evidence:**
```
Error: provider create failed: invalid offer ID: strconv.Atoi: parsing "vastai-30032902": invalid syntax
```

**Impact:** 1 failed session during monitoring period. Provisioning fails silently.

**Fix:** Use case-insensitive matching:
```go
if strings.HasPrefix(strings.ToLower(offerID), "vastai-") {
    offerID = offerID[7:]  // Skip prefix regardless of case
}
```

---

#### #61 - P2: TensorDock Offer ID Without Prefix Causes Failure

**File:** `internal/provider/tensordock/client.go:1300-1306`

**Problem:** TensorDock's parseOfferID requires the "tensordock-" prefix:
```go
if !strings.HasPrefix(offerID, prefix) {
    return "", "", fmt.Errorf("invalid offer ID format (missing prefix): %s", offerID)
}
```

If an offer ID is passed without the prefix (e.g., from manual API call or misconfigured client), provisioning fails.

**Live Evidence:**
```
Error: provider create failed: invalid offer ID format: 1a779525-4c04-4f2c-aa45-58b47d54bb38
```

**Impact:** 3 failed sessions during monitoring period.

**Fix:** Either accept bare UUIDs or provide clearer error messages explaining the expected format.

---

#### #62 - P1: TensorDock HTTP 401 Unauthorized During Provisioning

**Problem:** 8 sessions failed with TensorDock HTTP 401 errors. This indicates either:
1. API credentials expired or rotated mid-session
2. Rate limiting triggering auth rejection
3. Credential leakage between concurrent requests

**Live Evidence:**
```
Error: provider create failed: tensordock CreateInstance failed (HTTP 401): Unauthorized
```

**Count:** 8 failed sessions

**Impact:** Users cannot provision TensorDock instances. This is a P1 because it completely blocks a provider.

**Investigation Needed:**
- Check if TensorDock rotates API tokens
- Check if there's request rate limiting
- Verify credential handling in concurrent scenarios

---

## Session 11: API Response Validation (Bug 63)

#### #63 - P3: stopped_at Timestamp Not Exposed in API Response

**File:** `pkg/models/session.go:111-130`

**Problem:** The `SessionResponse` struct doesn't include a `StoppedAt` field. The value is correctly stored in the database but not exposed to API consumers.

**Evidence:**
```bash
# Database shows stopped_at is set:
sqlite3 data/gpu-shopper.db "SELECT stopped_at FROM sessions WHERE status='stopped' LIMIT 1"
# Output: 2026-01-30 09:59:11.199415-05:00

# But API response omits the field:
curl -s http://localhost:8080/api/v1/sessions/42fbf03b-... | jq .stopped_at
# Output: null (field missing)
```

**Impact:** Clients cannot determine when a session was stopped. This affects:
- Cost calculation for partial hours
- Audit trails
- Session duration reporting

**Fix:** Add `StoppedAt` field to `SessionResponse`:
```go
StoppedAt time.Time `json:"stopped_at,omitempty"`
```

---

## Session 12: Metrics Coverage Analysis (Bug 64)

#### #64 - P2: Cost and Budget Metrics Never Recorded

**File:** `internal/metrics/metrics.go:296-304`

**Problem:** Two Prometheus metric helper functions are defined but never called anywhere in the codebase:
1. `RecordCost(provider string, amount float64)` - for `gpu_cost_accrued_usd` counter
2. `RecordBudgetAlert(alertType string)` - for `gpu_budget_alerts_total` counter

**Evidence:**
```bash
# Metrics not appearing in output:
curl -s http://localhost:8080/metrics | grep -E "gpu_cost_accrued|gpu_budget"
# No output

# Grep shows functions are never called:
grep -r "metrics.RecordCost" internal/
# No matches
grep -r "metrics.RecordBudgetAlert" internal/
# No matches
```

**Impact:** Cost dashboards and budget alerting via Prometheus will never have data. This affects:
- Real-time cost monitoring
- Budget threshold alerts
- Provider cost comparison

**Note:** This is the same pattern as Bug #57 (provisioning duration metric never recorded).

**Fix:** Call `metrics.RecordCost()` in the cost tracker when costs are recorded, and `metrics.RecordBudgetAlert()` when budget thresholds are crossed.

---

## Session 13: Database Consistency Agent Findings (Bugs 65-66)

#### #65 - P3: TOCTOU Race in SSH Verification Goroutine

**File:** `internal/service/provisioner/service.go:575-636`

**Problem:** The SSH verification background goroutine reads session state, checks conditions, then updates. Between the read and update, another process (e.g., user-initiated destroy) could modify the session.

```go
// Line 575: Get current session state
session, err := s.store.Get(ctx, sessionID)
// ... check state ...
// Line 636: Update session to running
session.Status = models.StatusRunning
if err := s.store.Update(ctx, session); err != nil { ... }
```

**Mitigating Factor:** The code checks `session.IsTerminal()` before updating, but there's still a time-of-check-time-of-use window.

**Impact:** Low - could cause unexpected state transitions if a destroy races with verification completion.

**Fix:** Add optimistic locking with a version column, or use database-level constraints.

---

#### #66 - P3: Silent Error Swallowing in Database Migrations

**File:** `internal/storage/db.go:72-84`

**Problem:** Migration errors are silently ignored for idempotency:

```go
for _, migration := range dropColumnMigrations {
    _, _ = db.ExecContext(ctx, migration) // Ignore errors for idempotency
}

for _, migration := range indexMigrations {
    _, _ = db.ExecContext(ctx, migration) // Ignore errors for idempotency
}
```

**Impact:** Real errors (disk full, corrupted database, permission denied) would be masked, potentially leaving the database in an inconsistent state without any warning.

**Fix:** Check error types and only ignore expected errors (like "column already exists"), log others.

---

## Updated Final Statistics

**Total Bugs Found:** 66

**Breakdown by Severity:**
- **P0 (Critical):** 1
- **P1 (High):** 10
- **P2 (Medium):** 29
- **P3 (Low):** 26

---

## Bugs Found During Extended Monitoring (19:00 - 19:51)

### #67 - P2: Metrics Not Initialized From Database State on Startup

**Problem:** When the server starts, all Prometheus gauges initialize to 0 regardless of existing session states in the database. This causes metric values to become incorrect after restarts.

**Impact:** 
- Metrics don't reflect true session state after server restart
- Negative gauge values persist from pre-fix sessions
- Monitoring dashboards show inaccurate data

**Current Behavior:**
```
# Server starts with sessions in DB in various states
# All gauges start at 0
gpu_sessions_active{status="running"} 0  # Even if 3 sessions are running in DB
```

**Expected Behavior:**
```
# On startup, scan DB and initialize gauges
gpu_sessions_active{status="running"} 3  # Matches DB state
```

**Fix:** Add initialization function in `internal/metrics/` that queries the database on startup and sets initial gauge values based on actual session states.

---

### #68 - P2: Ghost Fix Path Causes Negative Provisioning Gauge

**File:** `internal/service/lifecycle/reconciler.go` (ghost detection)

**Problem:** When ghost detection finds a session in DB but not on provider, it updates the session to "stopped". The metrics transition decrements the old status gauge even if it's already at 0.

**Observed:**
```
# Ghost detected for TensorDock session in "provisioning" state
# Before: tensordock/provisioning = 0
# After ghost fix: tensordock/provisioning = -1
```

**Root Cause:** The ghost fix calls `metrics.UpdateSessionStatus(oldStatus, "stopped")` but the oldStatus gauge may already be at 0 due to:
1. Gauge not initialized from DB (Bug #67)
2. Previous ghost fix already decremented it

**Fix:** In ghost detection path, check if session is already tracked in metrics before decrementing, or ensure metrics are properly initialized on startup.

---

### #69 - P3: High TensorDock Ghost Detection Rate

**Observation:** During monitoring, 2 out of 3 TensorDock sessions (67%) became ghosts - instances existed in our DB but disappeared from the provider.

**Sessions Affected:**
- `2e2bf8e6` - Ghost detected ~13 min after provisioning
- `0e025ae5` - Ghost detected ~14 min after provisioning

**Possible Causes:**
1. TensorDock terminates instances that don't respond to health checks
2. Instance creation succeeds but instance is immediately terminated
3. API timing issue - instance not yet visible or already terminated

**Impact:** Poor user experience, wasted resources, confusing metrics.

**Investigation Needed:**
- Check TensorDock dashboard for instance termination reasons
- Add more detailed logging during provisioning
- Consider increasing polling frequency during initial provisioning

---

## Updated Final Statistics

**Total Bugs Found:** 69

**Breakdown by Severity:**
- **P0 (Critical):** 1
- **P1 (High):** 10
- **P2 (Medium):** 31
- **P3 (Low):** 27


---

## Bugs Found by Agent Team (20:18 - 20:28)

### API Stress Testing Agent Findings

#### #70 - P2: SignalDone returns success for terminal sessions
**Endpoint:** `POST /api/v1/sessions/{id}/done`
**Problem:** Returns 200 OK with "session shutdown initiated" on already-stopped sessions.
**Expected:** Return 409 Conflict indicating session is already terminal.
**File:** `internal/service/lifecycle/manager.go:489`

#### #71 - P1: ExtendSession allows extending "stopping" sessions
**Endpoint:** `POST /api/v1/sessions/{id}/extend`
**Problem:** Sessions in "stopping" state can be extended.
**Expected:** Return error - session cannot be extended while stopping.
**File:** `internal/service/lifecycle/manager.go:506`

#### #72 - P2: Negative/zero limit returns all results
**Endpoint:** `GET /api/v1/sessions?limit=-1`
**Problem:** Negative values treated as "no limit" instead of validation error.
**File:** `internal/storage/sessions.go:201`

#### #73 - P3: Cost endpoint accepts invalid period values
**Endpoint:** `GET /api/v1/costs?period=invalid`
**Problem:** Invalid period returns data with `period_start: "0001-01-01T00:00:00Z"`.
**File:** `internal/api/handlers.go:519`

#### #74 - P3: Cost endpoint accepts end_date before start_date
**Endpoint:** `GET /api/v1/costs?start_date=2026-01-31&end_date=2026-01-01`
**Problem:** No validation of date range logic.
**File:** `internal/api/handlers.go:527-548`

#### #75 - P2: Session cost for non-existent session returns 0
**Endpoint:** `GET /api/v1/costs?session_id=nonexistent`
**Problem:** Returns 200 with total_cost=0 instead of 404.
**File:** `internal/api/handlers.go:499`

#### #76 - P2: No rate limiting on API endpoints
**Problem:** Rapid consecutive requests all processed without rate limiting.
**File:** `internal/api/server.go` (no rate limiting middleware)

#### #77 - P3: Invalid UUID format not validated before DB lookup
**Problem:** Any string accepted as session ID, query runs before format validation.
**File:** `internal/api/handlers.go` (session handlers)

#### #78 - P3: Non-numeric query parameters silently ignored
**Endpoint:** `GET /api/v1/inventory?min_vram=abc`
**Problem:** Invalid numeric filters silently ignored.
**File:** `internal/api/handlers.go:159-176`

#### #79 - P3: No max length validation on string inputs
**Problem:** Extremely long consumer_id values accepted.
**File:** `internal/api/handlers.go:36`

---

### Metrics Analysis Agent Findings

#### #90 - P0: Negative gpu_sessions_active gauge value
**Metric:** `gpu_sessions_active{provider="vastai",status="provisioning"} -1`
**Problem:** Gauges should never be negative.
**Root Cause:** Metrics not initialized from DB state on startup.
**File:** `internal/service/lifecycle/startup.go`

#### #91 - P1: Missing gpu_sessions_active label combinations
**Problem:** Only 2 label combinations exist in metrics despite 110 sessions across multiple states.
**Root Cause:** Gauges only reflect changes since restart, not true state.
**File:** `internal/service/lifecycle/startup.go`

#### #92 - P1: gpu_ssh_verify_failures_total shows 0 despite 12 failures
**Problem:** Counter at 0 but API shows 12 SSH timeout failures.
**Root Cause:** Counters reset on server restart.
**File:** `internal/service/provisioner/service.go:565`

#### #93 - P1: gpu_api_verify_failures_total shows 0 despite 3 failures
**Problem:** Counter at 0 but API shows 3 API timeout failures.
**File:** `internal/metrics/metrics.go`

#### #94 - P2: Missing gpu_sessions_created/destroyed metrics
**Problem:** Defined in metrics.go but never appear in /metrics output.
**Root Cause:** RecordSessionCreated/Destroyed never called.
**File:** `internal/service/provisioner/service.go`

#### #95 - P2: Missing gpu_cost_accrued_usd metric
**Problem:** Defined but not appearing despite costs being tracked.
**File:** `internal/service/cost/tracker.go`

#### #96 - P2: Missing gpu_provisioning_duration_seconds histogram
**Problem:** Defined but never instrumented.
**File:** `internal/service/provisioner/service.go`

#### #97 - P3: gpu_reconciliation_mismatches_total shows 0 despite ghosts
**Problem:** Counter at 0 but ghost errors exist in sessions.
**File:** `internal/service/lifecycle/reconciler.go`

---

### Session Lifecycle Agent Findings

#### #100 - P2: Missing provider filter for sessions list
**Endpoint:** `GET /api/v1/sessions?provider=tensordock`
**Problem:** Provider filter silently ignored, returns all sessions.
**File:** `internal/api/handlers.go:63-67`

#### #101 - P1: Session can be extended while in "stopping" state
(Duplicate of #71 - confirmed by lifecycle agent)

#### #102 - P3: Misleading response for /done on stopped session
(Duplicate of #70 - confirmed by lifecycle agent)

#### #103 - P1: Sessions stuck in "stopping" when provider unavailable
**Problem:** When provider API fails, sessions stuck in "stopping" indefinitely.
**Observed:** Session 0a0858c0 stuck in stopping state.
**File:** `internal/service/provisioner/service.go:700-703`

#### #104 - P3: API accepts negative limit values
(Duplicate of #72 - confirmed by lifecycle agent)

---

## Final Statistics (After Agent Team)

**Total Unique Bugs Found:** 92 (deduped from 104)

**Breakdown by Severity:**
- **P0 (Critical):** 2
- **P1 (High):** 16
- **P2 (Medium):** 41
- **P3 (Low):** 33


---

## Bugs Found During Session 2 Monitoring (20:38 - 20:47)

### #105 - P1: Session Stuck in Provisioning Without Provider ID

**Problem:** Session 849bb206 has been in "provisioning" state for 6+ minutes with no provider_id assigned. The instance was never created on the provider's side.

**Observed:**
```json
{
  "id": "849bb206-d471-4b86-964b-a26aaf1e7d19",
  "status": "provisioning",
  "ssh_host": null,
  "provider_id": null,
  "error": null,
  "created_at": "2026-01-31T20:39:35.333642-05:00"
}
```

**Log Analysis:** Only GET requests appear for this session - no provisioning activity logged.

**Root Cause (Suspected):** 
- Provider API call may have failed silently
- Session created in DB but provisioning goroutine never started or failed
- No timeout mechanism to fail sessions stuck in provisioning

**Impact:** Users see "provisioning" indefinitely with no feedback. Resources may be allocated but not tracked.

**Fix Needed:**
1. Add provisioning timeout (e.g., 5 minutes max in provisioning state)
2. Ensure provider API errors update session status to failed
3. Add better logging around session creation flow

---

## Updated Statistics

**Total Unique Bugs Found:** 93

**Breakdown by Severity:**
- **P0 (Critical):** 2
- **P1 (High):** 17
- **P2 (Medium):** 41
- **P3 (Low):** 33


---

### Log/Metrics Monitor Agent Findings (20:39 - 20:42)

#### #106 - P0: Negative gauge progressively worsening for pending status

**Metric:** `gpu_sessions_active{provider="vastai",status="pending"}`

**Problem:** Gauge goes progressively more negative with each session creation (-1 -> -2 -> -3). Not just initialization issue - actively getting worse.

**Evidence:**
```
gpu_sessions_active{provider="vastai",status="pending"} -3
```

**Impact:** Prometheus metrics fundamentally broken. Alerting produces false negatives.

**File:** `internal/service/provisioner/service.go` (state transitions)

---

#### #107 - P2: Session stuck in "stopping" with null provider_instance_id

**Session:** `0a0858c0-614a-4ded-bb04-821df45c3034`

**Problem:** Session in "stopping" for 30+ minutes with `provider_instance_id: null`. Has no provider instance to destroy, so destroy logic waits indefinitely.

**Fix:** Detect and clean up sessions in stopping state with no provider_instance_id.

**File:** `internal/service/lifecycle/manager.go`

---

#### #108 - P2: Massive mismatch between API session counts and metrics

**API shows:**
- failed: 57, stopped: 53, provisioning: 1, running: 1, stopping: 1

**Metrics show:**
- failed: 1, pending: -3, provisioning: 1, running: 1

**Missing:** 56 failed, 53 stopped, 1 stopping not tracked.

**File:** `internal/metrics/metrics.go`

---

#### #109 - P3: gpu_sessions_created_total counter undercounts

**Problem:** Counter shows 2 but database has 113 sessions.

**Root Cause:** Only tracks since last restart; RecordSessionCreated() not called consistently.

**File:** `internal/service/provisioner/service.go`

---

## Updated Final Statistics

**Total Unique Bugs Found:** 97

**Breakdown by Severity:**
- **P0 (Critical):** 3
- **P1 (High):** 17
- **P2 (Medium):** 43
- **P3 (Low):** 34


---

### Session Provisioning Tester Findings (20:40 - 20:46)

#### #110 - P2: Stale Inventory Cache Returns Unavailable Offers

**Problem:** Inventory returns offers 20+ minutes old that are no longer available on provider.

**Evidence:**
```
# Offer had fetched_at 20+ minutes old
# Provisioning failed with HTTP 400: no_such_ask - Instance not available
```

**Impact:** Users see offers that cannot be provisioned.

**File:** `internal/service/inventory/service.go`

---

#### #111 - P3: Session Stuck in Provisioning Without SSH Host (6+ minutes)

**Session:** `849bb206-d471-4b86-964b-a26aaf1e7d19`

**Problem:** Session in provisioning for 6+ minutes with no ssh_host populated.

**Impact:** Session may be stuck indefinitely if provider never returns connection details.

**File:** `internal/service/provisioner/service.go`

---

#### #112 - P1: Confirmed Session Stuck in Stopping 30+ Minutes

**Session:** `0a0858c0-614a-4ded-bb04-821df45c3034`

**Problem:** Stuck in "stopping" since 20:08 (30+ minutes). Confirms Bug #103.

**Impact:** Resources may still be running on provider, accumulating costs.

**File:** `internal/service/provisioner/service.go:700-703`

---

## Final Monitoring Session Statistics

**Total Unique Bugs Found:** 100

**Breakdown by Severity:**
- **P0 (Critical):** 3
- **P1 (High):** 18
- **P2 (Medium):** 44
- **P3 (Low):** 35

**Key Issues Identified:**
1. Metrics system fundamentally broken (negative gauges, missing counters)
2. Sessions stuck indefinitely in transitional states
3. Stale inventory cache causing provisioning failures
4. No timeout mechanism for provisioning or stopping states

