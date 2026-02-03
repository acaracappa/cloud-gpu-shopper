# Bug Report - Monitoring Session 2026-01-31

**Duration:** 12:38 - 14:54 (~136 minutes)
**Environment:** Production (localhost:8080)
**Providers:** Vast.ai + TensorDock
**Consumer:** `gpu-deploy-llm/v0.1.0`

---

## Summary

| Metric | Value |
|--------|-------|
| Sessions Created | 7+ |
| Sessions Successful | 6 |
| Sessions Failed | 1 |
| Errors Logged | 2 |
| Bugs Identified | 5 |

---

## Bugs

### BUG-1: SSH Verification Timeout Destroys Paid Instance

**Severity:** HIGH
**Status:** Open
**Component:** `internal/service/provisioner/service.go`

**Description:**
SSH verification timed out after 7 attempts (~5 minutes), causing the instance to be destroyed. Customer was billed for an instance they never got access to.

**Evidence:**
```
14:00:02 ERROR: SSH verification timeout, destroying instance
         session_id: a0402eb5-78ac-4360-aa95-63999321eb01
         attempts: 7
         gpu: RTX 3090
         host: ssh9.vast.ai:39810
```

**Impact:**
- Customer charged ~$0.06/hr for unusable instance
- Only RTX 3090 affected; all RTX 5060 Ti succeeded

**Recommendation:**
- Investigate ssh9.vast.ai connectivity
- Consider longer timeout for certain GPU types
- Add alerting for SSH verification failures
- Consider refund mechanism for failed provisions

---

### BUG-2: Inventory Cache Refresh Blocks Requests

**Severity:** MEDIUM
**Status:** Open
**Component:** `internal/service/inventory/service.go`

**Description:**
When inventory cache expires (~2 minute TTL), the next request blocks for 4-6+ seconds while fetching from both providers synchronously.

**Evidence:**
```
Sample latencies during cache refresh:
- 14:02:06: 5.32s
- 14:08:06: 5.70s
- 14:24:02: 5.92s
- 14:30:00: 6.59s (worst observed)

Cached responses: 130-500ms
```

**Impact:**
- Poor UX during cache refresh
- Client requests block for 4-6 seconds
- Appears to fetch providers sequentially, not in parallel

**Recommendation:**
- Implement background cache refresh (refresh before expiry)
- Fetch from providers in parallel
- Consider stale-while-revalidate pattern

---

### BUG-3: Missing Deployment ID Warning

**Severity:** MEDIUM
**Status:** Open
**Component:** `internal/service/lifecycle/reconciler.go`

**Description:**
Reconciler logs warning every 5 minutes that deployment ID is empty, causing all provider instances to be considered "ours".

**Evidence:**
```
13:58:49 WARN: deploymentID is empty; all provider instances will be considered ours
14:03:50 WARN: deploymentID is empty; all provider instances will be considered ours
14:08:49 WARN: deploymentID is empty; all provider instances will be considered ours
[repeated every 5 minutes]
```

**Impact:**
- Risk of incorrect orphan detection
- Could destroy instances from other deployments sharing the same provider account

**Recommendation:**
- Set `DEPLOYMENT_ID` environment variable in production
- Consider making this required in production mode
- Add startup validation

---

### BUG-4: SSH Verification Times Degrading

**Severity:** MEDIUM
**Status:** Open
**Component:** `internal/service/provisioner/service.go`

**Description:**
SSH verification times increased significantly over the monitoring period, from 90s to 200s for successful verifications.

**Evidence:**
| Session | SSH Verify Time | Attempts |
|---------|-----------------|----------|
| 579a630f | 90s | 4 |
| 0b4e1e81 | 139s | 5 |
| cfcfaa2e | 138s | 5 |
| 82f03c1c | 200s | 6 |

**Impact:**
- Users wait 1.5-3.5 minutes for session to be usable
- Trend is worsening (122% increase from first to last)

**Recommendation:**
- Log SSH attempt details (connection refused vs timeout vs auth fail)
- Add SSH verification time to Prometheus metrics
- Consider adaptive retry intervals
- Investigate if provider load affects boot times

---

### BUG-5: DELETE Non-Existent Session Returns 500

**Severity:** HIGH
**Status:** Open
**Component:** `internal/api/handlers.go`

**Description:**
DELETE request for a non-existent session returns HTTP 500 instead of 404.

**Evidence:**
```
14:29:14 DELETE /api/v1/sessions/82f03c1c-f27d-456f-9e10-6c9e8e0da1c7
         status: 500
         latency: 95083ns
```

Note: The session ID `82f03c1c-f27d-456f-9e10-6c9e8e0da1c7` was never created (client typo). Server should return 404.

**Impact:**
- Incorrect HTTP semantics
- Client cannot distinguish between "not found" and "server error"
- Poor API contract

**Recommendation:**
- Fix DELETE handler to return 404 for non-existent sessions
- Add test coverage for this case

---

## Informational Warnings (Expected)

| Warning | Count | Notes |
|---------|-------|-------|
| Insecure host key verification | 7 | Expected for spot instances |

---

## Performance Baseline

| Operation | Avg | P99 | Notes |
|-----------|-----|-----|-------|
| POST /sessions | 1.6s | 2.7s | Good |
| DELETE /sessions | 5.3s | 6.4s | Acceptable |
| GET /inventory (cold) | 5.2s | 6.6s | Needs improvement |
| GET /inventory (warm) | 200ms | 540ms | Good |
| GET /sessions/:id | 120ms | 190ms | Good |
| SSH Verification | 142s | 200s | Needs investigation |

---

## Session Log

| Session ID | GPU | Price | Created | SSH Result | Outcome |
|------------|-----|-------|---------|------------|---------|
| a0402eb5-78ac-4360-aa95-63999321eb01 | RTX 3090 | $0.062/hr | 13:55:02 | TIMEOUT (7 att) | Failed |
| 579a630f-6ee1-4483-b4de-09c085527f14 | RTX 5060 Ti | $0.056/hr | 13:56:53 | 90s (4 att) | User destroyed |
| 0b4e1e81-3e3a-4f2c-89c2-e4c935e53605 | RTX 5060 Ti | $0.054/hr | 13:59:46 | 139s (5 att) | User destroyed |
| cfcfaa2e-5c64-498a-b4d1-6568c6c157fc | RTX 5060 Ti | $0.062/hr | 14:00:51 | 138s (5 att) | User destroyed |
| 82f03c1c-f279-4790-a268-518657b59e87 | RTX 5060 Ti | $0.060/hr | 14:16:12 | 200s (6 att) | User destroyed |
| 07057ef5-0380-4dcd-b401-35f3e8903540 | RTX 5060 Ti | - | 14:52:46 | - | User destroyed |
| 59de870e-a03d-4dbe-94a1-0fecd5c5530e | RTX 5060 Ti | - | 14:53:32 | - | User destroyed |

---

## Priority Matrix

| Priority | Bug | Action |
|----------|-----|--------|
| P0 | BUG-5 | Fix 500→404 on DELETE non-existent session |
| P1 | BUG-1 | Investigate SSH timeout on RTX 3090 |
| P1 | BUG-3 | Set DEPLOYMENT_ID in production |
| P2 | BUG-2 | Implement background inventory refresh |
| P2 | BUG-4 | Add SSH verification metrics & investigate |

---

## Next Steps

1. [ ] Fix BUG-5: Return 404 for DELETE non-existent session
2. [ ] Add DEPLOYMENT_ID to deployment configuration
3. [ ] Investigate RTX 3090 SSH timeout root cause
4. [ ] Add Prometheus metrics for SSH verification times
5. [ ] Implement parallel provider fetching for inventory
