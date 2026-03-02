# Blue Lobster Benchmark Bug Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix 10 bugs (C2-01 through C2-10) discovered during Blue Lobster benchmark campaign #2, covering manifest state management, session cleanup, and provider-level issues.

**Architecture:** Fixes are grouped into 8 tasks ordered by dependency — provider-level fixes first (isolated, no cross-cutting concerns), then benchmark script fixes, then runner state management fixes that build on each other. Each task targets specific files with focused changes.

**Tech Stack:** Go 1.22, SQLite, bash (benchmark script), testify for assertions, httptest for provider client mocks.

**Bug-to-Task Mapping:**

| Bug | Description | Task |
|-----|-------------|------|
| C2-06, C2-07 | Destroy 404 + stuck stopping | Task 1 |
| C2-10 | DeploymentID empty | Task 2 |
| C2-01, C2-02 | NVML timeout + GPU name "unknown" | Task 3 |
| C2-03 (part 1) | Completed entries re-processed | Task 4 |
| C2-03 (part 2) | Silent MarkSuccess failures | Task 5 |
| C2-04, C2-05 | Orphaned sessions + retry collisions | Task 6 |
| C2-08 | Failed entry has null error | Task 7 |
| C2-09 | Run status counters fluctuate | Task 8 |

---

### Task 1: Handle 404 as Success in Blue Lobster DestroyInstance

**Fixes:** C2-06 (destroy 404), C2-07 (stuck stopping — downstream)

**Rationale:** When Blue Lobster returns 404 on destroy, the instance is already gone. Treating this as an error causes sessions to get stuck in "stopping" state for 27+ minutes. The provisioner's `destroyWithVerification` already handles `ErrInstanceNotFound` from `GetInstanceStatus` as success (line 1196), but `DestroyInstance` itself propagates the 404 error, triggering the "destroy call failed" warning path which delays verification.

**Files:**
- Modify: `internal/provider/bluelobster/client.go:524-546`
- Test: `internal/provider/bluelobster/client_test.go`

**Step 1: Write failing test**

Add to `client_test.go`:

```go
func TestDestroyInstance_404_TreatedAsSuccess(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/instances/") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "instance_not_found",
					"message": "Instance not found",
				},
			})
			return
		}
	})
	client := newTestClient(t, handler)
	err := client.DestroyInstance(context.Background(), "550e8400-e29b-41d4-a716-446655440000")
	assert.NoError(t, err, "404 on destroy should be treated as success (instance already gone)")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/bluelobster/ -run TestDestroyInstance_404_TreatedAsSuccess -v`
Expected: FAIL — currently returns error on 404

**Step 3: Implement fix**

In `client.go`, modify `DestroyInstance` (line 536):

```go
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) (err error) {
	startTime := time.Now()
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("DestroyInstance", startTime, err)
	}()

	if err := validateInstanceID(instanceID); err != nil {
		return fmt.Errorf("bluelobster: DestroyInstance: %w", err)
	}

	if err = c.doRequest(ctx, http.MethodDelete, "/instances/"+instanceID, nil, nil); err != nil {
		// 404 means instance is already gone — treat as success
		if provider.IsNotFoundError(err) {
			c.logger.Info("instance already gone (404 on destroy)",
				slog.String("provider", "bluelobster"),
				slog.String("instance_id", instanceID),
			)
			return nil
		}
		return fmt.Errorf("bluelobster: DestroyInstance: %w", err)
	}

	c.logger.Info("instance destroyed",
		slog.String("provider", "bluelobster"),
		slog.String("instance_id", instanceID),
	)

	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/bluelobster/ -run TestDestroyInstance_404_TreatedAsSuccess -v`
Expected: PASS

**Step 5: Run full Blue Lobster test suite**

Run: `go test ./internal/provider/bluelobster/ -v`
Expected: All tests pass

**Step 6: Commit**

```bash
git add internal/provider/bluelobster/client.go internal/provider/bluelobster/client_test.go
git commit -m "fix: treat 404 as success in Blue Lobster DestroyInstance

Fixes C2-06 and C2-07. When Blue Lobster returns 404 on destroy, the
instance is already gone. Previously this caused sessions to get stuck
in 'stopping' state for 27+ minutes while the provisioner retried."
```

---

### Task 2: Encode Deployment ID in Blue Lobster Instance Name

**Fixes:** C2-10 (deploymentID empty)

**Rationale:** Blue Lobster doesn't persist metadata (BL-007), so the deployment ID stored in tags is lost. The reconciler can't distinguish our instances from others. Since the instance name IS persisted, we can encode the deployment ID there. The name format is already `shopper-{session_id}` — we extend to include a short deployment prefix.

**Files:**
- Modify: `internal/provider/bluelobster/client.go` (CreateInstance and ListAllInstances)
- Test: `internal/provider/bluelobster/client_test.go`

**Step 1: Write failing test**

Add to `client_test.go`:

```go
func TestCreateInstance_IncludesDeploymentIDInName(t *testing.T) {
	var capturedName string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/instances/launch"):
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			capturedName = req["name"].(string)
			json.NewEncoder(w).Encode(map[string]interface{}{"task_id": "task-1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tasks/task-1"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":      "COMPLETED",
				"instance_id": "inst-abc",
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances/inst-abc"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "inst-abc", "status": "running",
				"network": map[string]interface{}{"ip_address": "1.2.3.4", "ssh_port": 22},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	client := newTestClient(t, handler)
	info, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "v1_gpu_1x_a4000:igl",
		SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@test",
		Tags: models.InstanceTags{
			ShopperSessionID:    "sess-123",
			ShopperDeploymentID: "deploy-abcdef12",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Contains(t, capturedName, "deploy-abcdef1") // first 15 chars of deployment ID
}

func TestListAllInstances_ParsesDeploymentIDFromName(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances") {
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id": "inst-1", "status": "running",
					"name": "shopper-sess123-deploy-abcdef12",
					"network": map[string]interface{}{"ip_address": "1.2.3.4", "ssh_port": 22},
				},
			})
			return
		}
	})
	client := newTestClient(t, handler)
	instances, err := client.ListAllInstances(context.Background())
	require.NoError(t, err)
	require.Len(t, instances, 1)
	assert.Equal(t, "deploy-abcdef12", instances[0].Tags.ShopperDeploymentID)
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/bluelobster/ -run "TestCreateInstance_IncludesDeploymentID|TestListAllInstances_ParsesDeploymentID" -v`
Expected: FAIL

**Step 3: Implement fix**

In `client.go`, modify the instance name construction in `CreateInstance` (around line 415) to include deployment ID:

```go
// Build name: shopper-{session_id}-{deployment_id_prefix}
name := "shopper-" + sanitizeInstanceName(req.Tags.ShopperSessionID)
if req.Tags.ShopperDeploymentID != "" {
	depID := req.Tags.ShopperDeploymentID
	if len(depID) > 15 {
		depID = depID[:15]
	}
	name += "-deploy-" + sanitizeInstanceName(depID)
}
```

In `ListAllInstances`, modify the name parsing to extract deployment ID (around line 350):

```go
// Parse deployment ID from name: "shopper-{session_id}-deploy-{deployment_id}"
if idx := strings.Index(vm.Name, "-deploy-"); idx >= 0 {
	tags.ShopperDeploymentID = vm.Name[idx+len("-deploy-"):]
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/bluelobster/ -run "TestCreateInstance_IncludesDeploymentID|TestListAllInstances_ParsesDeploymentID" -v`
Expected: PASS

**Step 5: Run full test suite**

Run: `go test ./internal/provider/bluelobster/ -v`
Expected: All tests pass

**Step 6: Commit**

```bash
git add internal/provider/bluelobster/client.go internal/provider/bluelobster/client_test.go
git commit -m "fix: encode deployment ID in Blue Lobster instance name

Fixes C2-10. Blue Lobster doesn't persist metadata (BL-007), so the
deployment ID was empty on ListAllInstances. Now encoded in the instance
name as 'shopper-{session}-deploy-{id}' and parsed back out."
```

---

### Task 3: Fix Benchmark Script nvidia-smi Timeout and GPU Name

**Fixes:** C2-01 (NVML 15-25min timeout), C2-02 (GPU name "unknown")

**Rationale:** The nvidia-smi stability loop waits 600s (10min) but RTX 8000 instances need 15-25min for NVML to stabilize. Also, if the stability check TIMES OUT (loop exits without `break`), the GPU name read on line 167 will fail and return "unknown". Fix: increase timeout to 1800s (30min) and add a post-loop stability verification.

**Files:**
- Modify: `scripts/gpu-benchmark.sh:155-172`
- Modify: `internal/service/benchmark/runner.go:707` (waitForSystemReady timeout)

**Step 1: Fix the benchmark script**

In `scripts/gpu-benchmark.sh`, replace lines 155-172:

```bash
if command -v nvidia-smi >/dev/null 2>&1; then
  # Wait for nvidia-smi to return stable values (DKMS rebuild may be in progress)
  # RTX 8000 instances can take 15-25 minutes for NVML drivers to stabilize
  NVIDIA_WAIT=0
  NVIDIA_TIMEOUT=1800
  NVIDIA_STABLE=false
  while [ $NVIDIA_WAIT -lt $NVIDIA_TIMEOUT ]; do
    TEST_MEM=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs) || true
    if echo "$TEST_MEM" | grep -qE '^[0-9]+$'; then
      NVIDIA_STABLE=true
      break
    fi
    log "nvidia-smi not stable yet (got: ${TEST_MEM:-empty}), waiting 10s..."
    sleep 10
    NVIDIA_WAIT=$((NVIDIA_WAIT + 10))
  done

  if [ "$NVIDIA_STABLE" = true ]; then
    GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs) || GPU_NAME="unknown"
  else
    log "WARNING: nvidia-smi did not stabilize after ${NVIDIA_TIMEOUT}s, GPU name will be unknown"
    GPU_NAME="unknown"
  fi
  GPU_MEMORY_MIB=$(ensure_numeric "$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs)")
  GPU_COUNT=$(ensure_numeric "$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | wc -l | xargs)")
  DRIVER_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs) || DRIVER_VERSION="unknown"
  CUDA_VERSION=$(nvidia-smi 2>/dev/null | awk '/CUDA Version:/{for(i=1;i<=NF;i++)if($i=="Version:")print $(i+1)}' || echo "unknown")
fi
```

**Step 2: Increase waitForSystemReady timeout**

In `runner.go`, line 707, change `20*time.Minute` to `30*time.Minute`:

```go
readyCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
```

Also update the error message on line 714:
```go
return fmt.Errorf("system readiness timeout after 30 minutes")
```

**Step 3: Run tests**

Run: `go test ./internal/service/benchmark/ -v`
Expected: All tests pass (no runner tests exist yet, so this confirms no compilation errors)

Run: `bash -n scripts/gpu-benchmark.sh` (syntax check)
Expected: No errors

**Step 4: Commit**

```bash
git add scripts/gpu-benchmark.sh internal/service/benchmark/runner.go
git commit -m "fix: increase nvidia-smi timeout to 30min, fix GPU name detection

Fixes C2-01 and C2-02. RTX 8000 instances need 15-25 minutes for NVML
to stabilize. Increased script wait from 10min to 30min. Added stability
flag so GPU name is only read when nvidia-smi confirmed working, preventing
'unknown' GPU names. Also increased Go-side readiness timeout to 30min."
```

---

### Task 4: Add Optimistic Locking to MarkRunning

**Fixes:** C2-03 (completed entries re-processed — primary defense)

**Rationale:** `MarkRunning` currently updates unconditionally (`WHERE id = ?`), allowing it to overwrite a "success" or "failed" entry back to "running". Adding `WHERE status = 'pending'` ensures only genuinely pending entries can be claimed. If the row was already claimed or completed, the UPDATE affects 0 rows and we detect that.

**Files:**
- Modify: `internal/benchmark/manifest.go:285-294`
- Test: `internal/benchmark/manifest_test.go` (new file)

**Step 1: Write failing test**

Create `internal/benchmark/manifest_test.go`:

```go
package benchmark

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestManifest(t *testing.T) *ManifestStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	store, err := NewManifestStore(db)
	require.NoError(t, err)
	return store
}

func TestMarkRunning_OnlyClaimsPendingEntries(t *testing.T) {
	store := setupTestManifest(t)
	ctx := context.Background()

	entry := &ManifestEntry{
		RunID:    "run-test",
		GPUType:  "RTX A4000",
		Provider: "bluelobster",
		Model:    "llama3.1:8b",
	}
	require.NoError(t, store.Create(ctx, entry))

	// First claim should succeed
	err := store.MarkRunning(ctx, entry.ID, "worker-1", "")
	require.NoError(t, err)

	// Second claim on already-running entry should fail
	err = store.MarkRunning(ctx, entry.ID, "worker-2", "")
	assert.Error(t, err, "should not be able to claim an already-running entry")

	// Mark success
	require.NoError(t, store.MarkSuccess(ctx, entry.ID, "bench-1", 100.0, 0.05))

	// Claim on completed entry should fail
	err = store.MarkRunning(ctx, entry.ID, "worker-3", "")
	assert.Error(t, err, "should not be able to claim a completed entry")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/benchmark/ -run TestMarkRunning_OnlyClaimsPendingEntries -v`
Expected: FAIL — second MarkRunning succeeds when it shouldn't

**Step 3: Implement fix**

In `manifest.go`, modify `MarkRunning` (lines 285-294):

```go
// ErrEntryNotPending is returned when MarkRunning is called on an entry that is not pending.
var ErrEntryNotPending = errors.New("entry is not in pending state")

// MarkRunning atomically claims a pending entry for a worker.
// Returns ErrEntryNotPending if the entry has already been claimed or completed.
func (s *ManifestStore) MarkRunning(ctx context.Context, id, workerID, outputFile string) error {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE benchmark_manifest SET
			status = 'running', worker_id = ?, output_file = ?, started_at = ?
		WHERE id = ? AND status = 'pending'
	`, workerID, outputFile, now, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: entry %s", ErrEntryNotPending, id)
	}
	return nil
}
```

Add `"errors"` to the imports in `manifest.go` if not already present.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/benchmark/ -run TestMarkRunning_OnlyClaimsPendingEntries -v`
Expected: PASS

**Step 5: Run full test suite**

Run: `go test ./internal/benchmark/ -v && go test ./internal/service/benchmark/ -v`
Expected: All pass

**Step 6: Commit**

```bash
git add internal/benchmark/manifest.go internal/benchmark/manifest_test.go
git commit -m "fix: add optimistic locking to MarkRunning

Fixes C2-03 (part 1). MarkRunning now uses WHERE status = 'pending'
to prevent claiming entries that are already running, completed, or
failed. Returns ErrEntryNotPending if the entry was already claimed."
```

---

### Task 5: Handle MarkSuccess/MarkFailed Errors in Runner

**Fixes:** C2-03 (completed entries re-processed — secondary defense)

**Rationale:** The runner discards errors from `MarkSuccess` and `MarkFailed` with `_ =`. If a DB write fails, the entry status doesn't update, leaving it in a stale state. Combined with Task 4's optimistic locking, this ensures entries reliably reach terminal states.

**Files:**
- Modify: `internal/service/benchmark/runner.go` (multiple locations)

**Step 1: Fix MarkSuccess error handling**

In `runner.go`, line 642, replace:
```go
_ = r.manifest.MarkSuccess(ctx, entry.ID, result.ID, result.Results.AvgTokensPerSecond, totalCost)
```
with:
```go
if err := r.manifest.MarkSuccess(ctx, entry.ID, result.ID, result.Results.AvgTokensPerSecond, totalCost); err != nil {
	r.logger.Error("CRITICAL: failed to mark entry as success",
		slog.String("entry_id", entry.ID),
		slog.String("error", err.Error()))
}
```

**Step 2: Fix MarkFailed error handling**

Search for all `_ = r.manifest.MarkFailed(` in runner.go and replace each with:
```go
if err := r.manifest.MarkFailed(ctx, entry.ID, reason, "stage"); err != nil {
	r.logger.Error("failed to mark entry as failed",
		slog.String("entry_id", entry.ID),
		slog.String("error", err.Error()))
}
```

Do the same for all `_ = r.manifest.MarkTimeout(` calls.

And `_ = r.manifest.Update(` on line 449.

**Step 3: Run tests**

Run: `go test ./internal/service/benchmark/ -v`
Expected: All pass (compilation check)

Run: `go test ./... 2>&1 | tail -20`
Expected: All pass

**Step 4: Commit**

```bash
git add internal/service/benchmark/runner.go
git commit -m "fix: handle manifest status update errors instead of discarding

Fixes C2-03 (part 2). All MarkSuccess, MarkFailed, MarkTimeout, and
Update calls now log errors instead of discarding them with '_ ='.
Prevents silent state corruption when DB writes fail."
```

---

### Task 6: Fix Session Cleanup on Retry and Duplicate Handling

**Fixes:** C2-04 (orphaned sessions), C2-05 (retry collisions)

**Rationale:** When `processEntryOnce` fails and the retry loop starts attempt 2, it clears `entry.SessionID` without destroying the session. If `cleanupSession` in the failed attempt also failed (timeout, 404), the session is orphaned. Additionally, if the same entry is somehow re-processed (C2-03), the `DuplicateSessionError` blocks provisioning.

**Files:**
- Modify: `internal/service/benchmark/runner.go:370-392` (processEntry retry loop)
- Modify: `internal/service/benchmark/runner.go:439-446` (DuplicateSessionError handling)

**Step 1: Fix retry cleanup in processEntry**

In `runner.go`, modify `processEntry` (lines 370-392):

```go
func (r *Runner) processEntry(ctx context.Context, run *BenchmarkRun, entry *benchmarkpkg.ManifestEntry) {
	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			r.logger.Info("retrying benchmark entry",
				slog.String("entry_id", entry.ID),
				slog.Int("attempt", attempt))
			// Destroy previous session before retrying to prevent orphans
			if entry.SessionID != "" {
				r.logger.Info("cleaning up previous attempt session",
					slog.String("session_id", entry.SessionID),
					slog.String("entry_id", entry.ID))
				r.cleanupSession(ctx, entry.SessionID)
			}
			// Reset entry state for retry
			entry.SessionID = ""
			entry.OfferID = ""
			time.Sleep(10 * time.Second)
		}
		if r.processEntryOnce(ctx, run, entry, attempt) {
			return // success
		}
		// Check if context was cancelled before retrying
		if ctx.Err() != nil {
			return
		}
	}
}
```

**Step 2: Handle DuplicateSessionError in processEntryOnce**

In `runner.go`, after the `CreateSession` call (around lines 439-446), add handling for `DuplicateSessionError`:

```go
session, err := r.provisioner.CreateSession(ctx, createReq, offer)
if err != nil {
	// If duplicate session error, destroy the existing session and retry
	var dupErr *provisioner.DuplicateSessionError
	if errors.As(err, &dupErr) {
		r.logger.Warn("destroying duplicate session before retry",
			slog.String("existing_session", dupErr.SessionID),
			slog.String("entry_id", entry.ID))
		r.cleanupSession(ctx, dupErr.SessionID)
		// Return false to trigger retry with fresh session
	} else {
		r.logger.Error("failed to provision session for benchmark",
			slog.String("error", err.Error()),
			slog.String("entry_id", entry.ID))
	}
	if mfErr := r.manifest.MarkFailed(ctx, entry.ID, err.Error(), "provision"); mfErr != nil {
		r.logger.Error("failed to mark entry as failed", slog.String("error", mfErr.Error()))
	}
	return false
}
```

Add `"errors"` and the provisioner import to runner.go imports if not present:
```go
"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
```

**Step 3: Run tests**

Run: `go test ./internal/service/benchmark/ -v`
Expected: All pass

**Step 4: Commit**

```bash
git add internal/service/benchmark/runner.go
git commit -m "fix: destroy sessions before retry, handle DuplicateSessionError

Fixes C2-04 and C2-05. processEntry now explicitly destroys the
previous attempt's session before retrying, preventing orphaned GPU
instances. DuplicateSessionError from the provisioner now triggers
cleanup of the conflicting session before returning false for retry."
```

---

### Task 7: Store Final Failure Reason on Retry Exhaustion

**Fixes:** C2-08 (failed entry has null error_message)

**Rationale:** When both retry attempts fail, `processEntry` returns without setting a final failure reason. If the last `processEntryOnce` call's `MarkFailed` succeeded, there IS a reason. But if the entry was stuck in "running" state (MarkFailed also failed), there's no reason stored. We add a final catch-all.

**Files:**
- Modify: `internal/service/benchmark/runner.go:370-392` (processEntry)

**Step 1: Implement fix**

In `runner.go`, modify `processEntry` to add a final failure marker after the retry loop:

```go
func (r *Runner) processEntry(ctx context.Context, run *BenchmarkRun, entry *benchmarkpkg.ManifestEntry) {
	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			r.logger.Info("retrying benchmark entry",
				slog.String("entry_id", entry.ID),
				slog.Int("attempt", attempt))
			if entry.SessionID != "" {
				r.logger.Info("cleaning up previous attempt session",
					slog.String("session_id", entry.SessionID),
					slog.String("entry_id", entry.ID))
				r.cleanupSession(ctx, entry.SessionID)
			}
			entry.SessionID = ""
			entry.OfferID = ""
			time.Sleep(10 * time.Second)
		}
		if r.processEntryOnce(ctx, run, entry, attempt) {
			return // success
		}
		if ctx.Err() != nil {
			// Context cancelled — mark with reason if not already terminal
			if mfErr := r.manifest.MarkFailed(ctx, entry.ID, "run cancelled", "cancelled"); mfErr != nil {
				r.logger.Error("failed to mark cancelled entry", slog.String("error", mfErr.Error()))
			}
			return
		}
	}
	// All retries exhausted — ensure entry has a failure reason
	r.logger.Warn("all retry attempts exhausted",
		slog.String("entry_id", entry.ID),
		slog.Int("attempts", maxAttempts))
	if mfErr := r.manifest.MarkFailed(context.Background(), entry.ID,
		fmt.Sprintf("all %d attempts failed", maxAttempts), "retry_exhausted"); mfErr != nil {
		r.logger.Error("failed to mark exhausted entry", slog.String("error", mfErr.Error()))
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/service/benchmark/ -v`
Expected: All pass

**Step 3: Commit**

```bash
git add internal/service/benchmark/runner.go
git commit -m "fix: store failure reason when all retry attempts exhausted

Fixes C2-08. After both retry attempts fail, processEntry now
explicitly marks the entry as failed with 'all N attempts failed'
reason and 'retry_exhausted' stage. Also marks entries with 'run
cancelled' if context was cancelled during retries."
```

---

### Task 8: Make Run Status Updates Monotonic

**Fixes:** C2-09 (run status counters fluctuate)

**Rationale:** `updateRunStatus` recalculates counts from the manifest on every call. If entries temporarily flip states (e.g., during re-processing), the summary fluctuates. With Task 4's optimistic locking, this should be less of an issue, but as defense-in-depth we make `completed` monotonically non-decreasing by tracking high-water marks.

**Files:**
- Modify: `internal/service/benchmark/runner.go:345-368` (updateRunStatus)
- Modify: `internal/service/benchmark/runner.go:46-61` (BenchmarkRun struct)

**Step 1: Implement fix**

In `runner.go`, modify `updateRunStatus`:

```go
func (r *Runner) updateRunStatus(run *BenchmarkRun) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if run.Status == RunStatusCancelled {
		return
	}

	summary, _ := r.manifest.GetSummary(context.Background(), run.ID)
	totalCost, _ := r.manifest.GetTotalCost(context.Background(), run.ID)

	run.Pending = summary[benchmarkpkg.ManifestStatusPending]
	run.Running = summary[benchmarkpkg.ManifestStatusRunning]

	// Completed and failed counts should never decrease (monotonic)
	newCompleted := summary[benchmarkpkg.ManifestStatusSuccess]
	newFailed := summary[benchmarkpkg.ManifestStatusFailed] + summary[benchmarkpkg.ManifestStatusTimeout]
	if newCompleted > run.Completed {
		run.Completed = newCompleted
	}
	if newFailed > run.Failed {
		run.Failed = newFailed
	}
	if totalCost > run.TotalCost {
		run.TotalCost = totalCost
	}

	if run.Pending == 0 && run.Running == 0 {
		if run.Failed > 0 && run.Completed == 0 {
			run.Status = RunStatusFailed
		} else {
			run.Status = RunStatusCompleted
		}
	}
	run.UpdatedAt = time.Now()
}
```

**Step 2: Run tests**

Run: `go test ./internal/service/benchmark/ -v`
Expected: All pass

Run: `go test ./... 2>&1 | tail -5`
Expected: All pass

**Step 3: Commit**

```bash
git add internal/service/benchmark/runner.go
git commit -m "fix: make run status counters monotonically non-decreasing

Fixes C2-09. Completed, failed, and total_cost counters now only
increase, never decrease. Prevents confusing fluctuations in the
monitoring API when entries temporarily transition between states."
```

---

## Post-Implementation Validation

After all 8 tasks are done:

1. **Full test suite**: `go test ./... -v 2>&1 | tail -20`
2. **Format check**: `gofmt -l .` (should return nothing)
3. **Build check**: `go build ./cmd/...`
4. **Manual validation**: Run a small benchmark run with 2 Blue Lobster entries to verify:
   - No orphaned sessions after completion
   - RTX 8000 GPU name not "unknown" (if available)
   - Run status counters don't fluctuate
   - Failed entries have error messages
