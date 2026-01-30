package vastai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	defaultBaseURL = "https://console.vast.ai/api/v0"
	defaultTimeout = 30 * time.Second
)

// Client implements the provider.Provider interface for Vast.ai
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client

	// Rate limiting
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration
}

// ClientOption configures the Vast.ai client
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

// NewClient creates a new Vast.ai client
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:      apiKey,
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
	return "vastai"
}

// SupportsFeature checks if the provider supports a specific feature
func (c *Client) SupportsFeature(feature provider.ProviderFeature) bool {
	switch feature {
	case provider.FeatureInstanceTags:
		return true // Vast.ai supports instance labels
	case provider.FeatureSpotPricing:
		return true // Vast.ai has spot/interruptible pricing
	case provider.FeatureCustomImages:
		return true // Vast.ai supports custom Docker images
	default:
		return false
	}
}

// ListOffers returns available GPU offers from Vast.ai
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	c.rateLimit()

	// Build query - Vast.ai uses JSON query syntax
	query := map[string]interface{}{
		"rentable": map[string]bool{"eq": true},
	}

	if filter.GPUType != "" {
		query["gpu_name"] = map[string]string{"eq": filter.GPUType}
	}
	if filter.MinVRAM > 0 {
		query["gpu_ram"] = map[string]int{"gte": filter.MinVRAM * 1024} // Convert GB to MB
	}
	if filter.MaxPrice > 0 {
		query["dph_total"] = map[string]float64{"lte": filter.MaxPrice}
	}
	if filter.MinReliability > 0 {
		query["reliability2"] = map[string]float64{"gte": filter.MinReliability}
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query: %w", err)
	}

	reqURL := fmt.Sprintf("%s/bundles/?q=%s", c.baseURL, url.QueryEscape(string(queryJSON)))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp, "ListOffers")
	}

	var result BundlesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	offers := make([]models.GPUOffer, 0, len(result.Offers))
	for _, bundle := range result.Offers {
		offer := bundle.ToGPUOffer()
		if offer.MatchesFilter(filter) {
			offers = append(offers, offer)
		}
	}

	return offers, nil
}

// ListAllInstances returns all instances with our tags (for reconciliation)
func (c *Client) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	c.rateLimit()

	reqURL := fmt.Sprintf("%s/instances/", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp, "ListAllInstances")
	}

	var result InstancesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	instances := make([]provider.ProviderInstance, 0)
	for _, inst := range result.Instances {
		// Only include instances with our prefix
		if sessionID, ok := models.ParseLabel(inst.Label); ok {
			instances = append(instances, provider.ProviderInstance{
				ID:        strconv.Itoa(inst.ID),
				Name:      inst.Label,
				Status:    inst.ActualStatus,
				StartedAt: time.Unix(int64(inst.StartDate), 0),
				Tags: models.InstanceTags{
					ShopperSessionID: sessionID,
				},
				PricePerHour: inst.DphTotal,
			})
		}
	}

	return instances, nil
}

// CreateInstance provisions a new GPU instance
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	c.rateLimit()

	// Build the create request
	createReq := CreateInstanceRequest{
		ClientID:  "me",
		Image:     req.DockerImage,
		DiskSpace: 50, // Default disk space in GB
		Label:     req.Tags.ToLabel(),
	}

	// Build onstart script that:
	// 1. Sets up SSH access
	// 2. Downloads and runs the agent binary (if SHOPPER_AGENT_URL env var is set)
	var onStartParts []string

	// Add SSH key with proper permissions
	if req.SSHPublicKey != "" {
		onStartParts = append(onStartParts, fmt.Sprintf("mkdir -p /root/.ssh && chmod 700 /root/.ssh && echo '%s' >> /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", req.SSHPublicKey))
	}

	// Download and run agent if URL provided
	if agentURL := req.EnvVars["SHOPPER_AGENT_URL"]; agentURL != "" {
		// Build env var exports to ensure agent has all needed vars
		shopperURL := req.EnvVars["SHOPPER_URL"]
		sessionID := req.EnvVars["SHOPPER_SESSION_ID"]
		agentToken := req.EnvVars["SHOPPER_AGENT_TOKEN"]
		expiresAt := req.EnvVars["SHOPPER_EXPIRES_AT"]
		deploymentID := req.EnvVars["SHOPPER_DEPLOYMENT_ID"]
		consumerID := req.EnvVars["SHOPPER_CONSUMER_ID"]
		agentPort := req.EnvVars["SHOPPER_AGENT_PORT"]

		agentScript := fmt.Sprintf(`
apt-get update && apt-get install -y curl 2>/dev/null || true
curl -fsSL -L '%s' -o /usr/local/bin/gpu-agent && \
chmod +x /usr/local/bin/gpu-agent && \
SHOPPER_URL='%s' SHOPPER_SESSION_ID='%s' SHOPPER_AGENT_TOKEN='%s' SHOPPER_EXPIRES_AT='%s' SHOPPER_DEPLOYMENT_ID='%s' SHOPPER_CONSUMER_ID='%s' SHOPPER_AGENT_PORT='%s' nohup /usr/local/bin/gpu-agent > /var/log/gpu-agent.log 2>&1 &
`, agentURL, shopperURL, sessionID, agentToken, expiresAt, deploymentID, consumerID, agentPort)
		onStartParts = append(onStartParts, agentScript)
	}

	if len(onStartParts) > 0 {
		createReq.OnStart = strings.Join(onStartParts, " && ")
	}

	// Add environment variables as map (Vast.ai expects object, not string)
	if len(req.EnvVars) > 0 {
		createReq.Env = req.EnvVars
	}

	// Parse offer ID as bundle ID
	// Offer IDs are in format "vastai-{id}" or just "{id}"
	offerID := req.OfferID
	if strings.HasPrefix(offerID, "vastai-") {
		offerID = strings.TrimPrefix(offerID, "vastai-")
	}
	bundleID, err := strconv.Atoi(offerID)
	if err != nil {
		return nil, fmt.Errorf("invalid offer ID: %w", err)
	}

	reqURL := fmt.Sprintf("%s/asks/%d/", c.baseURL, bundleID)

	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "PUT", reqURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
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

	if !result.Success {
		return nil, provider.NewProviderError("vastai", "CreateInstance", 0, result.Error, nil)
	}

	return &provider.InstanceInfo{
		ProviderInstanceID: strconv.Itoa(result.NewContract),
		SSHHost:            "", // Will be populated after instance starts
		SSHPort:            22,
		SSHUser:            "root",
		Status:             "creating",
	}, nil
}

// DestroyInstance tears down a GPU instance
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) error {
	c.rateLimit()

	reqURL := fmt.Sprintf("%s/instances/%s/", c.baseURL, instanceID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

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

	reqURL := fmt.Sprintf("%s/instances/%s/", c.baseURL, instanceID)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
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

	// The individual instance endpoint wraps the response in {"instances": {...}}
	var wrapper struct {
		Instances Instance `json:"instances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	result := wrapper.Instances
	return &provider.InstanceStatus{
		Status:    result.ActualStatus,
		Running:   result.ActualStatus == "running",
		StartedAt: time.Unix(int64(result.StartDate), 0),
		SSHHost:   result.SSHHost,
		SSHPort:   result.SSHPort,
		SSHUser:   "root",
	}, nil
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

	return provider.NewProviderError("vastai", operation, resp.StatusCode, message, baseErr)
}
