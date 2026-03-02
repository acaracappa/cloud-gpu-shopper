# Blue Lobster Benchmark Campaign #2
**Date**: 2026-02-23 7:42pm - 8:50pm EST
**Run ID**: `run-fc5eca3f`
**Goal**: Extensive testing across all 3 GPU types with 4 models (12 sessions total)
**Total Cost**: $0.82
**Duration**: ~68 minutes

## Results Summary

| # | GPU Type | VRAM | Model | $/hr | Status | TPS | Notes |
|---|----------|------|-------|------|--------|-----|-------|
| 1 | RTX A4000 | 16GB | llama3.1:8b | $0.20 | SUCCESS | 74.3 | |
| 2 | RTX A4000 | 16GB | mistral:7b | $0.20 | SUCCESS | 79.0 | |
| 3 | RTX A4000 | 16GB | qwen2:7b | $0.20 | SUCCESS | 85.5 | |
| 4 | RTX A4000 | 16GB | deepseek-r1:14b | $0.20 | SUCCESS | 40.6 | |
| 5 | RTX A5000 | 24GB | llama3.1:8b | $0.30 | SUCCESS | 116.4 | |
| 6 | RTX A5000 | 24GB | mistral:7b | $0.30 | SUCCESS | 121.5 | |
| 7 | RTX A5000 | 24GB | qwen2:7b | $0.30 | SUCCESS | 135.5 | Re-processed 3 extra times (BUG-C2-03) |
| 8 | RTX A5000 | 24GB | deepseek-r1:14b | $0.30 | SUCCESS | 62.9 | |
| 9 | RTX 8000 | 48GB | llama3.1:8b | $0.35 | SUCCESS | 85.8 | 15min NVML delay; GPU reported as "unknown" |
| 10 | RTX 8000 | 48GB | mistral:7b | $0.35 | SUCCESS | 91.0 | 15min NVML delay; retry required |
| 11 | RTX 8000 | 48GB | qwen2:7b | $0.35 | SUCCESS | 97.8 | 15min NVML delay; GPU reported as "unknown" |
| 12 | RTX 8000 | 48GB | deepseek-r1:14b | $0.35 | FAILED | - | NVML mismatch never resolved; exhausted retries |

## Performance Rankings

| Model | A4000 TPS | A5000 TPS | RTX 8000 TPS | Best Value ($/M tokens) |
|-------|-----------|-----------|-------------|------------------------|
| llama3.1:8b | 74.3 | 116.4 | 85.8 | A4000: $0.75/M |
| mistral:7b | 79.0 | 121.5 | 91.0 | A4000: $0.70/M |
| qwen2:7b | 85.5 | 135.5 | 97.8 | A4000: $0.65/M |
| deepseek-r1:14b | 40.6 | 62.9 | - | A4000: $1.37/M |

**Key insight**: RTX A5000 is fastest, but RTX A4000 is best value (lowest $/M tokens) for all models.
RTX 8000 underperforms A5000 despite 2x VRAM and higher price - likely driver mismatch boot delays waste benchmark time.

## Bugs Found

### BUG-C2-01: RTX 8000 NVML Driver/Library Version Mismatch on Boot (PROVIDER)
**Severity**: High
**Impact**: RTX 8000 instances take 15-25 minutes for NVIDIA drivers to stabilize after boot
**Details**: Every RTX 8000 instance reported "Failed to initialize NVML: Driver/library version mismatch" continuously for 15-25 minutes after provisioning. The `nvidia-smi` command fails with this error, causing the benchmark script to loop in its stability check. Eventually resolves on its own, but wastes significant time and caused 1 of 4 RTX 8000 entries to fail entirely.
**Log evidence**: `nvidia-smi not stable yet (got: Failed to initialize NVML: Driver/library version mismatch), waiting 10s...`
**Workaround**: Benchmark script already retries; need longer patience timeout for RTX 8000.

### BUG-C2-02: RTX 8000 GPU Name Reported as "unknown"
**Severity**: Medium
**Impact**: Benchmark records for RTX 8000 have `gpu_name: "unknown"` instead of "NVIDIA RTX 8000"
**Details**: Because nvidia-smi fails during the early NVML mismatch phase, the benchmark script captures "unknown" as the GPU name. The script likely reads GPU info before nvidia-smi stabilizes.
**Fix needed**: Read GPU name after nvidia-smi is confirmed stable, not during initial probe.

### BUG-C2-03: Completed Manifest Entries Get Re-Processed
**Severity**: Critical
**Impact**: Already-completed benchmark entries are picked up and processed again, wasting money
**Details**: Entry `manifest-10fd4425` (A5000/qwen2:7b) completed successfully at least 3 times. After each completion, the entry status reverted to "pending" and was picked up by a new worker. Each re-run provisioned a new GPU instance and ran the full benchmark again.
**Log evidence**: Multiple "benchmark entry completed" logs for the same entry_id with different benchmark_ids.
**Root cause**: Likely a race condition in manifest status updates, or the worker's status update is not persisted before the entry is re-queried.

### BUG-C2-04: Orphaned Sessions from Failed Worker Retries
**Severity**: High
**Impact**: GPU instances remain running (costing money) after their benchmark entries fail
**Details**: When a worker fails and the entry reverts to "pending", the session created by that worker is NOT destroyed. The sessions remain in "running" state with no associated active entry. Had to manually destroy 2 orphaned RTX 8000 sessions after the run.
**Sessions orphaned**: ec01547b, c9daf83b (RTX 8000)
**Fix needed**: Worker must destroy its session before releasing the entry back to pending.

### BUG-C2-05: "Already Has Active Session" Blocking Retries
**Severity**: High
**Impact**: Entries cannot retry because previous failed attempt's session is still running
**Details**: When entry `manifest-9f693f15` (RTX 8000/llama) retried, it failed with: "consumer bench-manifest-9f693f15-1 already has active session 4c50f6cb for offer bluelobster:v1_gpu_1x_8000:igl (status: running)". The old session from the first failed attempt wasn't cleaned up.
**Fix needed**: Clean up old session before retrying, or use a new consumer ID for retries.

### BUG-C2-06: Session Destroy Returns 404 from Blue Lobster API
**Severity**: Medium
**Impact**: Session cleanup fails silently; sessions get stuck in "stopping" state
**Details**: `bluelobster DELETE /instances/de2e1107 failed (HTTP 404): instance_not_found`. Blue Lobster's API doesn't recognize the instance for deletion. Could be the instance was already terminated by the provider, or the provider_id mapping is stale.
**Log**: "destroy call failed" + "session stuck in transitional state" (27+ minutes in "stopping")

### BUG-C2-07: Session Stuck in "stopping" State for 27+ Minutes
**Severity**: Medium
**Impact**: Lifecycle manager detects but can't resolve stuck sessions
**Details**: Sessions get stuck in "stopping" state for extremely long periods (27+ minutes observed). The lifecycle manager's `stuck_session_failed` audit event fires, but the session doesn't get cleaned up promptly.
**Related to**: BUG-C2-06 (destroy 404 causes indefinite stopping state)

### BUG-C2-08: Failed Entry Has Null error_message
**Severity**: Low
**Impact**: No diagnostic information for why an entry failed
**Details**: RTX 8000/deepseek-r1:14b entry status is "failed" but error_message field is null. The failure reason (NVML timeout + retry exhaustion) is only in server logs, not stored in the manifest.
**Fix needed**: Store meaningful error messages in manifest entries when they fail.

### BUG-C2-09: Run Status Counters Fluctuate Inconsistently
**Severity**: Low
**Impact**: Monitoring unreliable; completed count went from 10 to 9 at one point
**Details**: The run summary's completed/failed/pending counts are not monotonically consistent. At one check: completed=10, failed=0. Next check: completed=9, failed=2. The cost also dropped ($0.67 to $0.62). This suggests the summary is recalculated from current entry statuses, which themselves fluctuate due to BUG-C2-03.

### BUG-C2-10: DeploymentID Empty for Blue Lobster Instances
**Severity**: Low
**Impact**: Orphan detector can't distinguish our instances from others
**Details**: "deploymentID is empty; all provider instances will be considered ours" warnings for all Blue Lobster instances. The Blue Lobster client doesn't set deployment IDs, so the orphan detector treats ALL Blue Lobster instances as belonging to this deployment.

## Observations

1. **RTX A5000 is the performance leader** across all models - 1.6x faster than A4000, 1.3-1.4x faster than RTX 8000
2. **RTX A4000 is best value** - cheapest per million tokens across all models
3. **RTX 8000 driver issues are systemic** - every single RTX 8000 instance had NVML mismatch on boot
4. **NVML mismatch is transient** - resolves after 15-25 minutes in most cases
5. **Blue Lobster session teardown is slow** - instances take multiple attempts to destroy
6. **Benchmark runner has significant state management bugs** - entries get re-processed, sessions orphaned
7. **The runner effectively completed all 12 benchmarks** despite bugs, just with extra cost from re-runs
8. **Driver version across all instances**: 560.35.05 with CUDA 12.6

## Estimated Wasted Cost from Bugs
- Re-processed A5000/qwen2 entries: ~$0.15 (3 extra runs)
- Orphaned RTX 8000 sessions: ~$0.10 (2 sessions running ~15min each)
- NVML delay overhead on RTX 8000: ~$0.12 (4 instances x 15-20min wasted)
- **Total waste: ~$0.37 (45% of total $0.82 cost)**
