# Benchmark Bug Fixes — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix 6 bugs causing 65% benchmark failure rate — target >75% success rate.

**Architecture:** Surgical fixes at 3 files: Vast.ai error classification, benchmark runner offer exclusion, and provisioner cleanup/retry. Each fix is independent.

**Tech Stack:** Go, httptest for mocks, testify assertions

**Design doc:** `docs/plans/2026-02-24-benchmark-bug-fixes-design.md`

---

### Task 1: BENCH-001 — Classify `no_such_ask` as stale inventory

**Files:**
- Modify: `internal/provider/vastai/client.go:1042-1059`
- Test: `internal/provider/vastai/client_test.go`

**Step 1: Write the failing test**

Add to `client_test.go`:

```go
func TestClient_HandleError_NoSuchAsk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success":false,"error":"invalid_args","msg":"error 404/3603: no_such_ask  Instance type by id 29907796 is not available.","ask_id":29907796}`))
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.Error(t, err)
	assert.True(t, provider.IsStaleInventoryError(err), "no_such_ask should be classified as stale inventory")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/vastai/ -run TestClient_HandleError_NoSuchAsk -v`
Expected: FAIL — `no_such_ask` currently maps to `ErrProviderError`, not `ErrOfferStaleInventory`

**Step 3: Implement the fix**

In `handleError()` at line 1042, after reading the body (line 1043-1044), add `no_such_ask` detection before the switch:

```go
func (c *Client) handleError(resp *http.Response, operation string) error {
	body, _ := io.ReadAll(resp.Body)
	message := string(body)

	var baseErr error

	// Check for specific error patterns in response body before status code switch
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(message, "no_such_ask") {
		baseErr = provider.ErrOfferStaleInventory
	} else {
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			baseErr = provider.ErrProviderRateLimit
		case http.StatusUnauthorized, http.StatusForbidden:
			baseErr = provider.ErrProviderAuth
		case http.StatusNotFound:
			baseErr = provider.ErrInstanceNotFound
		default:
			baseErr = provider.ErrProviderError
		}
	}

	return provider.NewProviderError("vastai", operation, resp.StatusCode, message, baseErr)
}
```

Ensure `"strings"` is in the import block (it likely already is).

**Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/vastai/ -run TestClient_HandleError_NoSuchAsk -v`
Expected: PASS

**Step 5: Run all existing tests**

Run: `go test ./internal/provider/vastai/ -v`
Expected: All pass (existing `TestClient_HandleError_RateLimit` etc. unaffected)

**Step 6: Commit**

```bash
git add internal/provider/vastai/client.go internal/provider/vastai/client_test.go
git commit -m "fix(vastai): classify no_such_ask as stale inventory error (BENCH-001)"
```

---

### Task 2: BENCH-004 — Retry on HTTP 429 with backoff

**Files:**
- Modify: `internal/provider/vastai/client.go`
- Test: `internal/provider/vastai/client_test.go`

**Step 1: Write the failing test**

Add to `client_test.go`:

```go
func TestClient_Retry429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"message": "rate limited"}`))
			return
		}
		// Third attempt succeeds
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"offers": []interface{}{},
		})
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))

	_, err := client.ListOffers(context.Background(), models.OfferFilter{})

	require.NoError(t, err, "should succeed after 429 retries")
	assert.Equal(t, 3, attempts, "should have retried twice before succeeding")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/vastai/ -run TestClient_Retry429 -v`
Expected: FAIL — currently 429 returns `ErrProviderRateLimit` immediately, no retry

**Step 3: Implement the fix**

Add a `doWithRetry` helper method to the Vast.ai client. This wraps `httpClient.Do` and retries on 429 with exponential backoff:

```go
// doWithRetry executes an HTTP request with automatic retry on 429 (rate limit).
// Retries up to 3 times with exponential backoff: 1s, 2s, 4s.
func (c *Client) doWithRetry(req *http.Request, bodyBytes []byte) (*http.Response, error) {
	const maxRetries = 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1)) * time.Second
			c.logger.Warn("rate limited by Vast.ai, retrying",
				slog.Int("attempt", attempt),
				slog.Duration("delay", delay))
			select {
			case <-time.After(delay):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			// Reset body for retry
			if bodyBytes != nil {
				req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		resp.Body.Close()
	}
	// All retries exhausted, make one final attempt and return whatever happens
	if bodyBytes != nil {
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}
	return c.httpClient.Do(req)
}
```

Ensure `"bytes"` is in the import block.

Then replace `c.httpClient.Do(req)` calls in key methods with `c.doWithRetry(req, bodyBytes)`:

- **ListOffers** (line 356): `resp, err := c.doWithRetry(req, nil)` (GET, no body)
- **CreateInstance** (line 509): `resp, err := c.doWithRetry(httpReq, body)` (PUT, has body — `body` is the `[]byte` from `json.Marshal` at line 495)
- **AttachSSHKey** (line 747): `resp, err := c.doWithRetry(httpReq, bodyJSON)` (POST, has body)
- **DestroyInstance** (line 789): `resp, err := c.doWithRetry(req, nil)` (DELETE, no body)

For GET/DELETE requests pass `nil` for bodyBytes. For PUT/POST pass the marshaled JSON bytes.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/vastai/ -run TestClient_Retry429 -v`
Expected: PASS

**Step 5: Run all existing tests**

Run: `go test ./internal/provider/vastai/ -v`
Expected: All pass

**Step 6: Commit**

```bash
git add internal/provider/vastai/client.go internal/provider/vastai/client_test.go
git commit -m "fix(vastai): retry HTTP 429 with exponential backoff (BENCH-004)"
```

---

### Task 3: BENCH-002 — Exclude failed offers on runner retry

**Files:**
- Modify: `internal/service/benchmark/runner.go:393-463`

**Step 1: Modify `processEntry` to track failed offer IDs**

At line 393, add a `failedOfferIDs` slice that persists across attempts. Pass it to `processEntryOnce`:

```go
func (r *Runner) processEntry(ctx context.Context, run *BenchmarkRun, entry *benchmarkpkg.ManifestEntry) {
	const maxAttempts = 2
	var failedOfferIDs []string
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
		if r.processEntryOnce(ctx, run, entry, attempt, failedOfferIDs) {
			return
		}
		// Track the failed offer for exclusion on next attempt
		if entry.OfferID != "" {
			failedOfferIDs = append(failedOfferIDs, entry.OfferID)
		}
		if ctx.Err() != nil {
			if mfErr := r.manifest.MarkFailed(context.Background(), entry.ID, "run cancelled", "cancelled"); mfErr != nil {
				r.logger.Error("failed to mark cancelled entry", slog.String("error", mfErr.Error()))
			}
			return
		}
	}
	r.logger.Warn("all retry attempts exhausted",
		slog.String("entry_id", entry.ID),
		slog.Int("attempts", maxAttempts))
	if mfErr := r.manifest.MarkFailed(context.Background(), entry.ID,
		fmt.Sprintf("all %d attempts failed", maxAttempts), "retry_exhausted"); mfErr != nil {
		r.logger.Error("failed to mark exhausted entry", slog.String("error", mfErr.Error()))
	}
}
```

**Step 2: Update `processEntryOnce` signature and filter offers**

Change signature at line 434 to accept `failedOfferIDs`:

```go
func (r *Runner) processEntryOnce(ctx context.Context, run *BenchmarkRun, entry *benchmarkpkg.ManifestEntry, attempt int, failedOfferIDs []string) bool {
```

After `ListOffers` at line 442-445, filter out failed offers before selecting:

```go
	offers, err := r.inventory.ListOffers(ctx, models.OfferFilter{
		Provider: entry.Provider,
		GPUType:  entry.GPUType,
	})
	if err != nil || len(offers) == 0 {
		// ... existing error handling unchanged
	}

	// Exclude offers that failed in previous attempts
	if len(failedOfferIDs) > 0 {
		filtered := offers[:0]
		excludeSet := make(map[string]bool, len(failedOfferIDs))
		for _, id := range failedOfferIDs {
			excludeSet[id] = true
		}
		for _, o := range offers {
			if !excludeSet[o.ID] {
				filtered = append(filtered, o)
			}
		}
		offers = filtered
		if len(offers) == 0 {
			reason := fmt.Sprintf("no offers remaining after excluding %d failed offers for %s on %s", len(failedOfferIDs), entry.GPUType, entry.Provider)
			r.logger.Warn(reason)
			if err := r.manifest.MarkFailed(ctx, entry.ID, reason, "find_offer"); err != nil {
				r.logger.Error("failed to mark entry as failed", slog.String("error", err.Error()))
			}
			return false
		}
	}

	offer := &offers[0]
```

**Step 3: Build and verify**

Run: `go build ./...`
Expected: Compiles successfully

Run: `go test ./internal/service/benchmark/ -v`
Expected: All pass (no existing runner tests, but package should compile)

**Step 4: Commit**

```bash
git add internal/service/benchmark/runner.go
git commit -m "fix(benchmark): exclude failed offers on runner retry (BENCH-002)"
```

---

### Task 4: BENCH-007 — Fail fast on zero offers

**Files:**
- Modify: `internal/service/benchmark/runner.go:393-431` (processEntry) and `:448-455` (processEntryOnce)

**Step 1: Add a "no retry" signal from processEntryOnce**

Change `processEntryOnce` to return a second value indicating whether retry is possible. When zero offers are found (before and after exclusion filtering), signal "no retry":

Change signature:
```go
func (r *Runner) processEntryOnce(ctx context.Context, run *BenchmarkRun, entry *benchmarkpkg.ManifestEntry, attempt int, failedOfferIDs []string) (success bool, shouldRetry bool) {
```

At the "no offers found" error path (around line 448), return `false, false`:
```go
	if err != nil || len(offers) == 0 {
		reason := fmt.Sprintf("no offers available for %s on %s", entry.GPUType, entry.Provider)
		if err != nil {
			reason = err.Error()
		}
		r.logger.Warn(reason, slog.String("gpu_type", entry.GPUType), slog.String("provider", entry.Provider))
		if err := r.manifest.MarkFailed(ctx, entry.ID, reason, "find_offer"); err != nil {
			r.logger.Error("failed to mark entry as failed", slog.String("error", err.Error()))
		}
		return false, false // no retry — no offers exist
	}
```

All other `return false` paths become `return false, true` (retry is possible).
All `return true` paths become `return true, true`.

**Step 2: Update processEntry to respect the no-retry signal**

```go
		success, shouldRetry := r.processEntryOnce(ctx, run, entry, attempt, failedOfferIDs)
		if success {
			return
		}
		if !shouldRetry {
			r.logger.Info("skipping retry — no offers available",
				slog.String("entry_id", entry.ID))
			return
		}
```

**Step 3: Build and verify**

Run: `go build ./...`
Expected: Compiles successfully

**Step 4: Commit**

```bash
git add internal/service/benchmark/runner.go
git commit -m "fix(benchmark): fail fast with clear message on zero offers (BENCH-007)"
```

---

### Task 5: BENCH-003 — Make SSH `auth_failed` trigger auto-retry

**Files:**
- Modify: `internal/service/provisioner/service.go:1052-1069`

**Step 1: Understand the current flow**

The SSH verification loop at line 1052 detects 3 consecutive `auth_failed` errors, then:
1. Destroys the instance (line 1060-1064)
2. Calls `failSession()` (line 1066)
3. Returns

After return, `waitForSSHVerifyAsyncWithRetry` (line 747) checks if the session is failed + auto_retry enabled and calls `triggerAsyncRetry`. This retry mechanism already works — but it may pick the same host because the current offer ID isn't always added to `FailedOffers` in the SSH failure path.

**Step 2: Ensure failed offer is tracked before retry**

After the `failSession` call at line 1066, add the current offer to `FailedOffers` so `triggerAsyncRetry` excludes it:

```go
						s.failSession(ctx, session, "permanent SSH error: "+lastErrorType)

						// Ensure current offer is in FailedOffers so retry picks a different host
						if session.OfferID != "" {
							if session.FailedOffers == "" {
								session.FailedOffers = session.OfferID
							} else if !strings.Contains(session.FailedOffers, session.OfferID) {
								session.FailedOffers = session.FailedOffers + "," + session.OfferID
							}
							_ = s.store.Update(ctx, session)
						}

						metrics.RecordSSHVerifyFailure()
```

**Step 3: Build and verify**

Run: `go build ./...`
Expected: Compiles successfully

Run: `go test ./internal/service/provisioner/ -v`
Expected: All pass

**Step 4: Commit**

```bash
git add internal/service/provisioner/service.go
git commit -m "fix(provisioner): track failed offer on SSH auth_failed for retry exclusion (BENCH-003)"
```

---

### Task 6: BENCH-009 — Destroy instance in `failSession()`

**Files:**
- Modify: `internal/service/provisioner/service.go:1387-1410`

**Step 1: Understand the concern**

`failSession()` only updates DB status. Some callers (like SSH `auth_failed` at line 1060) destroy the instance before calling `failSession()`, but other callers (SSH timeout, other error paths) don't. This leaves orphaned provider instances.

The fix: add a defensive `DestroyInstance()` call in `failSession()` itself. Callers that already destroyed will hit a 404 which is handled gracefully.

**Step 2: Add provider lookup and destroy**

`failSession` currently doesn't have access to the provider. We need to look it up. The provisioner `Service` has `s.providers` map. Add destroy logic:

```go
func (s *Service) failSession(ctx context.Context, session *models.Session, reason string) {
	// Destroy provider instance if one exists (defensive — prevents orphans)
	if session.ProviderID != "" {
		if prov, ok := s.providers[session.Provider]; ok {
			if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
				// 404 is expected if caller already destroyed — only log other errors
				if !errors.Is(err, provider.ErrInstanceNotFound) {
					s.logger.Warn("failed to destroy instance during session failure",
						slog.String("session_id", session.ID),
						slog.String("provider_id", session.ProviderID),
						slog.String("error", err.Error()))
				}
			} else {
				s.logger.Info("AUDIT",
					slog.Bool("audit", true),
					slog.String("operation", "instance_destroyed_on_fail"),
					slog.String("session_id", session.ID),
					slog.String("provider_id", session.ProviderID),
					slog.String("provider", session.Provider))
			}
		}
	}

	oldStatus := session.Status
	session.Status = models.StatusFailed
	session.Error = reason
	session.StoppedAt = s.now()
	if err := s.store.Update(ctx, session); err != nil {
		s.logger.Error("failed to update session to failed",
			slog.String("session_id", session.ID),
			slog.String("error", err.Error()))
	}

	metrics.UpdateSessionStatus(session.Provider, string(oldStatus), string(models.StatusFailed))

	if s.costRecorder != nil {
		if err := s.costRecorder.RecordFinalCost(ctx, session); err != nil {
			s.logger.Error("failed to record final cost for failed session",
				slog.String("session_id", session.ID),
				slog.String("error", err.Error()))
		}
	}
}
```

Ensure `"errors"` and `provider "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"` are in the import block. Check if the `provider` package is already imported — if it creates a cycle, use `errors.Is` with `ErrInstanceNotFound` defined locally or check the error string instead.

**Step 3: Verify no import cycles**

Run: `go build ./...`
Expected: Compiles. If import cycle detected, replace the `errors.Is` check with a string check: `!strings.Contains(err.Error(), "not found")`

**Step 4: Run all tests**

Run: `go test ./internal/service/provisioner/ -v`
Expected: All pass

**Step 5: Commit**

```bash
git add internal/service/provisioner/service.go
git commit -m "fix(provisioner): destroy provider instance in failSession to prevent orphans (BENCH-009)"
```

---

### Task 7: Final validation

**Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: All pass

**Step 2: Run gofmt**

Run: `gofmt -l ./internal/provider/vastai/client.go ./internal/service/benchmark/runner.go ./internal/service/provisioner/service.go`
Expected: No output (already formatted)

**Step 3: Build**

Run: `go build ./cmd/...`
Expected: Clean build

**Step 4: Commit any formatting fixes**

If gofmt found issues, fix and commit.

---

### Task 8: Validation campaign

**Step 1: Restart server with log capture**

```bash
kill $(lsof -ti :8080) 2>/dev/null; sleep 2
go run cmd/server/main.go > /private/tmp/gpu-server-campaign7.log 2>&1 &
sleep 5 && curl -s http://localhost:8080/health
```

**Step 2: Launch campaign 7 (12 BL + 12 Vast.ai)**

```bash
curl -s -X POST http://localhost:8080/api/v1/benchmark-runs \
  -H "Content-Type: application/json" \
  -d '{"models":["qwen2:7b","llama3.1:8b","deepseek-r1:8b"],"gpu_types":["RTX A4000","RTX A5000","RTX 8000","RTX 5090"],"providers":["bluelobster"],"max_budget":5}'

curl -s -X POST http://localhost:8080/api/v1/benchmark-runs \
  -H "Content-Type: application/json" \
  -d '{"models":["qwen2:7b","llama3.1:8b","deepseek-r1:8b"],"gpu_types":["RTX 5060 Ti","RTX 5070 Ti","RTX 5080","RTX 4090"],"providers":["vastai"],"max_budget":5}'
```

**Step 3: Monitor and verify target >75% success**

Check: `curl -s http://localhost:8080/api/v1/benchmark-runs/{run_id} | jq '{completed, failed, running}'`

Target: >18/24 entries succeed (>75%), up from 17/48 (35%).
