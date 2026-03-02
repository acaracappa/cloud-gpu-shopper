# Benchmark Bug Fixes — Design Doc

**Date**: 2026-02-24
**Approach**: Surgical per-bug fixes (Approach A)
**Bugs**: BENCH-001 through BENCH-009 (6 code fixes, 3 provider-side accepted)
**Bug Log**: `docs/benchmark-campaign-5-6-buglog.md`

## Problem

Benchmark campaigns 5 & 6 achieved only 35% success rate (17/48 entries). Root cause analysis identified 6 bugs in our code responsible for ~85% of failures, plus 3 provider-side constraints we accept.

## Scope

Fix the 6 code bugs. Accept provider constraints (BENCH-005 capacity, BENCH-006 poll timeout, BENCH-008 disk resize) as-is.

## Fix 1 — BENCH-001: Classify `no_such_ask` as stale inventory

**File**: `internal/provider/vastai/client.go`
**Impact**: 13+ failed sessions across campaigns 5 & 6

Vast.ai returns HTTP 400 with `no_such_ask` when an offer is gone. Currently mapped to `ErrProviderError` (generic). The provisioner's auto_retry never fires because `ShouldRetryWithDifferentOffer()` returns false.

**Change**: In `handleError()`, parse HTTP 400 response bodies for `"no_such_ask"`. Return `ErrOfferStaleInventory` instead of `ErrProviderError`.

**Result**: Provisioner auto_retry fires → picks different offer → succeeds.

## Fix 2 — BENCH-002: Exclude failed offers on runner retry

**File**: `internal/service/benchmark/runner.go`
**Impact**: All retried entries that got the same stale offer from cache

The runner's `processEntry()` retries after 10 seconds, but the inventory cache TTL is 60 seconds. Retry gets the exact same stale offer.

**Change**: Track failed offer IDs across attempts in `processEntry()`. In `processEntryOnce()`, filter out failed offers from `ListOffers()` results before selecting one. The offer ID from each failed attempt is appended to an exclusion list passed between attempts.

**Result**: Retry always picks a different offer.

## Fix 3 — BENCH-003: Make SSH `auth_failed` retryable

**File**: `internal/service/provisioner/service.go`
**Impact**: 14 failed sessions across campaigns 5 & 6

SSH `auth_failed` is treated as permanent — the session fails and no auto_retry with a different offer fires. But `auth_failed` is likely host-specific (certain Vast.ai hosts reject keys).

**Change**: When `auth_failed` is detected and `AutoRetry` is enabled, trigger the same retry-with-different-offer path used for `ErrOfferUnavailable`. The current instance is destroyed, and a new session is created on a different offer. Keep the 3-consecutive-auth_failed threshold before destroying the current instance.

**Result**: Bad hosts are skipped, next offer likely succeeds.

## Fix 4 — BENCH-004: Retry on HTTP 429 with backoff

**File**: `internal/provider/vastai/client.go`
**Impact**: 2 failed sessions in campaign 6

Vast.ai returns HTTP 429 when rate limited. Currently not retried — mapped to `ErrProviderRateLimit` and the request fails.

**Change**: Add request-level retry for HTTP 429 responses in the Vast.ai HTTP client's request method. Exponential backoff: 1s → 2s → 4s, max 3 retries. Transparent to callers.

**Result**: Transient rate limits are absorbed by the client.

## Fix 5 — BENCH-007: Fail fast on zero offers

**File**: `internal/service/benchmark/runner.go`
**Impact**: 3 failed sessions in campaign 6 (BL A4000 gone from inventory)

When no offers exist for a GPU type, the runner burns both retry attempts checking empty inventory. The failure reason is generic "all 2 attempts failed".

**Change**: When `ListOffers()` returns zero results in `processEntryOnce()`, log `"no offers available for {gpu_type} on {provider}"`, set the entry's failure reason to this message, and return false with a flag indicating no retry should be attempted. The caller skips remaining attempts.

**Result**: Clear failure message, no wasted retries.

## Fix 6 — BENCH-009: Destroy instance in `failSession()`

**File**: `internal/service/provisioner/service.go`
**Impact**: 13 orphaned Vast.ai instances found on server restart

When `failSession()` is called (e.g., after SSH `auth_failed`), the session is marked failed in DB but the provider instance is never destroyed. Instances accumulate until the next startup sweep or reconciliation.

**Change**: In `failSession()`, if the session has a `ProviderID`, call `DestroyInstance()` before marking the session as failed. Log the destroy as an audit event. Handle 404 gracefully (instance already gone).

**Result**: No orphaned instances after session failures.

## Files Changed

| File | Fixes |
|------|-------|
| `internal/provider/vastai/client.go` | Fix 1 (no_such_ask), Fix 4 (429 retry) |
| `internal/service/benchmark/runner.go` | Fix 2 (offer exclusion), Fix 5 (fail fast) |
| `internal/service/provisioner/service.go` | Fix 3 (auth_failed retry), Fix 6 (destroy on fail) |

## Testing

Each fix is independently verifiable:
- Fix 1: Unit test `handleError()` with `no_such_ask` response body
- Fix 2: Unit test that retry excludes previous offer ID
- Fix 3: Integration test that `auth_failed` triggers auto_retry
- Fix 4: Unit test that HTTP 429 is retried with backoff
- Fix 5: Unit test that zero offers returns immediately with clear message
- Fix 6: Unit test that `failSession()` calls `DestroyInstance()` when ProviderID exists

## Validation

Run benchmark campaign 7 (12 Vast.ai + 12 Blue Lobster) after all fixes. Target: >75% success rate (up from 35%).

## Not In Scope

- BENCH-005: BL RTX 5090 single-capacity (provider limitation)
- BENCH-006: BL RTX 8000 poll timeout (provider limitation, already 60min in benchmark runner)
- BENCH-008: BL disk resize failure (transient provider infra issue)
- Error pipeline overhaul (Approach B) — deferred unless more classification bugs surface
