# Blue Lobster Provider Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement a Blue Lobster Cloud provider adapter that implements `provider.Provider`, enabling GPU provisioning through Blue Lobster's async task-based API.

**Architecture:** Mirror the existing Vast.ai/TensorDock provider pattern — `NewClient()` with functional options, token bucket rate limiter, circuit breaker, Prometheus metrics. Blue Lobster's async task model (launch returns task_id, poll for completion) is handled inside `CreateInstance` with a 3-minute polling timeout.

**Tech Stack:** Go, `golang.org/x/time/rate` (token bucket), `net/http`, `httptest` (tests)

---

### Task 1: Create Blue Lobster API Types

**Files:**
- Create: `internal/provider/bluelobster/types.go`

**Step 1: Write types.go with all API request/response structs**

```go
package bluelobster

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	// BlueLobsterAvailabilityConfidence is set to 1.0 — dedicated hardware, no oversubscription.
	BlueLobsterAvailabilityConfidence = 1.0
)

// AvailableResponse is the response from GET /instances/available
type AvailableResponse struct {
	Data []AvailableInstance `json:"data"`
}

// AvailableInstance represents an instance type with regional availability
type AvailableInstance struct {
	ID           string       `json:"id"`
	InstanceType InstanceType `json:"instance_type"`
	Regions      []Region     `json:"regions_with_capacity_available"`
}

// InstanceType describes a Blue Lobster instance configuration
type InstanceType struct {
	Name              string       `json:"name"`
	Description       string       `json:"description"`
	GPUDescription    string       `json:"gpu_description,omitempty"`
	PriceCentsPerHour int          `json:"price_cents_per_hour"`
	Specs             InstanceSpec `json:"specs"`
}

// InstanceSpec describes the resource specs of an instance type
type InstanceSpec struct {
	VCPUs     int             `json:"vcpus"`
	MemoryGiB int             `json:"memory_gib"`
	StorageGiB int            `json:"storage_gib"`
	GPUs      int             `json:"gpus"`
	GPUModel  json.RawMessage `json:"gpu_model,omitempty"` // string or []string
}

// ParseGPUModel handles the polymorphic gpu_model field (string or []string).
// Returns the first model name, or empty string if no GPU.
func (s *InstanceSpec) ParseGPUModel() string {
	if s.GPUModel == nil || len(s.GPUModel) == 0 {
		return ""
	}

	// Try string first
	var single string
	if err := json.Unmarshal(s.GPUModel, &single); err == nil {
		return single
	}

	// Try []string
	var multi []string
	if err := json.Unmarshal(s.GPUModel, &multi); err == nil && len(multi) > 0 {
		return multi[0]
	}

	return ""
}

// Region describes a datacenter region with capacity
type Region struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Location    RegionLocation `json:"location"`
}

// RegionLocation describes the physical location of a region
type RegionLocation struct {
	City    string `json:"city"`
	State   string `json:"state"`
	Country string `json:"country"`
}

// ToGPUOffer converts an AvailableInstance + Region to a unified GPUOffer
func (a AvailableInstance) ToGPUOffer(region Region) models.GPUOffer {
	gpuModel := normalizeGPUName(a.InstanceType.Specs.ParseGPUModel())
	// Estimate VRAM from description (e.g., "1x RTX A5000 (24 GB)")
	vram := parseVRAMFromDescription(a.InstanceType.Description)

	return models.GPUOffer{
		ID:                     fmt.Sprintf("bluelobster:%s:%s", a.InstanceType.Name, region.Name),
		Provider:               "bluelobster",
		ProviderID:             a.InstanceType.Name,
		GPUType:                gpuModel,
		GPUCount:               a.InstanceType.Specs.GPUs,
		VRAM:                   vram,
		PricePerHour:           float64(a.InstanceType.PriceCentsPerHour) / 100.0,
		Location:               fmt.Sprintf("%s, %s", region.Location.City, region.Location.State),
		Reliability:            1.0,
		Available:              true,
		MaxDuration:            0,
		FetchedAt:              time.Now(),
		AvailabilityConfidence: BlueLobsterAvailabilityConfidence,
	}
}

// LaunchInstanceRequest is the request body for POST /instances/launch-instance
type LaunchInstanceRequest struct {
	Region       string            `json:"region"`
	InstanceType string            `json:"instance_type"`
	Username     string            `json:"username"`
	SSHKey       string            `json:"ssh_key,omitempty"`
	Name         string            `json:"name,omitempty"`
	TemplateName string            `json:"template_name,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// LaunchInstanceResponse is the response from POST /instances/launch-instance
type LaunchInstanceResponse struct {
	Data LaunchData `json:"data"`
}

// LaunchData contains the launch result
type LaunchData struct {
	InstanceIDs []string `json:"instance_ids"`
	TaskID      string   `json:"task_id"`
	AssignedIP  string   `json:"assigned_ip"`
	Status      string   `json:"status"`
}

// TaskResponse is the response from GET /tasks/{task_id}
type TaskResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"` // PENDING, PROCESSING, COMPLETED, FAILED
	Operation string `json:"operation"`
	Message   string `json:"message"`
	Params    struct {
		VMUUID string `json:"vm_uuid"`
	} `json:"params"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// VMInstance is the response from GET /instances/{id}
type VMInstance struct {
	UUID              string            `json:"uuid"`
	Name              string            `json:"name"`
	HostID            string            `json:"host_id"`
	Region            string            `json:"region"`
	IPAddress         string            `json:"ip_address"`
	InternalIP        string            `json:"internal_ip"`
	CPUCores          int               `json:"cpu_cores"`
	Memory            int               `json:"memory"`
	Storage           int               `json:"storage"`
	GPUCount          int               `json:"gpu_count"`
	GPUModel          string            `json:"gpu_model"`
	PowerStatus       string            `json:"power_status"` // running, stopped, paused, unknown
	CreatedAt         string            `json:"created_at"`
	Metadata          map[string]string `json:"metadata"`
	InstanceType      string            `json:"instance_type"`
	PriceCentsPerHour int               `json:"price_cents_per_hour"`
	VMUsername        string            `json:"vm_username"`
}

// DeleteInstanceResponse is the response from DELETE /instances/{id}
type DeleteInstanceResponse struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	InstanceID string `json:"instance_id"`
}

// ErrorResponse is the error format from the Blue Lobster API.
// The API returns errors in two possible shapes:
//   - {"error": "...", "message": "..."}
//   - {"detail": {"error": "...", "message": "..."}}
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ErrorDetailResponse wraps errors in a detail field
type ErrorDetailResponse struct {
	Detail ErrorResponse `json:"detail"`
}

// normalizeGPUName converts Blue Lobster GPU names to standardized names.
func normalizeGPUName(name string) string {
	name = strings.TrimSpace(name)
	prefixes := []string{"NVIDIA ", "GeForce ", "Quadro "}
	for _, prefix := range prefixes {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}

// parseVRAMFromDescription extracts VRAM in GB from descriptions like "1x RTX A5000 (24 GB)"
func parseVRAMFromDescription(desc string) int {
	// Look for pattern like "(24 GB)" or "(48 GB)"
	// Simple approach: find number before " GB)"
	idx := strings.Index(desc, " GB)")
	if idx < 0 {
		return 0
	}
	// Walk backwards to find the start of the number
	start := idx - 1
	for start >= 0 && desc[start] >= '0' && desc[start] <= '9' {
		start--
	}
	start++
	if start >= idx {
		return 0
	}
	var vram int
	fmt.Sscanf(desc[start:idx], "%d", &vram)
	return vram
}

// parseOfferID extracts instance_type and region from an offer ID.
// Format: "bluelobster:{instance_type}:{region}"
func parseOfferID(offerID string) (instanceType, region string, err error) {
	parts := strings.SplitN(offerID, ":", 3)
	if len(parts) != 3 || parts[0] != "bluelobster" {
		return "", "", fmt.Errorf("invalid Blue Lobster offer ID: %s", offerID)
	}
	return parts[1], parts[2], nil
}
```

**Step 2: Run `go vet` to verify types compile**

Run: `go vet ./internal/provider/bluelobster/...`
Expected: PASS (no errors)

**Step 3: Commit**

```bash
git add internal/provider/bluelobster/types.go
git commit -m "feat(bluelobster): add API request/response types"
```

---

### Task 2: Create Blue Lobster Client Core

**Files:**
- Create: `internal/provider/bluelobster/client.go`

**Step 1: Write client.go with Client struct, constructor, options, and helper methods**

```go
package bluelobster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"golang.org/x/time/rate"
)

const (
	defaultBaseURL     = "https://api.bluelobster.ai/api/v1"
	defaultTimeout     = 30 * time.Second
	defaultTemplate    = "UBUNTU-22-04-NV"
	defaultSSHUser     = "ubuntu"
	defaultSSHPort     = 22
	taskPollInterval   = 3 * time.Second
	taskPollTimeout    = 3 * time.Minute
)

// CircuitBreakerState represents the current state of the circuit breaker
type CircuitBreakerState int

const (
	CircuitClosed CircuitBreakerState = iota
	CircuitOpen
	CircuitHalfOpen
)

// CircuitBreakerConfig configures the circuit breaker behavior
type CircuitBreakerConfig struct {
	FailureThreshold int
	ResetTimeout     time.Duration
	MaxBackoff       time.Duration
	BaseBackoff      time.Duration
}

// DefaultCircuitBreakerConfig returns sensible defaults
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		ResetTimeout:     30 * time.Second,
		MaxBackoff:       2 * time.Minute,
		BaseBackoff:      1 * time.Second,
	}
}

// circuitBreaker implements a simple circuit breaker pattern with exponential backoff
type circuitBreaker struct {
	mu               sync.Mutex
	state            CircuitBreakerState
	failures         int
	lastFailure      time.Time
	lastStateChange  time.Time
	config           CircuitBreakerConfig
	consecutiveWaits int
}

func newCircuitBreaker(config CircuitBreakerConfig) *circuitBreaker {
	return &circuitBreaker{state: CircuitClosed, config: config}
}

func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastStateChange) > cb.config.ResetTimeout {
			cb.state = CircuitHalfOpen
			cb.lastStateChange = time.Now()
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	default:
		return true
	}
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.consecutiveWaits = 0
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitClosed
		cb.lastStateChange = time.Now()
	}
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()
		cb.consecutiveWaits++
		return
	}
	if cb.failures >= cb.config.FailureThreshold {
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()
		cb.consecutiveWaits++
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open
var ErrCircuitOpen = fmt.Errorf("circuit breaker is open")

// Client implements provider.Provider for Blue Lobster Cloud
type Client struct {
	apiKey          string
	baseURL         string
	httpClient      *http.Client
	limiter         *rate.Limiter
	circuitBreaker  *circuitBreaker
	logger          *slog.Logger
	defaultTemplate string
}

// ClientOption configures the Blue Lobster client
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL (for testing)
func WithBaseURL(url string) ClientOption {
	return func(c *Client) { c.baseURL = url }
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = client }
}

// WithRateLimit configures the token bucket rate limiter
func WithRateLimit(r rate.Limit, burst int) ClientOption {
	return func(c *Client) { c.limiter = rate.NewLimiter(r, burst) }
}

// WithCircuitBreaker configures the circuit breaker
func WithCircuitBreaker(config CircuitBreakerConfig) ClientOption {
	return func(c *Client) { c.circuitBreaker = newCircuitBreaker(config) }
}

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) { c.logger = logger }
}

// WithDefaultTemplate overrides the default OS template
func WithDefaultTemplate(name string) ClientOption {
	return func(c *Client) { c.defaultTemplate = name }
}

// NewClient creates a new Blue Lobster client
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:          apiKey,
		baseURL:         defaultBaseURL,
		httpClient:      &http.Client{Timeout: defaultTimeout},
		limiter:         rate.NewLimiter(2, 3), // 2 req/s, burst 3
		circuitBreaker:  newCircuitBreaker(DefaultCircuitBreakerConfig()),
		logger:          slog.Default(),
		defaultTemplate: defaultTemplate,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Name returns the provider identifier
func (c *Client) Name() string { return "bluelobster" }

// SupportsFeature checks if the provider supports a specific feature
func (c *Client) SupportsFeature(feature provider.ProviderFeature) bool {
	switch feature {
	case provider.FeatureInstanceTags:
		return true // Blue Lobster supports metadata on instances
	default:
		return false
	}
}

// rateLimit waits for the token bucket rate limiter
func (c *Client) rateLimit(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}

// checkCircuitBreaker returns an error if the circuit is open
func (c *Client) checkCircuitBreaker() error {
	if !c.circuitBreaker.allow() {
		return ErrCircuitOpen
	}
	return nil
}

// recordAPIResult records success/failure to the circuit breaker
func (c *Client) recordAPIResult(err error) {
	if err != nil {
		c.circuitBreaker.recordFailure()
	} else {
		c.circuitBreaker.recordSuccess()
	}
}

// recordAPIMetrics records Prometheus metrics for an API call
func (c *Client) recordAPIMetrics(operation string, startTime time.Time, err error) {
	duration := time.Since(startTime)
	metrics.RecordProviderAPIResponseTime("bluelobster", operation, duration)
	status := "success"
	if err != nil {
		status = "error"
	}
	metrics.RecordProviderAPICall("bluelobster", operation, status)
}

// doRequest executes an authenticated HTTP request and decodes the JSON response.
// It handles rate limiting, circuit breaker, error parsing, and metrics.
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader, result interface{}) error {
	if err := c.checkCircuitBreaker(); err != nil {
		metrics.RecordProviderAPICall("bluelobster", path, "circuit_open")
		return err
	}

	if err := c.rateLimit(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.recordAPIResult(err)
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.recordAPIResult(err)
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := c.parseError(resp.StatusCode, respBody, path)
		c.recordAPIResult(apiErr)
		return apiErr
	}

	c.recordAPIResult(nil)

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// parseError converts an HTTP error response into a ProviderError.
// Handles both {"error": ..., "message": ...} and {"detail": {"error": ..., "message": ...}} shapes.
func (c *Client) parseError(statusCode int, body []byte, operation string) error {
	// Try {"detail": {"error": ..., "message": ...}} first
	var detailResp ErrorDetailResponse
	if err := json.Unmarshal(body, &detailResp); err == nil && detailResp.Detail.Error != "" {
		return c.mapHTTPError(statusCode, detailResp.Detail.Message, operation)
	}

	// Try {"error": ..., "message": ...}
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		return c.mapHTTPError(statusCode, errResp.Message, operation)
	}

	return c.mapHTTPError(statusCode, string(body), operation)
}

// mapHTTPError maps an HTTP status code to the appropriate provider error
func (c *Client) mapHTTPError(statusCode int, message, operation string) error {
	var sentinel error
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		sentinel = provider.ErrProviderAuth
	case statusCode == http.StatusNotFound:
		sentinel = provider.ErrInstanceNotFound
	case statusCode == http.StatusConflict:
		sentinel = provider.ErrOfferUnavailable
	case statusCode == http.StatusTooManyRequests:
		sentinel = provider.ErrProviderRateLimit
	default:
		sentinel = provider.ErrProviderError
	}

	return provider.NewProviderError("bluelobster", operation, statusCode, message, sentinel)
}
```

**Step 2: Run `go vet` to verify it compiles**

Run: `go vet ./internal/provider/bluelobster/...`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/provider/bluelobster/client.go
git commit -m "feat(bluelobster): add client core with rate limiter and circuit breaker"
```

---

### Task 3: Implement ListOffers

**Files:**
- Modify: `internal/provider/bluelobster/client.go` (append method)

**Step 1: Write the failing test**

Create `internal/provider/bluelobster/client_test.go`:

```go
package bluelobster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient("test-api-key", WithBaseURL(server.URL))
}

func TestListOffers_FiltersOutCPUOnly(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances/available" {
			http.NotFound(w, r)
			return
		}
		resp := AvailableResponse{
			Data: []AvailableInstance{
				{
					ID: "v1_cpu_small",
					InstanceType: InstanceType{
						Name:              "v1_cpu_small",
						Description:       "CPU Only Instance",
						PriceCentsPerHour: 3,
						Specs:             InstanceSpec{VCPUs: 1, MemoryGiB: 8, StorageGiB: 250, GPUs: 0},
					},
					Regions: []Region{{Name: "igl", Description: "Wilmington", Location: RegionLocation{City: "Wilmington", State: "DE", Country: "USA"}}},
				},
				{
					ID: "v1_gpu_1x_a5000",
					InstanceType: InstanceType{
						Name:              "v1_gpu_1x_a5000",
						Description:       "1x RTX A5000 (24 GB)",
						PriceCentsPerHour: 30,
						Specs:             InstanceSpec{VCPUs: 4, MemoryGiB: 32, StorageGiB: 250, GPUs: 1, GPUModel: json.RawMessage(`"RTX A5000"`)},
					},
					Regions: []Region{{Name: "igl", Description: "Wilmington", Location: RegionLocation{City: "Wilmington", State: "DE", Country: "USA"}}},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 GPU offer, got %d", len(offers))
	}
	if offers[0].GPUType != "RTX A5000" {
		t.Errorf("expected GPU type RTX A5000, got %s", offers[0].GPUType)
	}
	if offers[0].PricePerHour != 0.30 {
		t.Errorf("expected price $0.30, got $%.2f", offers[0].PricePerHour)
	}
	if offers[0].ID != "bluelobster:v1_gpu_1x_a5000:igl" {
		t.Errorf("unexpected offer ID: %s", offers[0].ID)
	}
}

func TestListOffers_GPUModelArrayHandling(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := AvailableResponse{
			Data: []AvailableInstance{
				{
					ID: "v1_gpu_1x_8000",
					InstanceType: InstanceType{
						Name:              "v1_gpu_1x_8000",
						Description:       "1x RTX 8000 (48 GB)",
						PriceCentsPerHour: 35,
						Specs:             InstanceSpec{VCPUs: 8, MemoryGiB: 32, StorageGiB: 250, GPUs: 1, GPUModel: json.RawMessage(`["Quadro RTX 8000", "RTX 8000"]`)},
					},
					Regions: []Region{{Name: "igl", Description: "Wilmington", Location: RegionLocation{City: "Wilmington", State: "DE", Country: "USA"}}},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
	// Quadro prefix stripped by normalizeGPUName
	if offers[0].GPUType != "RTX 8000" {
		t.Errorf("expected GPU type RTX 8000, got %s", offers[0].GPUType)
	}
}

func TestListOffers_MultiRegionExpandsOffers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := AvailableResponse{
			Data: []AvailableInstance{
				{
					ID: "v1_cpu_small",
					InstanceType: InstanceType{
						Name:              "v1_cpu_small",
						Description:       "CPU Only",
						PriceCentsPerHour: 3,
						Specs:             InstanceSpec{VCPUs: 1, MemoryGiB: 8, StorageGiB: 250, GPUs: 0},
					},
					Regions: []Region{
						{Name: "igl", Location: RegionLocation{City: "Wilmington", State: "DE"}},
						{Name: "phl", Location: RegionLocation{City: "Philadelphia", State: "PA"}},
					},
				},
				{
					ID: "v1_gpu_1x_a4000",
					InstanceType: InstanceType{
						Name:              "v1_gpu_1x_a4000",
						Description:       "1x RTX A4000 (16 GB)",
						PriceCentsPerHour: 20,
						Specs:             InstanceSpec{VCPUs: 4, MemoryGiB: 16, StorageGiB: 250, GPUs: 1, GPUModel: json.RawMessage(`"RTX A4000"`)},
					},
					Regions: []Region{
						{Name: "igl", Location: RegionLocation{City: "Wilmington", State: "DE"}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	client := newTestClient(t, handler)
	offers, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only 1 GPU instance type × 1 region = 1 offer (CPU types skipped)
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/bluelobster/... -run TestListOffers -v`
Expected: FAIL — `ListOffers` method not defined

**Step 3: Implement ListOffers**

Append to `internal/provider/bluelobster/client.go`:

```go
// ListOffers returns available GPU offers from Blue Lobster.
// CPU-only instance types (gpus: 0) are filtered out.
// Each instance type × region combination becomes a separate offer.
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	startTime := time.Now()
	defer func() { c.recordAPIMetrics("ListOffers", startTime, nil) }()

	var resp AvailableResponse
	if err := c.doRequest(ctx, http.MethodGet, "/instances/available", nil, &resp); err != nil {
		c.recordAPIMetrics("ListOffers", startTime, err)
		return nil, err
	}

	var offers []models.GPUOffer
	for _, inst := range resp.Data {
		// Skip CPU-only instances
		if inst.InstanceType.Specs.GPUs == 0 {
			continue
		}
		// Create one offer per region with capacity
		for _, region := range inst.Regions {
			offer := inst.ToGPUOffer(region)
			if offer.MatchesFilter(filter) {
				offers = append(offers, offer)
			}
		}
	}

	return offers, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/bluelobster/... -run TestListOffers -v`
Expected: PASS (all 3 tests)

**Step 5: Commit**

```bash
git add internal/provider/bluelobster/client.go internal/provider/bluelobster/client_test.go
git commit -m "feat(bluelobster): implement ListOffers with GPU filtering"
```

---

### Task 4: Implement CreateInstance

**Files:**
- Modify: `internal/provider/bluelobster/client.go` (append method)
- Modify: `internal/provider/bluelobster/client_test.go` (append tests)

**Step 1: Write the failing tests**

Append to `client_test.go`:

```go
func TestCreateInstance_HappyPath(t *testing.T) {
	taskCompleted := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/instances/launch-instance":
			// Verify request body
			var req LaunchInstanceRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.Region != "igl" {
				t.Errorf("expected region igl, got %s", req.Region)
			}
			if req.InstanceType != "v1_gpu_1x_a5000" {
				t.Errorf("expected instance type v1_gpu_1x_a5000, got %s", req.InstanceType)
			}
			if req.Username != "ubuntu" {
				t.Errorf("expected username ubuntu, got %s", req.Username)
			}
			if req.TemplateName != "UBUNTU-22-04-NV" {
				t.Errorf("expected template UBUNTU-22-04-NV, got %s", req.TemplateName)
			}
			json.NewEncoder(w).Encode(LaunchInstanceResponse{
				Data: LaunchData{
					InstanceIDs: []string{"vm-abc123"},
					TaskID:      "task-xyz",
					AssignedIP:  "1.2.3.4",
					Status:      "PENDING",
				},
			})

		case r.Method == "GET" && r.URL.Path == "/tasks/task-xyz":
			taskCompleted = true
			json.NewEncoder(w).Encode(TaskResponse{
				TaskID: "task-xyz",
				Status: "COMPLETED",
			})

		case r.Method == "GET" && r.URL.Path == "/instances/vm-abc123":
			json.NewEncoder(w).Encode(VMInstance{
				UUID:              "vm-abc123",
				Name:              "shopper-sess-123",
				IPAddress:         "1.2.3.4",
				PowerStatus:       "running",
				GPUCount:          1,
				GPUModel:          "RTX A5000",
				PriceCentsPerHour: 30,
				VMUsername:        "ubuntu",
			})

		default:
			http.NotFound(w, r)
		}
	})

	client := newTestClient(t, handler)
	info, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "bluelobster:v1_gpu_1x_a5000:igl",
		SessionID:    "sess-123",
		SSHPublicKey: "ssh-ed25519 AAAA test@test",
		Tags:         models.InstanceTags{ShopperSessionID: "sess-123", ShopperDeploymentID: "deploy-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !taskCompleted {
		t.Error("expected task to be polled")
	}
	if info.ProviderInstanceID != "vm-abc123" {
		t.Errorf("expected instance ID vm-abc123, got %s", info.ProviderInstanceID)
	}
	if info.SSHHost != "1.2.3.4" {
		t.Errorf("expected SSH host 1.2.3.4, got %s", info.SSHHost)
	}
	if info.SSHPort != 22 {
		t.Errorf("expected SSH port 22, got %d", info.SSHPort)
	}
	if info.SSHUser != "ubuntu" {
		t.Errorf("expected SSH user ubuntu, got %s", info.SSHUser)
	}
}

func TestCreateInstance_TaskFailed(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/instances/launch-instance":
			json.NewEncoder(w).Encode(LaunchInstanceResponse{
				Data: LaunchData{TaskID: "task-fail", InstanceIDs: []string{"vm-fail"}, Status: "PENDING"},
			})
		case r.Method == "GET" && r.URL.Path == "/tasks/task-fail":
			json.NewEncoder(w).Encode(TaskResponse{
				TaskID:  "task-fail",
				Status:  "FAILED",
				Message: "No capacity available",
			})
		default:
			http.NotFound(w, r)
		}
	})

	client := newTestClient(t, handler)
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID:      "bluelobster:v1_gpu_1x_a5000:igl",
		SessionID:    "sess-fail",
		SSHPublicKey: "ssh-ed25519 AAAA test@test",
	})
	if err == nil {
		t.Fatal("expected error for failed task")
	}
}

func TestCreateInstance_InvalidOfferID(t *testing.T) {
	client := NewClient("test-key")
	_, err := client.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		OfferID: "invalid-offer",
	})
	if err == nil {
		t.Fatal("expected error for invalid offer ID")
	}
}
```

Add this import to the test file imports: `"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"`

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/bluelobster/... -run TestCreateInstance -v`
Expected: FAIL — `CreateInstance` method not defined

**Step 3: Implement CreateInstance**

Append to `client.go`:

```go
// CreateInstance provisions a new GPU instance on Blue Lobster.
// It launches the instance, then polls the task API for up to 3 minutes.
// If the task completes, it fetches full instance details.
// If the task is still in progress after the timeout, it returns partial info.
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	startTime := time.Now()

	instanceType, region, err := parseOfferID(req.OfferID)
	if err != nil {
		return nil, err
	}

	// Build metadata tags
	metadata := req.Tags.ToMap()

	// Build launch request
	launchReq := LaunchInstanceRequest{
		Region:       region,
		InstanceType: instanceType,
		Username:     defaultSSHUser,
		SSHKey:       req.SSHPublicKey,
		Name:         req.Tags.ToLabel(),
		TemplateName: c.defaultTemplate,
		Metadata:     metadata,
	}

	// Launch the instance
	var launchResp LaunchInstanceResponse
	body, _ := json.Marshal(launchReq)
	if err := c.doRequest(ctx, http.MethodPost, "/instances/launch-instance", bytes.NewReader(body), &launchResp); err != nil {
		c.recordAPIMetrics("CreateInstance", startTime, err)
		return nil, err
	}

	taskID := launchResp.Data.TaskID
	instanceID := ""
	if len(launchResp.Data.InstanceIDs) > 0 {
		instanceID = launchResp.Data.InstanceIDs[0]
	}

	// Poll task until complete or timeout
	pollCtx, cancel := context.WithTimeout(ctx, taskPollTimeout)
	defer cancel()

	ticker := time.NewTicker(taskPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			// Timeout — return partial info
			c.logger.Warn("task poll timeout, returning partial instance info",
				slog.String("task_id", taskID),
				slog.String("instance_id", instanceID))
			c.recordAPIMetrics("CreateInstance", startTime, nil)
			return &provider.InstanceInfo{
				ProviderInstanceID: instanceID,
				SSHHost:            launchResp.Data.AssignedIP,
				SSHPort:            defaultSSHPort,
				SSHUser:            defaultSSHUser,
				Status:             "provisioning",
			}, nil

		case <-ticker.C:
			var taskResp TaskResponse
			if err := c.doRequest(ctx, http.MethodGet, "/tasks/"+taskID, nil, &taskResp); err != nil {
				c.logger.Warn("task poll error", slog.String("error", err.Error()))
				continue
			}

			switch taskResp.Status {
			case "COMPLETED":
				// Fetch full instance details
				info, err := c.getInstanceInfo(ctx, instanceID)
				if err != nil {
					c.recordAPIMetrics("CreateInstance", startTime, err)
					return nil, fmt.Errorf("instance created but failed to get details: %w", err)
				}
				c.recordAPIMetrics("CreateInstance", startTime, nil)
				return info, nil

			case "FAILED":
				err := fmt.Errorf("instance creation failed: %s", taskResp.Message)
				c.recordAPIMetrics("CreateInstance", startTime, err)
				return nil, provider.NewProviderError("bluelobster", "CreateInstance", 0, taskResp.Message, provider.ErrProviderError)

			default:
				// PENDING or PROCESSING — keep polling
				continue
			}
		}
	}
}

// getInstanceInfo fetches instance details and returns an InstanceInfo
func (c *Client) getInstanceInfo(ctx context.Context, instanceID string) (*provider.InstanceInfo, error) {
	var vm VMInstance
	if err := c.doRequest(ctx, http.MethodGet, "/instances/"+instanceID, nil, &vm); err != nil {
		return nil, err
	}

	user := vm.VMUsername
	if user == "" {
		user = defaultSSHUser
	}

	return &provider.InstanceInfo{
		ProviderInstanceID: vm.UUID,
		SSHHost:            vm.IPAddress,
		SSHPort:            defaultSSHPort,
		SSHUser:            user,
		Status:             vm.PowerStatus,
		ActualPricePerHour: float64(vm.PriceCentsPerHour) / 100.0,
	}, nil
}
```

Note: Add `"bytes"` to the imports in client.go.

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/bluelobster/... -run TestCreateInstance -v`
Expected: PASS (all 3 tests)

**Step 5: Commit**

```bash
git add internal/provider/bluelobster/client.go internal/provider/bluelobster/client_test.go
git commit -m "feat(bluelobster): implement CreateInstance with task polling"
```

---

### Task 5: Implement DestroyInstance and GetInstanceStatus

**Files:**
- Modify: `internal/provider/bluelobster/client.go` (append methods)
- Modify: `internal/provider/bluelobster/client_test.go` (append tests)

**Step 1: Write failing tests**

Append to `client_test.go`:

```go
func TestDestroyInstance_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/instances/vm-abc123" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(DeleteInstanceResponse{Status: "success", InstanceID: "vm-abc123"})
	})

	client := newTestClient(t, handler)
	err := client.DestroyInstance(context.Background(), "vm-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDestroyInstance_NotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "instance_not_found", Message: "not found"})
	})

	client := newTestClient(t, handler)
	err := client.DestroyInstance(context.Background(), "vm-nonexistent")
	if !provider.IsNotFoundError(err) {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestGetInstanceStatus_Running(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VMInstance{
			UUID:        "vm-abc123",
			IPAddress:   "1.2.3.4",
			PowerStatus: "running",
			VMUsername:  "ubuntu",
		})
	})

	client := newTestClient(t, handler)
	status, err := client.GetInstanceStatus(context.Background(), "vm-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Running {
		t.Error("expected Running to be true")
	}
	if status.SSHHost != "1.2.3.4" {
		t.Errorf("expected SSH host 1.2.3.4, got %s", status.SSHHost)
	}
	if status.SSHPort != 22 {
		t.Errorf("expected SSH port 22, got %d", status.SSHPort)
	}
}

func TestGetInstanceStatus_Stopped(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(VMInstance{
			UUID:        "vm-abc123",
			IPAddress:   "1.2.3.4",
			PowerStatus: "stopped",
		})
	})

	client := newTestClient(t, handler)
	status, err := client.GetInstanceStatus(context.Background(), "vm-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Running {
		t.Error("expected Running to be false for stopped instance")
	}
	if status.Status != "stopped" {
		t.Errorf("expected status stopped, got %s", status.Status)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/bluelobster/... -run "TestDestroy|TestGetInstance" -v`
Expected: FAIL

**Step 3: Implement both methods**

Append to `client.go`:

```go
// DestroyInstance terminates a Blue Lobster instance.
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) error {
	startTime := time.Now()
	err := c.doRequest(ctx, http.MethodDelete, "/instances/"+instanceID, nil, nil)
	c.recordAPIMetrics("DestroyInstance", startTime, err)
	return err
}

// GetInstanceStatus returns the current status of a Blue Lobster instance.
func (c *Client) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	startTime := time.Now()

	var vm VMInstance
	if err := c.doRequest(ctx, http.MethodGet, "/instances/"+instanceID, nil, &vm); err != nil {
		c.recordAPIMetrics("GetInstanceStatus", startTime, err)
		return nil, err
	}
	c.recordAPIMetrics("GetInstanceStatus", startTime, nil)

	running := vm.PowerStatus == "running"
	user := vm.VMUsername
	if user == "" {
		user = defaultSSHUser
	}

	status := &provider.InstanceStatus{
		Status:   vm.PowerStatus,
		Running:  running,
		SSHHost:  vm.IPAddress,
		SSHPort:  defaultSSHPort,
		SSHUser:  user,
		PublicIP: vm.IPAddress,
	}

	if vm.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, vm.CreatedAt); err == nil {
			status.StartedAt = t
		}
	}

	return status, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/bluelobster/... -run "TestDestroy|TestGetInstance" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/provider/bluelobster/client.go internal/provider/bluelobster/client_test.go
git commit -m "feat(bluelobster): implement DestroyInstance and GetInstanceStatus"
```

---

### Task 6: Implement ListAllInstances

**Files:**
- Modify: `internal/provider/bluelobster/client.go` (append method)
- Modify: `internal/provider/bluelobster/client_test.go` (append tests)

**Step 1: Write failing test**

Append to `client_test.go`:

```go
func TestListAllInstances_FiltersByTags(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a mix of our instances and others
		instances := []VMInstance{
			{
				UUID:              "vm-ours",
				Name:              "shopper-sess-1",
				PowerStatus:       "running",
				PriceCentsPerHour: 30,
				Metadata: map[string]string{
					"shopper_session_id":    "sess-1",
					"shopper_deployment_id": "deploy-1",
					"shopper_expires_at":    "2026-02-24T00:00:00Z",
				},
			},
			{
				UUID:        "vm-other",
				Name:        "not-ours",
				PowerStatus: "running",
				Metadata:    nil,
			},
		}
		json.NewEncoder(w).Encode(instances)
	})

	client := newTestClient(t, handler)
	instances, err := client.ListAllInstances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both instances returned — caller filters by deployment ID via IsOurs()
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	// Verify our tagged instance has parsed tags
	found := false
	for _, inst := range instances {
		if inst.ID == "vm-ours" {
			found = true
			if inst.Tags.ShopperSessionID != "sess-1" {
				t.Errorf("expected session ID sess-1, got %s", inst.Tags.ShopperSessionID)
			}
			if inst.Tags.ShopperDeploymentID != "deploy-1" {
				t.Errorf("expected deployment ID deploy-1, got %s", inst.Tags.ShopperDeploymentID)
			}
		}
	}
	if !found {
		t.Error("expected to find vm-ours in results")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/bluelobster/... -run TestListAllInstances -v`
Expected: FAIL

**Step 3: Implement ListAllInstances**

Append to `client.go`:

```go
// ListAllInstances returns all instances for this account.
// Tags are parsed from instance metadata for orphan detection.
func (c *Client) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	startTime := time.Now()

	var vms []VMInstance
	if err := c.doRequest(ctx, http.MethodGet, "/instances", nil, &vms); err != nil {
		c.recordAPIMetrics("ListAllInstances", startTime, err)
		return nil, err
	}
	c.recordAPIMetrics("ListAllInstances", startTime, nil)

	var instances []provider.ProviderInstance
	for _, vm := range vms {
		inst := provider.ProviderInstance{
			ID:           vm.UUID,
			Name:         vm.Name,
			Status:       vm.PowerStatus,
			PricePerHour: float64(vm.PriceCentsPerHour) / 100.0,
		}

		// Parse tags from metadata
		if vm.Metadata != nil {
			inst.Tags.ShopperSessionID = vm.Metadata["shopper_session_id"]
			inst.Tags.ShopperDeploymentID = vm.Metadata["shopper_deployment_id"]
			inst.Tags.ShopperConsumerID = vm.Metadata["shopper_consumer_id"]
			if expiresStr, ok := vm.Metadata["shopper_expires_at"]; ok {
				if t, err := time.Parse(time.RFC3339, expiresStr); err == nil {
					inst.Tags.ShopperExpiresAt = t
				}
			}
		}

		if vm.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, vm.CreatedAt); err == nil {
				inst.StartedAt = t
			}
		}

		instances = append(instances, inst)
	}

	return instances, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/bluelobster/... -run TestListAllInstances -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/provider/bluelobster/client.go internal/provider/bluelobster/client_test.go
git commit -m "feat(bluelobster): implement ListAllInstances with tag parsing"
```

---

### Task 7: Add Error Handling Tests

**Files:**
- Modify: `internal/provider/bluelobster/client_test.go` (append tests)

**Step 1: Write error handling tests**

Append to `client_test.go`:

```go
func TestErrorHandling_AuthError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrorDetailResponse{
			Detail: ErrorResponse{Error: "forbidden", Message: "Invalid API key"},
		})
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if !provider.IsAuthError(err) {
		t.Fatalf("expected auth error, got: %v", err)
	}
}

func TestErrorHandling_RateLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "rate_limited", Message: "Too many requests"})
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if !provider.IsRateLimitError(err) {
		t.Fatalf("expected rate limit error, got: %v", err)
	}
}

func TestErrorHandling_ServerError_IsRetryable(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "server_error", Message: "Internal error"})
	})

	client := newTestClient(t, handler)
	_, err := client.ListOffers(context.Background(), models.OfferFilter{})
	if !provider.IsRetryable(err) {
		t.Fatalf("expected retryable error, got: %v", err)
	}
}

func TestAPIKeyHeader(t *testing.T) {
	var receivedKey string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedKey = r.Header.Get("X-API-Key")
		json.NewEncoder(w).Encode(AvailableResponse{})
	})

	client := newTestClient(t, handler)
	// Override the key for this test
	client.apiKey = "my-secret-key"
	client.ListOffers(context.Background(), models.OfferFilter{})

	if receivedKey != "my-secret-key" {
		t.Errorf("expected API key my-secret-key, got %s", receivedKey)
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/provider/bluelobster/... -run TestError -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/provider/bluelobster/client_test.go
git commit -m "test(bluelobster): add error handling and auth header tests"
```

---

### Task 8: Wire Up Config and Server Initialization

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/server/main.go`

**Step 1: Add BlueLobster config**

In `internal/config/config.go`, add to `ProvidersConfig`:

```go
type ProvidersConfig struct {
	VastAI      VastAIConfig      `mapstructure:"vastai"`
	TensorDock  TensorDockConfig  `mapstructure:"tensordock"`
	BlueLobster BlueLobsterConfig `mapstructure:"bluelobster"`
}

type BlueLobsterConfig struct {
	APIKey          string `mapstructure:"api_key"`
	Enabled         bool   `mapstructure:"enabled"`
	DefaultTemplate string `mapstructure:"default_template"`
}
```

Add to `setDefaults()`:
```go
v.SetDefault("providers.bluelobster.enabled", true)
v.SetDefault("providers.bluelobster.default_template", "UBUNTU-22-04-NV")
```

Add to `mapEnvFileKeys()`:
```go
"bluelobster_api_key": "providers.bluelobster.api_key",
```

Add to `bindEnvVars()`:
```go
bindEnv("providers.bluelobster.api_key", "BLUELOBSTER_API_KEY")
```

Add to `Validate()`:
```go
if c.Providers.BlueLobster.Enabled && c.Providers.BlueLobster.APIKey == "" {
	return fmt.Errorf("BLUELOBSTER_API_KEY is required when Blue Lobster is enabled")
}
```

Update the validation at the top — at least one provider must be enabled:
```go
if !c.Providers.VastAI.Enabled && !c.Providers.TensorDock.Enabled && !c.Providers.BlueLobster.Enabled {
```

**Step 2: Add Blue Lobster initialization to main.go**

In `cmd/server/main.go`, after the TensorDock block (~line 90), add:

```go
if cfg.Providers.BlueLobster.APIKey != "" {
	bluelobsterClient := bluelobster.NewClient(
		cfg.Providers.BlueLobster.APIKey,
		bluelobster.WithDefaultTemplate(cfg.Providers.BlueLobster.DefaultTemplate),
	)
	providers = append(providers, bluelobsterClient)
	logger.Info("initialized Blue Lobster provider",
		slog.String("default_template", cfg.Providers.BlueLobster.DefaultTemplate))
}
```

Add import: `"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider/bluelobster"`

**Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS — all existing tests still pass, new tests pass

**Step 4: Commit**

```bash
git add internal/config/config.go cmd/server/main.go
git commit -m "feat(bluelobster): wire config and server initialization"
```

---

### Task 9: Run Full Suite and Verify

**Step 1: Run all tests**

Run: `go test ./... -count=1`
Expected: PASS

**Step 2: Run gofmt check**

Run: `gofmt -l .`
Expected: No output (all files formatted)

If any files listed, fix with: `gofmt -w .`

**Step 3: Run go vet**

Run: `go vet ./...`
Expected: PASS

**Step 4: Verify server starts with new config**

Run: `go build ./cmd/server/`
Expected: Builds successfully

**Step 5: Final commit if any formatting fixes**

```bash
git add -A && git commit -m "chore: fix formatting"
```

---

### Task 10: Push Branch

**Step 1: Push to remote**

```bash
git push -u origin feat/provider-bluelobster
```

**Step 2: Verify CI passes (if configured)**

Check: `gh run list --branch feat/provider-bluelobster`
