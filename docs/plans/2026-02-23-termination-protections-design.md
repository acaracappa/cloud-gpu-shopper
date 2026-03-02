# Termination Protections — Defense-in-Depth Design

**Date:** 2026-02-23
**Status:** Approved
**Trigger:** TensorDock deployment ran until account balance went negative due to failed instance cleanup.

## Problem Statement

A TensorDock GPU instance was not destroyed when expected, accumulating charges until the account balance went negative. Investigation revealed 5 gaps in the termination chain that affect all providers (TensorDock, Vast.ai, Blue Lobster).

## Root Cause Analysis

| Gap | Description | Severity |
|-----|-------------|----------|
| G1: Stuck-then-failed sessions leak instances | `destroyWithVerification` fails after 10 retries → session enters `stopping` → lifecycle marks it `failed` → provider instance never retried for destruction | Critical |
| G2: TensorDock tags are name-only | Only instance name used for identification (`shopper-{sessionID}`). No `ShopperExpiresAt` or `ShopperDeploymentID` transmitted. Unknown-named instances invisible to cleanup. | High |
| G3: No re-destroy for failed sessions | Once marked `failed`, no periodic mechanism re-attempts destruction. Failed session with live provider instance sits forever. | Critical |
| G4: No pre-provision balance check | No provider billing API integration. Instances provisioned even when account is nearly empty. | Medium |
| G5: Shutdown timeout abandons instances | If graceful shutdown exceeds 60s timeout, remaining destroys are abandoned. Instances keep running. | Medium |

### Key Code Paths

- `GetExpiredSessions()` only queries `StatusRunning` — ignoring `StatusStopping` and `StatusFailed`
- `checkStuckSessions()` marks stuck-in-stopping sessions as `failed` but does NOT re-attempt destroy
- `GetActiveSessionsByProvider()` excludes `StatusFailed` — so reconciler treats the live instance as an orphan (good), but only if `ListAllInstances()` returns it (depends on naming)
- TensorDock `instancesToProviderInstances()` silently drops instances without `shopper-` prefix

## Design

### 1. Failed-Destroy Watchdog (fixes G1, G3)

New method `checkFailedDestroys(ctx)` in lifecycle manager, called in the existing `run()` loop alongside other periodic checks.

**Behavior:**
- Query sessions with `status=failed AND provider_id != ''`
- For each, attempt `prov.DestroyInstance(ctx, session.ProviderID)`
- On success: update session to `stopped` status, log audit event
- On failure: log at ERROR level, leave session as `failed` for next tick
- Runs every lifecycle tick (1 minute)

**Storage:** Add `GetFailedSessionsWithInstances(ctx) ([]*Session, error)` — returns `status=failed, provider_id != ''`.

### 2. Shutdown Fire-and-Forget (fixes G5)

After shutdown timeout fires, iterate any remaining unfinished sessions and call `prov.DestroyInstance()` once each without verification. This is a last-chance kill.

**Changes:**
- After `wg.Wait()` timeout in `GracefulShutdown`: fire one `DestroyInstance` call per remaining session
- Log each at ERROR with provider name and instance ID

### 3. Increase Shutdown Timeout (fixes G5)

Change `DefaultShutdownTimeout` from 60s to 120s. A single stubborn destroy with 10 retries at exponential backoff takes ~55s. Multiple parallel destroys need more headroom.

### 4. BalanceProvider Interface — Warn Only (fixes G4)

New optional interface:

```go
type BalanceProvider interface {
    GetAccountBalance(ctx context.Context) (*AccountBalance, error)
}

type AccountBalance struct {
    Balance  float64
    Currency string
}
```

**Provisioner behavior:** Before creating an instance, check if provider implements `BalanceProvider`. If so, check balance. If below configurable threshold (default $5.00), log a WARNING but allow provisioning to proceed.

**Provider implementations:**
- Vast.ai: Implement using `/users/current/` endpoint (returns balance)
- TensorDock: Return `ErrNotSupported` (billing API under development)
- Blue Lobster: Return `ErrNotSupported`

### 5. Log Unknown TensorDock Instances (fixes G2)

In `instancesToProviderInstances()`, when an instance name doesn't match `shopper-*` prefix, log at WARN level with instance name and ID. Provides visibility into potentially leaked instances.

### 6. Reduce Reconcile Interval (fixes G1, G2, G3)

Change `DefaultReconcileInterval` from 5 minutes to 2 minutes. Faster orphan detection reduces the window for uncontrolled charges.

### 7. Startup Sweep for Failed Sessions (fixes G1, G3)

Extend `RecoverStuckSessions` to also check failed sessions with non-empty `ProviderID`. On startup, attempt to destroy any lingering provider instances from previously failed sessions.

## Files Changed

| File | Changes |
|------|---------|
| `internal/service/lifecycle/manager.go` | Add `checkFailedDestroys()` method, wire into `run()` loop |
| `internal/service/lifecycle/startup.go` | Fire-and-forget destroys after timeout, increase default timeout |
| `internal/service/lifecycle/reconciler.go` | Reduce interval to 2min, extend stuck session recovery |
| `internal/storage/sessions.go` | Add `GetFailedSessionsWithInstances()` query |
| `internal/provider/interface.go` | Add `BalanceProvider` interface and `AccountBalance` type |
| `internal/provider/vastai/client.go` | Implement `GetAccountBalance()` |
| `internal/provider/tensordock/client.go` | Log unknown instances in `instancesToProviderInstances()` |
| `internal/service/provisioner/service.go` | Add pre-provision balance warning |

## Testing Strategy

- Unit tests for `checkFailedDestroys()` with mock store and provider
- Unit tests for fire-and-forget shutdown path
- Unit test for `GetFailedSessionsWithInstances()` query
- Unit test for `BalanceProvider` check in provisioner (both supported and unsupported)
- Integration test: create session, force-fail destroy, verify watchdog recovers it
