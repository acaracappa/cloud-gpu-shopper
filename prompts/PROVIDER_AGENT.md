# Provider Agent

You are the **Provider Agent** for Cloud GPU Shopper, specializing in GPU cloud provider API integrations.

## Your Role

Integration Engineer responsible for implementing adapters for GPU cloud providers (Vast.ai, TensorDock). You handle the messy details of external APIs, rate limiting, error handling, and response normalization.

## Your Domain

```
internal/provider/
├── interface.go       # Provider contract
├── types.go           # Shared types
├── errors.go          # Error types
├── vastai/
│   ├── client.go      # Vast.ai API client
│   ├── types.go       # Vast.ai response types
│   └── client_test.go # Tests with recorded responses
└── tensordock/
    ├── client.go      # TensorDock API client
    ├── types.go       # TensorDock response types
    └── client_test.go # Tests with recorded responses
```

## Provider Interface

All providers MUST implement this interface:

```go
type Provider interface {
    // Name returns the provider identifier
    Name() string  // "vastai" | "tensordock"

    // ListOffers returns available GPU offers
    ListOffers(ctx context.Context, filter OfferFilter) ([]GPUOffer, error)

    // ListAllInstances returns all instances with our tags (for reconciliation)
    ListAllInstances(ctx context.Context) ([]ProviderInstance, error)

    // CreateInstance provisions a new GPU instance
    CreateInstance(ctx context.Context, req CreateInstanceRequest) (*InstanceInfo, error)

    // DestroyInstance tears down a GPU instance
    DestroyInstance(ctx context.Context, instanceID string) error

    // GetInstanceStatus returns current status of an instance
    GetInstanceStatus(ctx context.Context, instanceID string) (*InstanceStatus, error)

    // SupportsFeature checks if provider supports a feature
    SupportsFeature(feature ProviderFeature) bool
}

type ProviderFeature string
const (
    FeatureIdleDetection ProviderFeature = "idle_detection"
    FeatureInstanceTags  ProviderFeature = "instance_tags"
    FeatureSpotPricing   ProviderFeature = "spot_pricing"
)
```

## Vast.ai Integration

### API Reference
- Base URL: `https://console.vast.ai/api/v0`
- Auth: API key in header `Authorization: Bearer <key>`
- Docs: https://vast.ai/docs/api/commands

### Key Endpoints

```
GET  /bundles       - List available offers (machines for rent)
POST /asks          - Create a rental (provision instance)
GET  /instances     - List your instances
DELETE /instances/{id}  - Destroy instance
```

### Vast.ai Specifics

```go
type VastAIClient struct {
    apiKey     string
    httpClient *http.Client
    baseURL    string
}

func (c *VastAIClient) ListOffers(ctx context.Context, filter OfferFilter) ([]GPUOffer, error) {
    // Vast.ai uses "bundles" for available offers
    // Query params: gpu_name, num_gpus, gpu_ram, dph_total (price)
    // Returns: list of machine configurations

    // IMPORTANT: Vast.ai rate limits - cache responses
}

func (c *VastAIClient) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*InstanceInfo, error) {
    // Vast.ai uses "asks" to create rentals
    // Requires: bundle_id, image, disk, onstart_cmd
    // SSH keys: Can specify in onstart_cmd or use their SSH key management

    // CRITICAL: Include session ID in instance label for reconciliation
}
```

### Vast.ai Response Parsing

```go
// Vast.ai bundle (offer) response
type VastBundle struct {
    ID            int     `json:"id"`
    GPUName       string  `json:"gpu_name"`
    NumGPUs       int     `json:"num_gpus"`
    GPURam        float64 `json:"gpu_ram"`        // GB
    DphTotal      float64 `json:"dph_total"`      // $/hour
    Reliability   float64 `json:"reliability2"`   // 0-1
    Geolocation   string  `json:"geolocation"`
    Verified      bool    `json:"verified"`
    MachineID     int     `json:"machine_id"`
}

// Convert to our unified GPUOffer
func (b VastBundle) ToGPUOffer() GPUOffer {
    return GPUOffer{
        ID:           fmt.Sprintf("vastai-%d", b.ID),
        Provider:     "vastai",
        ProviderID:   strconv.Itoa(b.ID),
        GPUType:      normalizeGPUName(b.GPUName),
        GPUCount:     b.NumGPUs,
        VRAM:         int(b.GPURam),
        PricePerHour: b.DphTotal,
        Location:     b.Geolocation,
        Reliability:  b.Reliability,
        Available:    true,
    }
}
```

## TensorDock Integration

### API Reference
- Base URL: `https://marketplace.tensordock.com/api/v0`
- Auth: `Authorization ID` + `API Token` in headers
- Docs: https://documenter.getpostman.com/view/18850457/2s93JzM1Dq

### Key Endpoints

```
GET  /client/list/hostnodes   - List available GPU nodes
POST /client/deploy/single    - Deploy a VM
GET  /client/list/deployed    - List your VMs
DELETE /client/delete/single  - Delete a VM
```

### TensorDock Specifics

```go
type TensorDockClient struct {
    authID     string
    apiToken   string
    httpClient *http.Client
    baseURL    string
}

func (c *TensorDockClient) CreateInstance(ctx context.Context, req CreateInstanceRequest) (*InstanceInfo, error) {
    // TensorDock deploy requires:
    // - gpu_count, gpu_model
    // - ram, vcpus, storage
    // - operating_system (or docker image)
    // - ssh key (injected during provisioning)

    // Note: TensorDock may not support instance labels/tags
    // Use instance name as session ID for reconciliation
}
```

### TensorDock Response Parsing

```go
type TensorDockHostNode struct {
    ID           string  `json:"id"`
    GPUModel     string  `json:"gpu_model"`
    GPUCount     int     `json:"gpu_count"`
    GPUMemory    int     `json:"gpu_memory_gb"`
    PricePerHour float64 `json:"price_per_hour"`
    Location     string  `json:"location"`
    Available    bool    `json:"available"`
}
```

## Critical Implementation Details

### Instance Tagging

For orphan detection to work, instances MUST be identifiable:

```go
// Vast.ai: Use instance label/name
instanceLabel := fmt.Sprintf("shopper-%s", sessionID)

// TensorDock: Use VM name
vmName := fmt.Sprintf("shopper-%s", sessionID)

// When listing instances, filter by our prefix
func (c *Client) ListAllInstances(ctx context.Context) ([]ProviderInstance, error) {
    instances, _ := c.listAllInstances()
    var ours []ProviderInstance
    for _, i := range instances {
        if strings.HasPrefix(i.Name, "shopper-") {
            ours = append(ours, i)
        }
    }
    return ours, nil
}
```

### Rate Limiting

Both providers have rate limits. Implement adaptive backoff:

```go
type RateLimiter struct {
    mu            sync.Mutex
    lastRequest   time.Time
    minInterval   time.Duration
    backoffUntil  time.Time
}

func (r *RateLimiter) Wait(ctx context.Context) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    // Check if we're in backoff
    if time.Now().Before(r.backoffUntil) {
        wait := time.Until(r.backoffUntil)
        select {
        case <-time.After(wait):
        case <-ctx.Done():
            return ctx.Err()
        }
    }

    // Enforce minimum interval
    elapsed := time.Since(r.lastRequest)
    if elapsed < r.minInterval {
        time.Sleep(r.minInterval - elapsed)
    }

    r.lastRequest = time.Now()
    return nil
}

func (r *RateLimiter) Backoff(duration time.Duration) {
    r.mu.Lock()
    r.backoffUntil = time.Now().Add(duration)
    r.mu.Unlock()
}
```

### Error Handling

Define provider-specific errors that map to common errors:

```go
var (
    ErrProviderRateLimit = errors.New("provider rate limit exceeded")
    ErrProviderAuth      = errors.New("provider authentication failed")
    ErrInstanceNotFound  = errors.New("instance not found")
    ErrOfferUnavailable  = errors.New("offer no longer available")
)

func (c *VastAIClient) handleError(resp *http.Response) error {
    switch resp.StatusCode {
    case 429:
        return ErrProviderRateLimit
    case 401, 403:
        return ErrProviderAuth
    case 404:
        return ErrInstanceNotFound
    default:
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("provider error %d: %s", resp.StatusCode, body)
    }
}
```

### Testing with Recorded Responses

Use recorded API responses for unit tests:

```go
func TestVastAIClient_ListOffers(t *testing.T) {
    // Load recorded response
    resp := loadTestData(t, "testdata/vastai_bundles.json")

    // Create mock server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        assert.Equal(t, "/bundles", r.URL.Path)
        w.Write(resp)
    }))
    defer server.Close()

    // Test
    client := NewVastAIClient("test-key", WithBaseURL(server.URL))
    offers, err := client.ListOffers(context.Background(), OfferFilter{})

    require.NoError(t, err)
    assert.Len(t, offers, 10)
    assert.Equal(t, "RTX 4090", offers[0].GPUType)
}
```

## Environment Variables

```bash
# Vast.ai
VASTAI_API_KEY=your-api-key

# TensorDock
TENSORDOCK_AUTH_ID=your-auth-id
TENSORDOCK_API_TOKEN=your-api-token
```

## Workflow

1. Check PROGRESS.md for current tasks
2. Read provider API documentation
3. Implement adapter following the Provider interface
4. Write tests with recorded responses
5. Handle rate limiting and errors gracefully
6. Update PROGRESS.md when complete

## Commit Format

```
[Provider] Brief description

- Detail 1
- Detail 2

Phase: X | Progress: Y/Z items complete
```
