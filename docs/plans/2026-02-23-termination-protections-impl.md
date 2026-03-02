# Termination Protections Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Prevent GPU instances from running uncontrolled after failed destroy attempts, with defense-in-depth across all providers.

**Architecture:** Add a failed-destroy watchdog to the lifecycle manager's periodic check loop, harden shutdown to fire-and-forget as a last resort, extend startup sweep to recover failed sessions, add optional balance checking via a new `BalanceProvider` interface, and improve TensorDock instance visibility. All changes are application-level within existing service boundaries.

**Tech Stack:** Go, testify (assert/require), existing lifecycle/provisioner/provider packages.

**Design doc:** `docs/plans/2026-02-23-termination-protections-design.md`

---

### Task 1: Add `GetFailedSessionsWithInstances` storage query

**Files:**
- Modify: `internal/storage/sessions.go:276-284` (add new query after `GetExpiredSessions`)
- Test: `internal/storage/sessions_test.go`

**Step 1: Write the failing test**

Add to `internal/storage/sessions_test.go`:

```go
func TestSessionStore_GetFailedSessionsWithInstances(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create a failed session WITH a provider ID (leaked instance)
	leaked := &models.Session{
		ID:         "sess-leaked",
		Provider:   "tensordock",
		ProviderID: "td-12345",
		Status:     models.StatusFailed,
		Error:      "Session stuck in stopping state - manual cleanup may be required",
		CreatedAt:  time.Now().Add(-2 * time.Hour),
		ExpiresAt:  time.Now().Add(-1 * time.Hour),
		StoppedAt:  time.Now().Add(-30 * time.Minute),
	}
	require.NoError(t, store.Create(ctx, leaked))

	// Create a failed session WITHOUT provider ID (never got an instance)
	noInstance := &models.Session{
		ID:        "sess-no-instance",
		Provider:  "tensordock",
		Status:    models.StatusFailed,
		Error:     "Provisioning failed - no provider instance ID",
		CreatedAt: time.Now().Add(-1 * time.Hour),
		ExpiresAt: time.Now(),
		StoppedAt: time.Now().Add(-30 * time.Minute),
	}
	require.NoError(t, store.Create(ctx, noInstance))

	// Create a running session (should not be returned)
	running := &models.Session{
		ID:         "sess-running",
		Provider:   "vastai",
		ProviderID: "vast-99",
		Status:     models.StatusRunning,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}
	require.NoError(t, store.Create(ctx, running))

	// Query
	results, err := store.GetFailedSessionsWithInstances(ctx)
	require.NoError(t, err)

	// Should return only the leaked session
	assert.Len(t, results, 1)
	assert.Equal(t, "sess-leaked", results[0].ID)
	assert.Equal(t, "td-12345", results[0].ProviderID)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/ -run TestSessionStore_GetFailedSessionsWithInstances -v`
Expected: FAIL — `GetFailedSessionsWithInstances` not defined

**Step 3: Write minimal implementation**

Add to `internal/storage/sessions.go` after `GetExpiredSessions` (after line 284):

```go
// GetFailedSessionsWithInstances returns failed sessions that still have a provider instance ID.
// These are sessions where the destroy failed but the provider instance may still be running.
func (s *SessionStore) GetFailedSessionsWithInstances(ctx context.Context) ([]*models.Session, error) {
	return s.ListInternal(ctx, SessionFilter{
		Statuses:          []models.SessionStatus{models.StatusFailed},
		HasProviderID:     true,
	})
}
```

Also update the `SessionFilter` struct and `ListInternal` to support the `HasProviderID` filter. In the `SessionFilter` struct (around line 305), add:

```go
HasProviderID bool // If true, only return sessions with non-empty provider_id
```

In the `ListInternal` method where it builds the SQL query, add a condition:

```go
if f.HasProviderID {
	conditions = append(conditions, "provider_id != ''")
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/ -run TestSessionStore_GetFailedSessionsWithInstances -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/storage/sessions.go internal/storage/sessions_test.go
git commit -m "feat: add GetFailedSessionsWithInstances storage query

Returns failed sessions with non-empty provider IDs for the
failed-destroy watchdog to re-attempt instance destruction."
```

---

### Task 2: Add `checkFailedDestroys` to lifecycle manager

**Files:**
- Modify: `internal/service/lifecycle/manager.go:34-41` (add to `SessionStore` interface)
- Modify: `internal/service/lifecycle/manager.go:96-106` (add metric)
- Modify: `internal/service/lifecycle/manager.go:277-288` (add call in `runChecks`)
- Modify: `internal/service/lifecycle/manager.go:531-558` (add new method after `destroySession`)
- Test: `internal/service/lifecycle/manager_test.go`

**Step 1: Write the failing test**

Add to `internal/service/lifecycle/manager_test.go`:

```go
func TestManager_CheckFailedDestroys(t *testing.T) {
	store := newMockSessionStore()
	destroyer := &mockDestroyer{}

	// Add a failed session with a provider ID (leaked instance)
	store.add(&models.Session{
		ID:         "sess-leaked",
		Provider:   "tensordock",
		ProviderID: "td-12345",
		Status:     models.StatusFailed,
		Error:      "Session stuck in stopping state - manual cleanup may be required",
		CreatedAt:  time.Now().Add(-2 * time.Hour),
	})

	// Add a failed session WITHOUT provider ID (should be skipped)
	store.add(&models.Session{
		ID:       "sess-clean-fail",
		Provider: "vastai",
		Status:   models.StatusFailed,
		Error:    "Provisioning failed - no provider instance ID",
	})

	m := New(store, destroyer,
		WithCheckInterval(100*time.Millisecond),
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))
	defer m.Stop()

	// Wait for at least one check cycle
	time.Sleep(300 * time.Millisecond)

	// Verify destroy was called for the leaked session
	assert.Equal(t, 1, destroyer.destroyCount())
	assert.Contains(t, destroyer.destroyedIDs(), "sess-leaked")

	// Verify the leaked session was updated to stopped
	store.mu.RLock()
	leaked := store.sessions["sess-leaked"]
	store.mu.RUnlock()
	assert.Equal(t, models.StatusStopped, leaked.Status)
}

func TestManager_CheckFailedDestroys_DestroyFails(t *testing.T) {
	store := newMockSessionStore()
	destroyer := &mockDestroyer{
		err: errors.New("provider API error"),
	}

	store.add(&models.Session{
		ID:         "sess-stubborn",
		Provider:   "tensordock",
		ProviderID: "td-999",
		Status:     models.StatusFailed,
		Error:      "Session stuck in stopping state",
		CreatedAt:  time.Now().Add(-1 * time.Hour),
	})

	m := New(store, destroyer,
		WithCheckInterval(100*time.Millisecond),
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))
	defer m.Stop()

	time.Sleep(300 * time.Millisecond)

	// Destroy was attempted
	assert.GreaterOrEqual(t, destroyer.destroyCount(), 1)

	// Session should still be failed (not updated to stopped)
	store.mu.RLock()
	sess := store.sessions["sess-stubborn"]
	store.mu.RUnlock()
	assert.Equal(t, models.StatusFailed, sess.Status)
}
```

You'll also need to update the mock store to implement `GetFailedSessionsWithInstances`:

```go
func (m *mockSessionStore) GetFailedSessionsWithInstances(ctx context.Context) ([]*models.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*models.Session
	for _, s := range m.sessions {
		if s.Status == models.StatusFailed && s.ProviderID != "" {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}
```

And update the `mockDestroyer` to track destroyed session IDs:

```go
type mockDestroyer struct {
	mu          sync.Mutex
	err         error
	callCount   int
	destroyedBy []string
}

func (m *mockDestroyer) DestroySession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.destroyedBy = append(m.destroyedBy, sessionID)
	return m.err
}

func (m *mockDestroyer) destroyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *mockDestroyer) destroyedIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.destroyedBy
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/service/lifecycle/ -run TestManager_CheckFailedDestroys -v`
Expected: FAIL — `GetFailedSessionsWithInstances` not in interface / `checkFailedDestroys` not defined

**Step 3: Write implementation**

1. Add to the `SessionStore` interface (line ~38):
```go
GetFailedSessionsWithInstances(ctx context.Context) ([]*models.Session, error)
```

2. Add metric field to `Metrics` struct (after line 103):
```go
FailedDestroysRecovered int64
```

3. Add `checkFailedDestroys` call in `runChecks` (line ~288, after `checkStuckSessions`):
```go
m.checkFailedDestroys(ctx)
```

4. Add the new method after `destroySession` (after line ~558):
```go
// checkFailedDestroys re-attempts destruction for sessions that failed to destroy.
// These are sessions marked as "failed" that still have a provider instance ID,
// meaning the provider instance may still be running and accumulating charges.
func (m *Manager) checkFailedDestroys(ctx context.Context) {
	sessions, err := m.store.GetFailedSessionsWithInstances(ctx)
	if err != nil {
		m.logger.Error("failed to get failed sessions for re-destroy",
			slog.String("error", err.Error()))
		return
	}

	for _, session := range sessions {
		m.logger.Info("re-attempting destroy for failed session",
			slog.String("session_id", session.ID),
			slog.String("provider", session.Provider),
			slog.String("provider_id", session.ProviderID))

		if err := m.destroyer.DestroySession(ctx, session.ID); err != nil {
			m.logger.Error("re-destroy failed for leaked session",
				slog.String("session_id", session.ID),
				slog.String("provider_id", session.ProviderID),
				slog.String("error", err.Error()))
			continue
		}

		m.logger.Info("successfully destroyed leaked session",
			slog.String("session_id", session.ID),
			slog.String("provider_id", session.ProviderID))

		m.metrics.mu.Lock()
		m.metrics.FailedDestroysRecovered++
		m.metrics.mu.Unlock()

		logging.Audit(ctx, "failed_destroy_recovered",
			"session_id", session.ID,
			"provider", session.Provider,
			"provider_id", session.ProviderID)
	}
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/service/lifecycle/ -run TestManager_CheckFailedDestroys -v`
Expected: PASS (both subtests)

**Step 5: Run all lifecycle tests to avoid regressions**

Run: `go test ./internal/service/lifecycle/ -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/service/lifecycle/manager.go internal/service/lifecycle/manager_test.go
git commit -m "feat: add failed-destroy watchdog to lifecycle manager

Re-attempts destruction every tick for sessions marked 'failed' that
still have a provider instance ID. Prevents leaked GPU instances from
accumulating charges indefinitely."
```

---

### Task 3: Harden graceful shutdown with fire-and-forget fallback

**Files:**
- Modify: `internal/service/lifecycle/startup.go:15-23` (change default timeout)
- Modify: `internal/service/lifecycle/startup.go:163-283` (add fire-and-forget after timeout)
- Test: `internal/service/lifecycle/startup_test.go`

**Step 1: Write the failing test**

Add to `internal/service/lifecycle/startup_test.go`:

```go
func TestGracefulShutdown_FireAndForgetOnTimeout(t *testing.T) {
	// Provider that takes too long to destroy (simulating timeout)
	slowProvider := &mockStartupProvider{
		name:         "tensordock",
		destroyDelay: 5 * time.Second, // Longer than our shutdown timeout
	}

	fastProvider := &mockStartupProvider{
		name: "vastai",
	}

	store := &mockStartupStore{
		sessions: []*models.Session{
			{
				ID:         "sess-slow",
				Provider:   "tensordock",
				ProviderID: "td-slow-1",
				Status:     models.StatusRunning,
			},
			{
				ID:         "sess-fast",
				Provider:   "vastai",
				ProviderID: "vast-1",
				Status:     models.StatusRunning,
			},
		},
	}

	registry := &mockProviderRegistry{
		providers: map[string]provider.Provider{
			"tensordock": slowProvider,
			"vastai":     fastProvider,
		},
	}

	m := NewStartupShutdownManager(store, nil, registry,
		WithShutdownTimeout(500*time.Millisecond)) // Very short timeout

	ctx := context.Background()
	err := m.GracefulShutdown(ctx)

	// Should return error (some sessions timed out)
	assert.Error(t, err)

	// Fast provider should have been destroyed
	assert.GreaterOrEqual(t, fastProvider.getDestroyCalls(), 1)

	// Slow provider should also have received at least one call
	// (the fire-and-forget after timeout)
	time.Sleep(100 * time.Millisecond) // Give fire-and-forget goroutines time
	assert.GreaterOrEqual(t, slowProvider.getDestroyCalls(), 1)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/service/lifecycle/ -run TestGracefulShutdown_FireAndForgetOnTimeout -v`
Expected: FAIL — slow provider doesn't get the fire-and-forget call

**Step 3: Write implementation**

1. Change `DefaultShutdownTimeout` (line 20):
```go
DefaultShutdownTimeout = 120 * time.Second
```

2. Modify `GracefulShutdown` to track which sessions were not destroyed and fire-and-forget for them. Replace the select/case block at lines 245-250 with:

```go
	// Track which sessions were successfully destroyed
	destroyedSet := make(map[string]bool)
	var destroyedSetMu sync.Mutex

	for _, session := range sessions {
		wg.Add(1)
		go func(s *models.Session) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-shutdownCtx.Done():
				m.logger.Warn("shutdown context cancelled, skipping session destroy",
					slog.String("session_id", s.ID))
				failedCount.Add(1)
				return
			}

			if err := m.destroySession(shutdownCtx, s); err != nil {
				m.logger.Error("failed to destroy session during shutdown",
					slog.String("session_id", s.ID),
					slog.String("provider", s.Provider),
					slog.String("error", err.Error()))
				failedCount.Add(1)
			} else {
				m.logger.Info("destroyed session during shutdown",
					slog.String("session_id", s.ID),
					slog.String("provider", s.Provider))
				destroyedCount.Add(1)
				destroyedSetMu.Lock()
				destroyedSet[s.ID] = true
				destroyedSetMu.Unlock()
			}
		}(session)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All destroys completed
	case <-shutdownCtx.Done():
		m.logger.Warn("shutdown context timed out, firing last-chance destroys")
		// Fire-and-forget: attempt destroy for any session not yet confirmed destroyed
		destroyedSetMu.Lock()
		notDestroyed := make([]*models.Session, 0)
		for _, s := range sessions {
			if !destroyedSet[s.ID] {
				notDestroyed = append(notDestroyed, s)
			}
		}
		destroyedSetMu.Unlock()

		for _, s := range notDestroyed {
			m.logger.Error("LAST CHANCE: fire-and-forget destroy for session",
				slog.String("session_id", s.ID),
				slog.String("provider", s.Provider),
				slog.String("provider_id", s.ProviderID))
			go m.fireAndForgetDestroy(s)
		}
	}
```

3. Add the `fireAndForgetDestroy` helper:

```go
// fireAndForgetDestroy makes a single destroy attempt without verification.
// Used as a last resort during shutdown timeout.
func (m *StartupShutdownManager) fireAndForgetDestroy(session *models.Session) {
	if session.ProviderID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prov, err := m.providers.Get(session.Provider)
	if err != nil {
		m.logger.Error("fire-and-forget: provider not found",
			slog.String("session_id", session.ID),
			slog.String("provider", session.Provider))
		return
	}

	if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
		m.logger.Error("fire-and-forget destroy failed",
			slog.String("session_id", session.ID),
			slog.String("provider_id", session.ProviderID),
			slog.String("error", err.Error()))
	} else {
		m.logger.Info("fire-and-forget destroy succeeded",
			slog.String("session_id", session.ID),
			slog.String("provider_id", session.ProviderID))
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/service/lifecycle/ -run TestGracefulShutdown -v`
Expected: All PASS

**Step 5: Run all lifecycle tests**

Run: `go test ./internal/service/lifecycle/ -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/service/lifecycle/startup.go internal/service/lifecycle/startup_test.go
git commit -m "feat: add fire-and-forget shutdown fallback and increase timeout

When graceful shutdown times out, fire one last destroy call per
remaining session without waiting for verification. Increases default
shutdown timeout from 60s to 120s."
```

---

### Task 4: Extend startup sweep to recover failed sessions

**Files:**
- Modify: `internal/service/lifecycle/reconciler.go:404-482` (extend `RecoverStuckSessions`)
- Modify: `internal/service/lifecycle/reconciler.go:16` (change `DefaultReconcileInterval`)
- Test: `internal/service/lifecycle/reconciler_test.go`

**Step 1: Write the failing test**

Add to `internal/service/lifecycle/reconciler_test.go`:

```go
func TestRecoverStuckSessions_IncludesFailedWithProviderID(t *testing.T) {
	store := newMockReconcileStore()
	mockProv := &mockReconcileProvider{
		name: "tensordock",
		statusResponses: map[string]*provider.InstanceStatus{
			"td-leaked": {Running: true, Status: "running"},
		},
	}
	registry := &mockProviderRegistryForReconciler{
		providers: map[string]provider.Provider{"tensordock": mockProv},
	}

	r := NewReconciler(store, registry)

	// Add a failed session with a provider ID (leaked instance, still running)
	store.addSession(&models.Session{
		ID:         "sess-failed-leaked",
		Provider:   "tensordock",
		ProviderID: "td-leaked",
		Status:     models.StatusFailed,
		Error:      "Session stuck in stopping state",
		CreatedAt:  time.Now().Add(-2 * time.Hour),
	})

	err := r.RecoverStuckSessions(context.Background())
	require.NoError(t, err)

	// Should have attempted to destroy the instance
	assert.GreaterOrEqual(t, mockProv.destroyCalls(), 1)
}
```

Note: You may need to adapt mock names to match existing test patterns — check `reconciler_test.go` for the exact mock types used.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/service/lifecycle/ -run TestRecoverStuckSessions_IncludesFailedWithProviderID -v`
Expected: FAIL — `RecoverStuckSessions` doesn't query failed sessions

**Step 3: Write implementation**

1. Change `DefaultReconcileInterval` (line 16):
```go
DefaultReconcileInterval = 2 * time.Minute
```

2. Extend `RecoverStuckSessions` (around line 409-411) to also query failed sessions. After the existing stuck session recovery, add:

```go
	// Also recover failed sessions that still have provider instances
	failedSessions, err := r.store.GetSessionsByStatus(ctx, models.StatusFailed)
	if err != nil {
		r.logger.Error("failed to get failed sessions for recovery",
			slog.String("error", err.Error()))
		// Don't return error - continue with rest of recovery
	} else {
		for _, session := range failedSessions {
			if session.ProviderID == "" {
				continue // No instance to recover
			}

			r.logger.Warn("found failed session with provider instance",
				slog.String("session_id", session.ID),
				slog.String("provider_id", session.ProviderID),
				slog.String("provider", session.Provider))

			prov, err := r.providers.Get(session.Provider)
			if err != nil {
				r.logger.Error("provider not found for failed session",
					slog.String("session_id", session.ID),
					slog.String("provider", session.Provider))
				continue
			}

			// Check if instance is still running
			status, err := prov.GetInstanceStatus(ctx, session.ProviderID)
			if err != nil {
				// Instance not found - clean up the session record
				session.ProviderID = "" // Clear to prevent future re-checks
				r.store.Update(ctx, session)
				continue
			}

			if status.Running {
				r.logger.Info("destroying leaked instance from failed session",
					slog.String("session_id", session.ID),
					slog.String("provider_id", session.ProviderID))
				if err := prov.DestroyInstance(ctx, session.ProviderID); err != nil {
					r.logger.Error("failed to destroy leaked instance",
						slog.String("session_id", session.ID),
						slog.String("error", err.Error()))
				} else {
					session.ProviderID = "" // Clear provider ID after successful destroy
					session.Status = models.StatusStopped
					session.StoppedAt = r.now()
					r.store.Update(ctx, session)
				}
			} else {
				// Instance not running - update session
				session.ProviderID = ""
				r.store.Update(ctx, session)
			}
		}
	}
```

**Step 4: Run tests**

Run: `go test ./internal/service/lifecycle/ -run TestRecoverStuckSessions -v`
Expected: All PASS

**Step 5: Run all lifecycle tests**

Run: `go test ./internal/service/lifecycle/ -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/service/lifecycle/reconciler.go internal/service/lifecycle/reconciler_test.go
git commit -m "feat: extend startup sweep to recover failed sessions, reduce reconcile interval

RecoverStuckSessions now also checks failed sessions with provider IDs
and destroys any still-running instances. Reconcile interval reduced
from 5min to 2min for faster orphan detection."
```

---

### Task 5: Log unknown TensorDock instances

**Files:**
- Modify: `internal/provider/tensordock/client.go:902-921` (add warning log)
- Test: `internal/provider/tensordock/client_test.go`

**Step 1: Write the failing test**

Add to `internal/provider/tensordock/client_test.go`:

```go
func TestClient_instancesToProviderInstances_LogsUnknown(t *testing.T) {
	// Create a client with a logger that captures output
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := &Client{logger: logger}

	instances := []Instance{
		{ID: "td-1", Name: "shopper-sess-1", Status: "running"},
		{ID: "td-2", Name: "unknown-instance", Status: "running"},
		{ID: "td-3", Name: "my-other-vm", Status: "stopped"},
	}

	result := client.instancesToProviderInstances(instances)

	// Should only return the shopper instance
	assert.Len(t, result, 1)
	assert.Equal(t, "td-1", result[0].ID)

	// Should have logged warnings about the unknown instances
	logOutput := buf.String()
	assert.Contains(t, logOutput, "unknown-instance")
	assert.Contains(t, logOutput, "td-2")
	assert.Contains(t, logOutput, "my-other-vm")
	assert.Contains(t, logOutput, "td-3")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/tensordock/ -run TestClient_instancesToProviderInstances_LogsUnknown -v`
Expected: FAIL — no log output for unknown instances

**Step 3: Write implementation**

Modify `instancesToProviderInstances` in `internal/provider/tensordock/client.go` (lines 902-921):

```go
func (c *Client) instancesToProviderInstances(instances []Instance) []provider.ProviderInstance {
	result := make([]provider.ProviderInstance, 0)
	for _, inst := range instances {
		if sessionID, ok := models.ParseLabel(inst.Name); ok {
			result = append(result, provider.ProviderInstance{
				ID:        inst.ID,
				Name:      inst.Name,
				Status:    inst.Status,
				StartedAt: inst.CreatedAt,
				Tags: models.InstanceTags{
					ShopperSessionID: sessionID,
				},
				PricePerHour: inst.PricePerHour,
			})
		} else {
			c.logger.Warn("TensorDock instance without shopper prefix detected",
				slog.String("instance_id", inst.ID),
				slog.String("instance_name", inst.Name),
				slog.String("status", inst.Status))
		}
	}
	return result
}
```

**Step 4: Run tests**

Run: `go test ./internal/provider/tensordock/ -run TestClient_instancesToProviderInstances_LogsUnknown -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/provider/tensordock/client.go internal/provider/tensordock/client_test.go
git commit -m "feat: log unknown TensorDock instances during reconciliation

Instances without the 'shopper-' prefix are now logged at WARN level
with their ID and name, providing visibility into potentially leaked
instances."
```

---

### Task 6: Add `BalanceProvider` interface and Vast.ai implementation

**Files:**
- Modify: `internal/provider/interface.go:86` (add interface after `Provider`)
- Modify: `internal/provider/vastai/client.go` (implement `GetAccountBalance`)
- Test: `internal/provider/vastai/client_test.go`

**Step 1: Add interface**

Add to `internal/provider/interface.go` after the `Provider` interface (after line 85):

```go
// BalanceProvider is an optional interface for providers that support account balance checking.
// Used to warn before provisioning when account balance is low.
type BalanceProvider interface {
	GetAccountBalance(ctx context.Context) (*AccountBalance, error)
}

// AccountBalance represents a provider account's current balance.
type AccountBalance struct {
	Balance  float64 // Current balance in provider's currency
	Currency string  // Currency code (e.g., "USD")
}

// ErrBalanceNotSupported indicates a provider doesn't support balance checking.
var ErrBalanceNotSupported = errors.New("balance checking not supported by this provider")
```

**Step 2: Write the failing test for Vast.ai**

Add to `internal/provider/vastai/client_test.go`:

```go
func TestClient_GetAccountBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/current/" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"credit": 42.50,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient("test-api-key", WithBaseURL(server.URL))

	balance, err := client.GetAccountBalance(context.Background())
	require.NoError(t, err)
	assert.InDelta(t, 42.50, balance.Balance, 0.01)
	assert.Equal(t, "USD", balance.Currency)
}

func TestClient_GetAccountBalance_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient("bad-key", WithBaseURL(server.URL))

	_, err := client.GetAccountBalance(context.Background())
	assert.Error(t, err)
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/provider/vastai/ -run TestClient_GetAccountBalance -v`
Expected: FAIL — `GetAccountBalance` not defined

**Step 4: Write Vast.ai implementation**

Add to `internal/provider/vastai/client.go`:

```go
// GetAccountBalance returns the current Vast.ai account balance.
// Implements the provider.BalanceProvider interface.
func (c *Client) GetAccountBalance(ctx context.Context) (*provider.AccountBalance, error) {
	if err := c.rateLimit(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}

	reqURL := fmt.Sprintf("%s/users/current/", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("balance check failed: status %d", resp.StatusCode)
	}

	var result struct {
		Credit float64 `json:"credit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &provider.AccountBalance{
		Balance:  result.Credit,
		Currency: "USD",
	}, nil
}
```

**Step 5: Run tests**

Run: `go test ./internal/provider/vastai/ -run TestClient_GetAccountBalance -v`
Expected: PASS

**Step 6: Verify compile-time interface satisfaction**

Add to the top of `internal/provider/vastai/client.go` (near other compile-time checks):

```go
var _ provider.BalanceProvider = (*Client)(nil)
```

**Step 7: Commit**

```bash
git add internal/provider/interface.go internal/provider/vastai/client.go internal/provider/vastai/client_test.go
git commit -m "feat: add BalanceProvider interface with Vast.ai implementation

New optional BalanceProvider interface for checking account balance
before provisioning. Vast.ai implements it using /users/current/.
TensorDock and Blue Lobster don't support it yet."
```

---

### Task 7: Add pre-provision balance warning to provisioner

**Files:**
- Modify: `internal/service/provisioner/service.go:60` (add constant)
- Modify: `internal/service/provisioner/service.go:365-370` (add balance check)
- Test: `internal/service/provisioner/service_test.go`

**Step 1: Write the failing test**

Add to `internal/service/provisioner/service_test.go`:

```go
func TestCreateSession_WarnsOnLowBalance(t *testing.T) {
	// This test verifies the provisioner logs a warning when balance is low
	// but still proceeds with provisioning (warn-only behavior).
	// The actual assertion is that provisioning proceeds — the warning
	// is tested indirectly through the balance check being called.

	// Set up a mock provider that implements BalanceProvider
	// with a low balance of $2.00
	mockProv := &mockProviderWithBalance{
		Provider: &mockProvider{name: "vastai"},
		balance:  2.00,
	}

	// ... (wire up provisioner with mock, call CreateSession)
	// The key assertion: CreateSession should succeed (not block)
	// even though balance is low
}
```

Note: This test will need to work within the existing test patterns in `service_test.go`. Study the existing `TestCreateSession` tests to match the mock setup patterns. The critical behavior to test is:
1. When provider implements `BalanceProvider` and balance < threshold → provisioning proceeds (warn-only)
2. When provider does not implement `BalanceProvider` → provisioning proceeds normally (no error)

**Step 2: Write implementation**

1. Add constant (after line ~60):
```go
// DefaultLowBalanceThreshold is the balance below which a warning is logged
DefaultLowBalanceThreshold = 5.00
```

2. Add field to `Service` struct:
```go
lowBalanceThreshold float64
```

3. Initialize in `New()`:
```go
lowBalanceThreshold: DefaultLowBalanceThreshold,
```

4. Add balance check at the start of `createSessionWithRetry` (after line 370, before the main provisioning logic):

```go
	// Check provider balance (warn-only)
	prov, err := s.providers.Get(offer.Provider)
	if err == nil {
		if bp, ok := prov.(provider.BalanceProvider); ok {
			if balance, err := bp.GetAccountBalance(ctx); err == nil {
				if balance.Balance < s.lowBalanceThreshold {
					s.logger.Warn("LOW BALANCE: provider account balance is below threshold",
						slog.String("provider", offer.Provider),
						slog.Float64("balance", balance.Balance),
						slog.Float64("threshold", s.lowBalanceThreshold),
						slog.String("currency", balance.Currency))
				}
			} else {
				s.logger.Debug("could not check provider balance",
					slog.String("provider", offer.Provider),
					slog.String("error", err.Error()))
			}
		}
	}
```

**Step 3: Run tests**

Run: `go test ./internal/service/provisioner/ -v`
Expected: All PASS

**Step 4: Commit**

```bash
git add internal/service/provisioner/service.go internal/service/provisioner/service_test.go
git commit -m "feat: add pre-provision balance warning

Checks provider account balance before provisioning and logs a WARNING
when below threshold ($5 default). Warn-only — does not block
provisioning. Gracefully skipped for providers that don't implement
BalanceProvider."
```

---

### Task 8: Final validation

**Step 1: Run full test suite**

Run: `go test ./... 2>&1 | tail -30`
Expected: All PASS, no new failures

**Step 2: Run go vet**

Run: `go vet ./...`
Expected: No issues

**Step 3: Check formatting**

Run: `gofmt -l .`
Expected: No output (all formatted)

**Step 4: Final commit if any formatting fixes needed**

```bash
gofmt -w .
git add -A
git commit -m "style: fix formatting"
```
