# Token Bucket Rate Limiter Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace sleep-based rate limiting in both provider clients with `golang.org/x/time/rate` token bucket (2 req/s, burst 3).

**Architecture:** Each provider client's `rateLimit()` method is replaced with a context-aware `rate.Limiter.Wait(ctx)` call. The `WithMinInterval` option is kept as a backward-compatible wrapper. All existing tests that pass `WithMinInterval(0)` continue to work (0 duration = unlimited rate).

**Tech Stack:** Go, `golang.org/x/time/rate`

---

### Task 1: Add `golang.org/x/time` dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Add the dependency**

Run: `cd /Users/avc/Documents/cloud-gpu-shopper && go get golang.org/x/time`

Expected: `go.mod` updated with `golang.org/x/time` version, `go.sum` updated.

**Step 2: Verify**

Run: `grep "golang.org/x/time" go.mod`

Expected: Line like `golang.org/x/time v0.x.x`

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add golang.org/x/time for token bucket rate limiter"
```

---

### Task 2: Replace rate limiter in Vast.ai client

**Files:**
- Modify: `internal/provider/vastai/client.go`

**Step 1: Update imports**

In `internal/provider/vastai/client.go:3-20`, add `"golang.org/x/time/rate"` to imports. Remove `"sync"` (no longer needed for rate limiting — but check if `sync` is used elsewhere in the file first; the `templateCache` and `bundleCache` use `sync.RWMutex` so `sync` stays).

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"golang.org/x/time/rate"
)
```

**Step 2: Replace struct fields**

In `internal/provider/vastai/client.go:194-212`, replace the rate limiting fields:

Old:
```go
	// Rate limiting
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration
```

New:
```go
	// Rate limiting (token bucket)
	limiter *rate.Limiter
```

**Step 3: Add `WithRateLimit` option, update `WithMinInterval`**

After `WithMinInterval` at line 231, add `WithRateLimit`. Then update `WithMinInterval` to delegate:

```go
// WithRateLimit sets the token bucket rate limiter parameters.
// rps is requests per second, burst is the maximum burst size.
func WithRateLimit(rps float64, burst int) ClientOption {
	return func(c *Client) {
		c.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
}

// WithMinInterval sets the minimum interval between requests.
// Kept for backward compatibility — converts to token bucket rate.
// A zero duration sets rate.Inf (unlimited).
func WithMinInterval(d time.Duration) ClientOption {
	return func(c *Client) {
		if d <= 0 {
			c.limiter = rate.NewLimiter(rate.Inf, 1)
		} else {
			c.limiter = rate.NewLimiter(rate.Every(d), 1)
		}
	}
}
```

**Step 4: Update constructor default**

In `NewClient` at line 246-262, replace `minInterval: time.Second` with `limiter`:

```go
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:         apiKey,
		baseURL:        defaultBaseURL,
		httpClient:     &http.Client{Timeout: defaultTimeout},
		limiter:        rate.NewLimiter(rate.Limit(2), 3),                 // 2 req/s, burst 3
		circuitBreaker: newCircuitBreaker(DefaultCircuitBreakerConfig()),
		templates:      &templateCache{},
		bundles:        &bundleCache{bundles: make(map[int]Bundle)},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}
```

**Step 5: Replace `rateLimit()` method**

At line 964-974, replace:

Old:
```go
func (c *Client) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.lastRequest)
	if elapsed < c.minInterval {
		time.Sleep(c.minInterval - elapsed)
	}
	c.lastRequest = time.Now()
}
```

New:
```go
// rateLimit waits for a token from the rate limiter.
// Returns an error if the context is cancelled while waiting.
func (c *Client) rateLimit(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}
```

**Step 6: Update all 7 call sites**

Each `c.rateLimit()` call (lines 299, 390, 453, 704, 751, 791, 870) becomes `if err := c.rateLimit(ctx); err != nil { return ..., err }`. The exact return depends on the function signature:

For functions returning `(something, error)`:
```go
	if err := c.rateLimit(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
```

For `AttachSSHKey` which returns just `error` (line 704):
```go
	if err := c.rateLimit(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
```

**Step 7: Run tests**

Run: `cd /Users/avc/Documents/cloud-gpu-shopper && go test ./internal/provider/vastai/... -v -count=1 -timeout 120s 2>&1 | tail -30`

Expected: All tests pass. Tests using `WithMinInterval(0)` will set `rate.Inf` (unlimited), so they won't be throttled.

**Step 8: Commit**

```bash
git add internal/provider/vastai/client.go
git commit -m "feat(vastai): replace sleep-based rate limiter with token bucket (2 req/s, burst 3)"
```

---

### Task 3: Replace rate limiter in TensorDock client

**Files:**
- Modify: `internal/provider/tensordock/client.go`

**Step 1: Update imports**

In `internal/provider/tensordock/client.go:45-61`, add `"golang.org/x/time/rate"`. Keep `"sync"` — it's used by `locationStats`.

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)
```

**Step 2: Replace struct fields**

In `internal/provider/tensordock/client.go:368-393`, replace rate limiting fields:

Old:
```go
	// Rate limiting to avoid 429 errors
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration
```

New:
```go
	// Rate limiting to avoid 429 errors (token bucket)
	limiter *rate.Limiter
```

**Step 3: Add `WithRateLimit` option, update `WithMinInterval`**

After `WithMinInterval` at line 412:

```go
// WithRateLimit sets the token bucket rate limiter parameters.
// rps is requests per second, burst is the maximum burst size.
func WithRateLimit(rps float64, burst int) ClientOption {
	return func(c *Client) {
		c.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
}

// WithMinInterval sets the minimum interval between API requests.
// Kept for backward compatibility — converts to token bucket rate.
// A zero duration sets rate.Inf (unlimited).
func WithMinInterval(d time.Duration) ClientOption {
	return func(c *Client) {
		if d <= 0 {
			c.limiter = rate.NewLimiter(rate.Inf, 1)
		} else {
			c.limiter = rate.NewLimiter(rate.Every(d), 1)
		}
	}
}
```

**Step 4: Update constructor default**

In `NewClient` at line 674-693, replace `minInterval: time.Second` with `limiter`:

```go
func NewClient(apiKey, apiToken string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:         apiKey,
		apiToken:       apiToken,
		baseURL:        defaultBaseURL,
		httpClient:     &http.Client{},
		defaultImage:   defaultImageName,
		timeouts:       DefaultTimeouts(),
		circuitBreaker: newCircuitBreaker(DefaultCircuitBreakerConfig()),
		limiter:        rate.NewLimiter(rate.Limit(2), 3), // 2 req/s, burst 3
		logger:         slog.Default(),
		locationStats:  newLocationStats(),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}
```

**Step 5: Replace `rateLimit()` method**

At line 1295-1305, replace:

```go
// rateLimit waits for a token from the rate limiter.
// Returns an error if the context is cancelled while waiting.
func (c *Client) rateLimit(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}
```

**Step 6: Update all 5 call sites**

Each `c.rateLimit()` call (lines 733, 807, 954, 1120, 1204) becomes:

```go
	if err := c.rateLimit(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
```

For `DestroyInstance` which returns just `error` (line 1120):
```go
	if err := c.rateLimit(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
```

**Step 7: Run tests**

Run: `cd /Users/avc/Documents/cloud-gpu-shopper && go test ./internal/provider/tensordock/... -v -count=1 -timeout 120s 2>&1 | tail -30`

Expected: All tests pass.

**Step 8: Commit**

```bash
git add internal/provider/tensordock/client.go
git commit -m "feat(tensordock): replace sleep-based rate limiter with token bucket (2 req/s, burst 3)"
```

---

### Task 4: Update rate limiting tests

**Files:**
- Modify: `internal/provider/tensordock/edge_cases_test.go`
- Modify: `internal/provider/tensordock/security_test.go`

**Step 1: Update `TestClient_RateLimiting` in `edge_cases_test.go:837`**

The existing test uses `WithMinInterval(100ms)` and expects 3 sequential requests to take >=180ms. With token bucket (burst=1 at 10/s from 100ms interval), the first request consumes the burst, then 2 more need tokens at 100ms each = ~200ms minimum. The test logic stays the same, but update the comment:

```go
func TestClient_RateLimiting(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		json.NewEncoder(w).Encode(LocationsResponse{})
	}))
	defer server.Close()

	// Token bucket: 10 req/s (every 100ms), burst 1
	client := NewClient("test-key", "test-token",
		WithBaseURL(server.URL),
		WithMinInterval(100*time.Millisecond),
	)

	// Make multiple rapid requests
	start := time.Now()
	for i := 0; i < 3; i++ {
		_, _ = client.ListOffers(context.Background(), models.OfferFilter{})
	}
	elapsed := time.Since(start)

	// First request uses the burst token (immediate), next 2 wait ~100ms each = ~200ms
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(180), "Rate limiting should enforce delays")
	assert.Equal(t, 3, requestCount, "All requests should complete")
}
```

**Step 2: Update `TestRateLimitingPreventsRapidRequests` in `security_test.go:323`**

Same approach — the test uses `WithMinInterval(100ms)` and expects intervals. With token bucket (burst 1), the behavior is nearly identical. Update the assertion tolerances slightly since token bucket has slightly different timing characteristics:

```go
func TestRateLimitingPreventsRapidRequests(t *testing.T) {
	var requestCount int32
	var requestTimes []time.Time
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()

		resp := LocationsResponse{
			Data: LocationsData{Locations: []Location{}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Token bucket: 10 req/s (every 100ms), burst 1
	client := NewClient("key", "token",
		WithBaseURL(server.URL),
		WithMinInterval(100*time.Millisecond),
	)

	// Make 3 rapid requests
	start := time.Now()
	for i := 0; i < 3; i++ {
		_, err := client.ListOffers(context.Background(), models.OfferFilter{})
		require.NoError(t, err)
	}
	elapsed := time.Since(start)

	// First request uses burst token, next 2 wait ~100ms each
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(180),
		"Rate limiting should enforce minimum interval between requests")

	mu.Lock()
	defer mu.Unlock()

	// Verify intervals between requests (after the burst)
	for i := 1; i < len(requestTimes); i++ {
		interval := requestTimes[i].Sub(requestTimes[i-1])
		assert.GreaterOrEqual(t, interval.Milliseconds(), int64(85), // Allow 15ms tolerance for token bucket
			"Each request should be at least minInterval apart")
	}
}
```

**Step 3: Run all tests**

Run: `cd /Users/avc/Documents/cloud-gpu-shopper && go test ./internal/provider/... -v -count=1 -timeout 120s 2>&1 | tail -30`

Expected: All tests pass.

**Step 4: Commit**

```bash
git add internal/provider/tensordock/edge_cases_test.go internal/provider/tensordock/security_test.go
git commit -m "test: update rate limiting tests for token bucket behavior"
```

---

### Task 5: Add token bucket specific test

**Files:**
- Modify: `internal/provider/vastai/client_test.go`

**Step 1: Write a test verifying burst behavior**

Add a test to `internal/provider/vastai/client_test.go` that verifies the token bucket burst:

```go
func TestTokenBucketRateLimiting(t *testing.T) {
	var requestTimes []time.Time
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"offers": []interface{}{}})
	}))
	defer server.Close()

	// 5 req/s with burst of 3
	client := NewClient("test-key",
		WithBaseURL(server.URL),
		WithRateLimit(5, 3),
	)

	// Make 5 rapid requests
	start := time.Now()
	for i := 0; i < 5; i++ {
		_, _ = client.ListOffers(context.Background(), models.OfferFilter{})
	}
	elapsed := time.Since(start)

	// First 3 use burst tokens (immediate), then 2 more at 200ms each = ~400ms
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(350),
		"Requests beyond burst should be throttled")

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, 5, len(requestTimes), "All requests should complete")
}

func TestRateLimitRespectsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"offers": []interface{}{}})
	}))
	defer server.Close()

	// Very slow rate to force waiting
	client := NewClient("test-key",
		WithBaseURL(server.URL),
		WithRateLimit(0.1, 1), // 1 per 10 seconds, burst 1
	)

	// First request uses burst token
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	assert.NoError(t, err)

	// Second request must wait — cancel it
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = client.ListOffers(ctx, models.OfferFilter{})
	assert.Error(t, err, "Should fail when context cancelled during rate limit wait")
}
```

**Step 2: Run tests**

Run: `cd /Users/avc/Documents/cloud-gpu-shopper && go test ./internal/provider/vastai/... -v -count=1 -run TestTokenBucket -timeout 30s`

Expected: Both new tests pass.

**Step 3: Run full test suite**

Run: `cd /Users/avc/Documents/cloud-gpu-shopper && go test ./... -count=1 -timeout 300s 2>&1 | tail -20`

Expected: All tests pass.

**Step 4: Commit**

```bash
git add internal/provider/vastai/client_test.go
git commit -m "test: add token bucket burst and context cancellation tests"
```
