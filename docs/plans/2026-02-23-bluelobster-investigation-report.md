# Blue Lobster Integration — Investigation Report

**Date**: 2026-02-23
**Branch**: `feat/provider-bluelobster`
**Status**: Comprehensive investigation by 4 parallel agents

---

## Executive Summary

Four independent investigation agents analyzed different aspects of the Blue Lobster integration. The core finding is that **Blue Lobster instances have a fundamentally different post-boot lifecycle** than Vast.ai or TensorDock: they run `apt-get dist-upgrade` on boot, which rebuilds NVIDIA kernel modules (DKMS), holds dpkg locks for 7-19 minutes, and makes the GPU unavailable during the rebuild.

Our code currently treats "SSH accessible" as "system ready", but on Blue Lobster, SSH becomes available minutes before the system is actually usable. This single gap is the root cause of most failures.

---

## Critical Findings (Ordered by Priority)

### P0: Task Poll Timeout Returns Success Instead of Error

**File**: `internal/provider/bluelobster/client.go:422-429`

When the 3-minute task poll times out, `CreateInstance` returns partial `InstanceInfo` with **nil error**. The provisioner treats this as success and enters the SSH verification loop with an empty host. This directly causes stuck sessions.

**Fix**: Return an error on task poll timeout instead of nil.

### P1: No "System Ready" Phase Between SSH and Workload

**File**: `internal/service/benchmark/runner.go:480-502`

After SSH verification succeeds, the benchmark runner immediately uploads the script via SCP. But Blue Lobster is still running `dist-upgrade`:
- sshd may restart mid-SCP → "ssh: unexpected packet in response to channel open"
- dpkg lock held → curl/jq installation fails
- NVIDIA DKMS rebuilding → nvidia-smi returns garbage → "unknown" GPU name

**Fix**: Add a system readiness probe (dpkg lock + nvidia-smi stability) before script upload.

### P1: BL-008 Workaround Defeats Destroy Verification

**File**: `internal/provider/bluelobster/client.go:534-540`

The IP-presence heuristic (`if powerStatus == "" && vm.IPAddress != "" → "running"`) correctly handles null power_status on live instances, but also triggers on recently-destroyed instances where the IP lingers. This causes destroy verification to retry up to 10 times unnecessarily.

**Fix**: Don't infer "running" if a destroy was just issued, or check for terminated/deleting status first.

### P2: SCP Retry Window Too Short

**File**: `internal/service/benchmark/runner.go:495-512`

Current: 3 attempts with 10/20/30s backoff = ~60s total.
Needed: Blue Lobster dist-upgrade can take 2-5 minutes of SSH instability.

**Fix**: For Blue Lobster, use 5 attempts with 30/60/90/120s backoff.

### P2: All Timeouts Too Short for Blue Lobster

| Timeout | Current | Needed | File |
|---------|---------|--------|------|
| apt lock retry | 10 min (60×10s) | 20 min (120×10s) | `scripts/gpu-benchmark.sh` |
| nvidia-smi wait | 2 min (120s) | 10 min (600s) | `scripts/gpu-benchmark.sh` |
| Result poll | 30 min | 60 min for BL | `runner.go:536` |
| Task poll | 3 min | 5 min | `client.go:30` |

### P2: No Blue Lobster Boot Delay (Like TensorDock Has)

**File**: `internal/service/provisioner/service.go:798-809`

TensorDock gets a 90-second cloud-init delay before SSH polling. Blue Lobster gets nothing. This wastes SSH poll attempts during the first minute when connection refused is guaranteed, and doesn't account for the much longer post-boot stabilization period.

**Fix**: Add a 60-second boot delay before SSH polling for Blue Lobster.

### P3: `SupportsFeature(FeatureInstanceTags)` Returns True Incorrectly

**File**: `internal/provider/bluelobster/client.go:263-264`

Returns `true` but metadata is NOT persisted (BL-007). The reconciler trusts metadata tags for deployment ID matching, which always fails. This means orphan detection is broken for Blue Lobster instances.

**Fix**: Return `false`.

### P3: `ListAllInstances` Missing BL-008 Workaround

**File**: `internal/provider/bluelobster/client.go:355`

`GetInstanceStatus` and `getInstanceInfo` both have the IP-based power_status inference, but `ListAllInstances` uses `vm.PowerStatus` directly. Listed instances can have empty status strings.

### P3: `set -euo pipefail` Can Kill Script on Transient Failures

**File**: `scripts/gpu-benchmark.sh:10`

With `set -e`, any transient `nvidia-smi` failure during DKMS rebuild kills the entire script. The `|| true` guards are inconsistent across lines.

**Fix**: Remove `set -e`, use explicit error checks instead.

---

## Provider-Side Issues (Not Our Code)

| Bug | Description | Impact | Workaround |
|-----|-------------|--------|------------|
| BL-007 | Metadata not persisted | Breaks deployment ID tracking | Name-based fallback (already done) |
| BL-008 | power_status always null | Misleading instance status | IP-based inference (already done) |
| BL-010 | template_name ignored | No Docker pre-installed | Script auto-installs Ollama natively |
| N/A | dist-upgrade on boot | 7-19min GPU unavailability | Need system readiness probe |
| N/A | Driver version split: A-series=CUDA 12.6, RTX 8000/5090=CUDA 13.0 | Triggers DKMS rebuild | nvidia-smi stability wait |

---

## Questions for Blue Lobster Support

1. Is `template_name` intended to work? Is there a `user_data` or `cloud_init` parameter for post-boot setup?
2. Is metadata persistence a planned feature? Any alternative API for instance metadata?
3. Is `power_status` null a known issue? What are the valid values?
4. Why do different GPU types have different driver versions? Is driver version pinning possible?
5. Is there a "system ready" indicator in the API (beyond task COMPLETED)?
6. After DELETE returns success, how long before the instance is fully gone?
7. Can the boot-time `dist-upgrade` be disabled or deferred?

---

## Recommended Implementation Plan

### Phase 1: Critical Fixes (Immediate)

1. **Fix task poll timeout** — return error instead of nil (`client.go:425`)
2. **Fix `SupportsFeature`** — return false for FeatureInstanceTags (`client.go:263`)
3. **Fix `ListAllInstances`** — add BL-008 power_status inference (`client.go:355`)
4. **Increase task poll timeout** — 3min → 5min (`client.go:30`)

### Phase 2: Robustness (Before Next Benchmark Run)

5. **Add Blue Lobster boot delay** — 60s before SSH polling (`service.go:809`)
6. **Add SSH stability verification** — require 2 consecutive SSH successes for BL (`service.go:957`)
7. **Add system readiness probe** — dpkg lock + nvidia-smi check before script upload (`runner.go:480`)
8. **Increase SCP retries** — 5 attempts with longer backoff for BL (`runner.go:495`)
9. **Increase all script timeouts** — apt: 20min, nvidia-smi: 10min (`gpu-benchmark.sh`)
10. **Increase result poll timeout** — 60min for BL (`runner.go:536`)

### Phase 3: Script Improvements

11. **Remove `set -e`** — use explicit error checks (`gpu-benchmark.sh:10`)
12. **Install all apt packages in one call** — single dpkg lock acquisition
13. **Move hardware collection after Ollama ready** — nvidia-smi guaranteed stable
14. **Add crash detection** — runner checks if script process is alive during polling
15. **Add failure marker** — `trap` writes `/tmp/benchmark_failed` on crash

---

## Worst-Case Timeline (Blue Lobster RTX 8000/5090)

| Phase | Duration | Cumulative |
|-------|----------|------------|
| Task poll + instance launch | 3 min | 3 min |
| SSH becomes available | 2 min | 5 min |
| dist-upgrade + DKMS rebuild | 19 min | 24 min |
| curl/jq install (after lock) | 1 min | 25 min |
| Ollama install | 2 min | 27 min |
| nvidia-smi stabilization | 2 min | 29 min |
| Model pull (qwen2:0.5b) | 1 min | 30 min |
| Warmup + quality + throughput | 7 min | 37 min |

**Total worst-case: ~37 minutes** (current 30-minute result poll timeout is insufficient)
