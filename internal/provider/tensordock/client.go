package tensordock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	defaultBaseURL = "https://dashboard.tensordock.com/api/v2"
	defaultTimeout = 30 * time.Second
)

// Client implements the provider.Provider interface for TensorDock
type Client struct {
	apiKey     string // Authorization ID
	apiToken   string // API Token
	baseURL    string
	httpClient *http.Client

	// Rate limiting
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration
}

// ClientOption configures the TensorDock client
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL (for testing)
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithMinInterval sets the minimum interval between requests
func WithMinInterval(d time.Duration) ClientOption {
	return func(c *Client) {
		c.minInterval = d
	}
}

// NewClient creates a new TensorDock client
func NewClient(apiKey, apiToken string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:      apiKey,
		apiToken:    apiToken,
		baseURL:     defaultBaseURL,
		httpClient:  &http.Client{Timeout: defaultTimeout},
		minInterval: time.Second, // Default 1 request per second
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Name returns the provider identifier
func (c *Client) Name() string {
	return "tensordock"
}

// SupportsFeature checks if the provider supports a specific feature
func (c *Client) SupportsFeature(feature provider.ProviderFeature) bool {
	switch feature {
	case provider.FeatureCustomImages:
		return true // TensorDock supports custom images
	default:
		return false
	}
}

// ListOffers returns available GPU offers from TensorDock
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	c.rateLimit()

	reqURL := c.buildURL("/locations")

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp, "ListOffers")
	}

	var result LocationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	offers := make([]models.GPUOffer, 0)
	for _, location := range result.Data.Locations {
		for _, gpu := range location.GPUs {
			offer := locationGPUToOffer(location, gpu)
			if offer.MatchesFilter(filter) {
				offers = append(offers, offer)
			}
		}
	}

	return offers, nil
}

// ListAllInstances returns all instances with our tags (for reconciliation)
func (c *Client) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	c.rateLimit()

	reqURL := c.buildURL("/instances")

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// TensorDock may return empty or error for no instances
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, c.handleError(resp, "ListAllInstances")
		}
		// Return empty list for other errors
		return []provider.ProviderInstance{}, nil
	}

	var result InstancesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	instances := make([]provider.ProviderInstance, 0)
	for _, inst := range result.Data.Instances {
		// Only include instances with our prefix
		if sessionID, ok := models.ParseLabel(inst.Name); ok {
			instances = append(instances, provider.ProviderInstance{
				ID:        inst.ID,
				Name:      inst.Name,
				Status:    inst.Status,
				StartedAt: inst.CreatedAt,
				Tags: models.InstanceTags{
					ShopperSessionID: sessionID,
				},
				PricePerHour: inst.PricePerHour,
			})
		}
	}

	return instances, nil
}

// CreateInstance provisions a new GPU instance
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	c.rateLimit()

	// Parse offer ID to get location and GPU info
	// Format: "tensordock-{locationID}-{gpuName}"
	parts := strings.SplitN(req.OfferID, "-", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid offer ID format: %s", req.OfferID)
	}
	locationID := parts[1]
	gpuName := parts[2]

	createReq := CreateInstanceRequest{
		Data: CreateInstanceData{
			Type: "virtualmachine",
			Attributes: CreateInstanceAttributes{
				Name:       req.Tags.ToLabel(),
				Image:      "ubuntu2404", // Default image, can be customized
				LocationID: locationID,
				Resources: ResourcesConfig{
					VCPUCount: 8,         // Default vCPUs
					RAMGb:     32,        // Default RAM
					StorageGb: 100,       // Minimum required
					GPUs: GPUsConfig{
						Model: gpuName,
						Count: 1,
					},
				},
			},
		},
	}

	// Add SSH key if provided
	if req.SSHPublicKey != "" {
		createReq.Data.Attributes.SSHKey = req.SSHPublicKey
	}

	reqURL := c.buildURL("/instances")

	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, c.handleError(resp, "CreateInstance")
	}

	var result CreateInstanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &provider.InstanceInfo{
		ProviderInstanceID: result.Data.ID,
		SSHHost:            result.Data.Attributes.IPAddress,
		SSHPort:            22,
		SSHUser:            "root",
		Status:             result.Data.Attributes.Status,
	}, nil
}

// DestroyInstance tears down a GPU instance
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) error {
	c.rateLimit()

	reqURL := c.buildURL(fmt.Sprintf("/instances/%s", instanceID))

	req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.handleError(resp, "DestroyInstance")
	}

	return nil
}

// GetInstanceStatus returns current status of an instance
func (c *Client) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	c.rateLimit()

	reqURL := c.buildURL(fmt.Sprintf("/instances/%s", instanceID))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, provider.ErrInstanceNotFound
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp, "GetInstanceStatus")
	}

	var result InstanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &provider.InstanceStatus{
		Status:    result.Data.Attributes.Status,
		Running:   result.Data.Attributes.Status == "running",
		StartedAt: result.Data.Attributes.CreatedAt,
	}, nil
}

// buildURL builds a URL with authentication query params
func (c *Client) buildURL(path string) string {
	u, _ := url.Parse(c.baseURL + path)
	q := u.Query()
	q.Set("api_key", c.apiKey)
	q.Set("api_token", c.apiToken)
	u.RawQuery = q.Encode()
	return u.String()
}

// rateLimit enforces minimum interval between requests
func (c *Client) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.lastRequest)
	if elapsed < c.minInterval {
		time.Sleep(c.minInterval - elapsed)
	}
	c.lastRequest = time.Now()
}

// handleError converts HTTP errors to provider errors
func (c *Client) handleError(resp *http.Response, operation string) error {
	body, _ := io.ReadAll(resp.Body)
	message := string(body)

	var baseErr error
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

	return provider.NewProviderError("tensordock", operation, resp.StatusCode, message, baseErr)
}

// locationGPUToOffer converts a TensorDock location+GPU to a unified GPUOffer
func locationGPUToOffer(loc Location, gpu LocationGPU) models.GPUOffer {
	// Parse VRAM from display name (e.g., "NVIDIA GeForce RTX 4090 PCIe 24GB")
	vram := parseVRAMFromName(gpu.DisplayName)

	// Build location string
	location := fmt.Sprintf("%s, %s, %s", loc.City, loc.StateProvince, loc.Country)

	return models.GPUOffer{
		ID:           fmt.Sprintf("tensordock-%s-%s", loc.ID, gpu.V0Name),
		Provider:     "tensordock",
		ProviderID:   loc.ID,
		GPUType:      normalizeGPUName(gpu.DisplayName),
		GPUCount:     gpu.MaxCount,
		VRAM:         vram,
		PricePerHour: gpu.PricePerHr,
		Location:     location,
		Reliability:  float64(loc.Tier) / 3.0, // Tier 1-3, normalize to 0-1
		Available:    true,
		MaxDuration:  0,
		FetchedAt:    time.Now(),
	}
}
