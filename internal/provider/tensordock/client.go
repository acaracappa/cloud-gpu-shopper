package tensordock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	defaultBaseURL   = "https://dashboard.tensordock.com/api/v2"
	defaultTimeout   = 30 * time.Second
	defaultImageName = "ubuntu2404"

	// TensorDockAvailabilityConfidence is the confidence level for TensorDock offers.
	// TensorDock's inventory API often shows GPUs as available when they're not,
	// leading to "No available nodes found" errors during provisioning.
	// This lower confidence helps users understand these offers may fail.
	TensorDockAvailabilityConfidence = 0.5
)

// Client implements the provider.Provider interface for TensorDock
type Client struct {
	apiKey       string // Authorization ID
	apiToken     string // API Token
	baseURL      string
	httpClient   *http.Client
	defaultImage string // Default OS image for instances

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

// WithDefaultImage sets the default OS image for instances
func WithDefaultImage(image string) ClientOption {
	return func(c *Client) {
		if image != "" {
			c.defaultImage = image
		}
	}
}

// NewClient creates a new TensorDock client
func NewClient(apiKey, apiToken string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:       apiKey,
		apiToken:     apiToken,
		baseURL:      defaultBaseURL,
		httpClient:   &http.Client{Timeout: defaultTimeout},
		defaultImage: defaultImageName,
		minInterval:  time.Second, // Default 1 request per second
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

	reqURL := c.buildURLNoAuth("/instances")

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)
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
	// Format: "tensordock-{locationUUID}-{gpuName}"
	// Location UUIDs contain dashes, so we need to find the GPU name at the end
	prefix := "tensordock-"
	if !strings.HasPrefix(req.OfferID, prefix) {
		return nil, fmt.Errorf("invalid offer ID format: %s", req.OfferID)
	}

	// The GPU name is the last segment after the UUID (36 chars + 1 dash)
	remainder := strings.TrimPrefix(req.OfferID, prefix)
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 chars)
	if len(remainder) < 38 { // 36 for UUID + 1 for dash + at least 1 for GPU name
		return nil, fmt.Errorf("invalid offer ID format: %s", req.OfferID)
	}
	locationID := remainder[:36]
	gpuName := remainder[37:] // Skip the dash after UUID

	// TensorDock uses predefined OS images (not Docker images), always use the client default
	// The DockerImage field in CreateInstanceRequest is for Docker-based providers like Vast.ai
	image := c.defaultImage

	createReq := CreateInstanceRequest{
		Data: CreateInstanceData{
			Type: "virtualmachine",
			Attributes: CreateInstanceAttributes{
				Name:       req.Tags.ToLabel(),
				Type:       "virtualmachine",
				Image:      image,
				LocationID: locationID,
				Resources: ResourcesConfig{
					VCPUCount: 8,   // Default vCPUs
					RAMGb:     32,  // Default RAM
					StorageGb: 100, // Minimum required
					GPUs: map[string]GPUCount{
						gpuName: {Count: 1},
					},
				},
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 22}, // SSH required for Ubuntu
				},
			},
		},
	}

	// Build cloud-init configuration
	// Note: TensorDock's ssh_key API field is REQUIRED but doesn't actually install the key
	// We must also add it to cloud-init ssh_authorized_keys for it to actually work
	cloudInit := &CloudInit{
		PackageUpdate: true,
		Packages:      []string{"curl"},
	}

	// Add SSH key in both places:
	// 1. API field (required by TensorDock, but doesn't actually install it)
	// 2. cloud-init (actually installs the key)
	if req.SSHPublicKey != "" {
		createReq.Data.Attributes.SSHKey = req.SSHPublicKey
		cloudInit.SSHAuthorizedKeys = []string{req.SSHPublicKey}
	}

	// Build runcmd to install and run the agent
	if agentURL := req.EnvVars["SHOPPER_AGENT_URL"]; agentURL != "" {
		var runcmds []string

		// Download agent binary
		runcmds = append(runcmds,
			fmt.Sprintf("curl -fsSL -L '%s' -o /usr/local/bin/gpu-agent", agentURL),
			"chmod +x /usr/local/bin/gpu-agent",
		)

		// Run agent with env vars inline (nohup for background)
		runcmds = append(runcmds,
			fmt.Sprintf("SHOPPER_URL='%s' SHOPPER_SESSION_ID='%s' SHOPPER_AGENT_TOKEN='%s' SHOPPER_EXPIRES_AT='%s' SHOPPER_DEPLOYMENT_ID='%s' SHOPPER_CONSUMER_ID='%s' SHOPPER_AGENT_PORT='%s' nohup /usr/local/bin/gpu-agent > /var/log/gpu-agent.log 2>&1 &",
				req.EnvVars["SHOPPER_URL"],
				req.EnvVars["SHOPPER_SESSION_ID"],
				req.EnvVars["SHOPPER_AGENT_TOKEN"],
				req.EnvVars["SHOPPER_EXPIRES_AT"],
				req.EnvVars["SHOPPER_DEPLOYMENT_ID"],
				req.EnvVars["SHOPPER_CONSUMER_ID"],
				req.EnvVars["SHOPPER_AGENT_PORT"]),
		)

		cloudInit.RunCmd = runcmds
	}

	createReq.Data.Attributes.CloudInit = cloudInit

	reqURL := c.buildURLNoAuth("/instances")

	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(httpReq)
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

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// TensorDock sometimes returns HTTP 200 with error in body, check for that
	var errResp struct {
		Status int    `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Status >= 400 {
		// Check for stale inventory errors - these indicate the inventory showed
		// availability but the actual resources weren't available
		if isStaleInventoryErrorMessage(errResp.Error) {
			return nil, provider.NewProviderError("tensordock", "CreateInstance", errResp.Status,
				errResp.Error, provider.ErrOfferStaleInventory)
		}
		return nil, fmt.Errorf("tensordock CreateInstance failed: %s", errResp.Error)
	}

	var result CreateInstanceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(respBody))
	}

	// Validate we got an instance ID
	if result.Data.ID == "" {
		return nil, fmt.Errorf("tensordock CreateInstance returned empty instance ID (body: %s)", string(respBody))
	}

	log.Printf("[DEBUG] TensorDock CreateInstance success: ID=%s", result.Data.ID)

	// Note: Create response doesn't include IP address - must poll GetInstanceStatus for that
	return &provider.InstanceInfo{
		ProviderInstanceID: result.Data.ID,
		SSHHost:            "", // Will be populated by GetInstanceStatus after instance boots
		SSHPort:            22,
		SSHUser:            "root",
		Status:             result.Data.Status,
	}, nil
}

// DestroyInstance tears down a GPU instance
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) error {
	c.rateLimit()

	reqURL := c.buildURLNoAuth(fmt.Sprintf("/instances/%s", instanceID))

	req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)

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

	reqURL := c.buildURLNoAuth(fmt.Sprintf("/instances/%s", instanceID))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)
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

	// Find SSH port from port forwards (default to 22)
	sshPort := 22
	for _, pf := range result.PortForwards {
		if pf.InternalPort == 22 {
			sshPort = pf.ExternalPort
			break
		}
	}

	return &provider.InstanceStatus{
		Status:  result.Status,
		Running: result.Status == "running",
		SSHHost: result.IPAddress,
		SSHPort: sshPort,
		SSHUser: "root",
	}, nil
}

// buildURL builds a URL with authentication query params (for /locations endpoint)
func (c *Client) buildURL(path string) string {
	u, _ := url.Parse(c.baseURL + path)
	q := u.Query()
	q.Set("api_key", c.apiKey)
	q.Set("api_token", c.apiToken)
	u.RawQuery = q.Encode()
	return u.String()
}

// buildURLNoAuth builds a URL without query param auth (for /instances endpoint which uses Bearer auth)
func (c *Client) buildURLNoAuth(path string) string {
	return c.baseURL + path
}

// setAuthHeader adds Bearer token authentication header
func (c *Client) setAuthHeader(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
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
		// TensorDock inventory is often stale - set lower confidence
		AvailabilityConfidence: TensorDockAvailabilityConfidence,
	}
}

// isStaleInventoryErrorMessage checks if an error message indicates stale inventory
// These are errors that occur when TensorDock's inventory shows availability
// but the actual resources are not available for provisioning
func isStaleInventoryErrorMessage(msg string) bool {
	staleIndicators := []string{
		"No available nodes found",
		"no available nodes",
		"insufficient capacity",
		"Insufficient capacity",
		"not enough capacity",
		"resource unavailable",
		"Resource unavailable",
		"out of stock",
		"Out of stock",
	}
	msgLower := strings.ToLower(msg)
	for _, indicator := range staleIndicators {
		if strings.Contains(msgLower, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}
