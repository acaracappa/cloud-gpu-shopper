// Package tensordock implements the provider.Provider interface for TensorDock.
//
// # TensorDock API Overview
//
// TensorDock provides GPU virtual machines through their Core Compute platform.
// The API uses JSON:API-style request/response formats with Bearer token authentication.
//
// # Authentication
//
// TensorDock uses two authentication methods depending on the endpoint:
//   - /locations: Query parameters (api_key, api_token)
//   - /instances: Bearer token in Authorization header
//
// # Instance Lifecycle
//
// 1. CreateInstance: Returns instance ID immediately, but NO IP address
// 2. Poll GetInstanceStatus: Wait for IP address to be assigned (~5-30 seconds)
// 3. Wait for cloud-init: SSH key installation takes ~60-90 seconds after boot
// 4. SSH verification: Connect to verify instance is ready
//
// # Key Behaviors Discovered Through Testing
//
//   - Port Forwarding Required: Ubuntu VMs MUST have SSH port 22 forwarded.
//     The API returns error "SSH port (22) must be forwarded for Ubuntu VMs"
//     if port_forwards is omitted.
//
//   - Dynamic External Ports: TensorDock assigns random external ports.
//     You request internal:22 -> external:22, but may receive external:20456.
//     Always read the actual port from GetInstanceStatus.portForwards.
//
//   - SSH Key Installation: The ssh_key API field is required but doesn't
//     actually install the key. The ssh_authorized_keys cloud-init field
//     also doesn't work reliably. Use cloud-init runcmd with base64 encoding:
//     echo '<base64-key>' | base64 -d >> /root/.ssh/authorized_keys
//
//   - Stale Inventory: TensorDock's /locations endpoint often shows GPUs
//     as available when they're not, leading to "No available nodes found"
//     errors during provisioning. We set AvailabilityConfidence to 50%.
//
//   - API Response Quirks: GET /instances returns data as an array directly,
//     while POST /instances wraps response in {"data": {...}}.
package tensordock

import (
	"context"
	"encoding/base64"
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
	// defaultBaseURL is the TensorDock API v2 endpoint
	defaultBaseURL = "https://dashboard.tensordock.com/api/v2"

	// defaultTimeout for HTTP requests
	defaultTimeout = 30 * time.Second

	// defaultImageName is Ubuntu 24.04 - the most common choice for GPU workloads
	defaultImageName = "ubuntu2404"

	// TensorDockAvailabilityConfidence indicates how reliable TensorDock inventory is.
	// Set to 50% because TensorDock's /locations endpoint frequently shows GPUs
	// as available when provisioning will fail with "No available nodes found".
	// This helps users understand these offers may not actually be available.
	TensorDockAvailabilityConfidence = 0.5

	// defaultVCPUs is the default number of vCPUs for new instances
	defaultVCPUs = 8

	// defaultRAMGB is the default RAM in GB for new instances
	defaultRAMGB = 32

	// defaultStorageGB is the minimum storage required by TensorDock
	defaultStorageGB = 100
)

// Client implements the provider.Provider interface for TensorDock.
// It handles authentication, rate limiting, and API communication.
type Client struct {
	// Authentication credentials
	apiKey   string // Also called "Authorization ID" in TensorDock dashboard
	apiToken string // API Token from TensorDock dashboard

	// Configuration
	baseURL      string
	httpClient   *http.Client
	defaultImage string

	// Rate limiting to avoid 429 errors
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration

	// Debug mode for troubleshooting API issues
	debugEnabled bool
}

// ClientOption configures the TensorDock client
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL (useful for testing with mock servers)
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPClient sets a custom HTTP client (useful for testing or custom timeouts)
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// WithMinInterval sets the minimum interval between API requests.
// TensorDock has rate limits; default is 1 second between requests.
func WithMinInterval(d time.Duration) ClientOption {
	return func(c *Client) {
		c.minInterval = d
	}
}

// WithDefaultImage sets the default OS image for new instances.
// Available images: ubuntu2404, ubuntu2204, ubuntu2004, debian12, etc.
func WithDefaultImage(image string) ClientOption {
	return func(c *Client) {
		if image != "" {
			c.defaultImage = image
		}
	}
}

// WithDebug enables debug logging for API requests and responses.
// Logs are prefixed with "[TensorDock DEBUG]" and include:
// - Request URLs and bodies
// - Response status codes and bodies
// Useful for troubleshooting API issues or understanding behavior.
func WithDebug(enabled bool) ClientOption {
	return func(c *Client) {
		c.debugEnabled = enabled
	}
}

// debugLog logs a message if debug mode is enabled
func (c *Client) debugLog(format string, args ...interface{}) {
	if c.debugEnabled {
		log.Printf("[TensorDock DEBUG] "+format, args...)
	}
}

// NewClient creates a new TensorDock API client.
//
// Parameters:
//   - apiKey: Your TensorDock Authorization ID (from dashboard)
//   - apiToken: Your TensorDock API Token (from dashboard)
//   - opts: Optional configuration functions
//
// Example:
//
//	client := tensordock.NewClient(
//	    os.Getenv("TENSORDOCK_AUTH_ID"),
//	    os.Getenv("TENSORDOCK_API_TOKEN"),
//	    tensordock.WithDebug(true),
//	)
func NewClient(apiKey, apiToken string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:       apiKey,
		apiToken:     apiToken,
		baseURL:      defaultBaseURL,
		httpClient:   &http.Client{Timeout: defaultTimeout},
		defaultImage: defaultImageName,
		minInterval:  time.Second, // Conservative rate limit
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Name returns the provider identifier used in offer IDs and session records
func (c *Client) Name() string {
	return "tensordock"
}

// SupportsFeature checks if the provider supports a specific feature
func (c *Client) SupportsFeature(feature provider.ProviderFeature) bool {
	switch feature {
	case provider.FeatureCustomImages:
		return true // TensorDock supports selecting from predefined OS images
	default:
		return false
	}
}

// ListOffers fetches available GPU configurations from TensorDock.
//
// TensorDock's /locations endpoint returns all data centers and their available
// GPU types. Each location+GPU combination becomes a separate offer.
//
// Note: The inventory data is frequently stale. GPUs shown as available may
// fail to provision with "No available nodes found". We set AvailabilityConfidence
// to 50% to reflect this uncertainty.
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	c.rateLimit()

	// /locations uses query parameter authentication
	reqURL := c.buildURLWithQueryAuth("/locations")

	c.debugLog("ListOffers request URL: %s", reqURL)

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

	// Convert each location+GPU combination to a GPUOffer
	offers := make([]models.GPUOffer, 0)
	for _, location := range result.Data.Locations {
		for _, gpu := range location.GPUs {
			offer := locationGPUToOffer(location, gpu)
			if offer.MatchesFilter(filter) {
				offers = append(offers, offer)
			}
		}
	}

	c.debugLog("ListOffers found %d offers matching filter", len(offers))

	return offers, nil
}

// ListAllInstances returns all instances that belong to this shopper deployment.
//
// Used for reconciliation to detect orphaned instances. Only returns instances
// whose names match the shopper label format (prefix "shopper-").
//
// Note: TensorDock's /instances endpoint returns an array directly in the "data"
// field, not wrapped in {"instances": [...]}.
func (c *Client) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	c.rateLimit()

	reqURL := c.buildURL("/instances")

	c.debugLog("ListAllInstances request URL: %s", reqURL)

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
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, c.handleError(resp, "ListAllInstances")
		}
		// Return empty list for other errors (e.g., no instances)
		return []provider.ProviderInstance{}, nil
	}

	// Read and log the response body for debugging
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	c.debugLog("ListAllInstances response: %s", string(respBody))

	// TensorDock's API is inconsistent with response format.
	// It may return either:
	// - Array format: {"data": [...]}
	// - Nested format: {"data": {"instances": [...]}}
	//
	// We need to detect which format by checking if "data" is an array.
	// We do this by attempting to unmarshal into each format.

	// First, try the array format: {"data": [...]}
	// This handles both populated arrays and empty arrays
	var arrayResult struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &arrayResult); err == nil {
		// Check if data starts with '[' (array) or '{' (object)
		trimmed := string(arrayResult.Data)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// It's an array - parse it as such
			var instances []Instance
			if err := json.Unmarshal(arrayResult.Data, &instances); err != nil {
				return nil, fmt.Errorf("failed to decode instances array: %w", err)
			}
			c.debugLog("ListAllInstances: parsed %d instances from array format", len(instances))
			return c.instancesToProviderInstances(instances), nil
		}
	}

	// Fall back to the nested format: {"data": {"instances": [...]}}
	var nestedResult InstancesResponse
	if err := json.Unmarshal(respBody, &nestedResult); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.debugLog("ListAllInstances: parsed %d instances from nested format", len(nestedResult.Data.Instances))
	return c.instancesToProviderInstances(nestedResult.Data.Instances), nil
}

// instancesToProviderInstances converts TensorDock instances to provider instances
func (c *Client) instancesToProviderInstances(instances []Instance) []provider.ProviderInstance {
	result := make([]provider.ProviderInstance, 0)
	for _, inst := range instances {
		// Only include instances with our shopper label prefix
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
		}
	}
	return result
}

// CreateInstance provisions a new GPU virtual machine on TensorDock.
//
// # Offer ID Format
//
// The offer ID must be in format: "tensordock-{locationUUID}-{gpuName}"
// Example: "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb"
//
// # Instance Configuration
//
// Default resources (can be customized in future versions):
//   - 8 vCPUs
//   - 32 GB RAM
//   - 100 GB storage (TensorDock minimum)
//   - 1 GPU of the specified type
//
// # SSH Key Installation
//
// TensorDock requires the ssh_key field but doesn't actually use it to install keys.
// We use cloud-init runcmd with base64 encoding for reliable key installation:
//
//	echo '<base64-encoded-key>' | base64 -d >> /root/.ssh/authorized_keys
//
// # Port Forwarding
//
// TensorDock REQUIRES SSH port forwarding for Ubuntu VMs. We request internal:22
// but TensorDock may assign a different external port (e.g., 20456). Always
// check GetInstanceStatus to get the actual assigned port.
//
// # Response
//
// The create response does NOT include the IP address. You must poll
// GetInstanceStatus until SSHHost is populated (typically 5-30 seconds).
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	c.rateLimit()

	// Parse offer ID to extract location UUID and GPU name
	locationID, gpuName, err := parseOfferID(req.OfferID)
	if err != nil {
		return nil, err
	}

	c.debugLog("CreateInstance: locationID=%s, gpuName=%s", locationID, gpuName)

	// Build the create request
	createReq := CreateInstanceRequest{
		Data: CreateInstanceData{
			Type: "virtualmachine",
			Attributes: CreateInstanceAttributes{
				Name:       req.Tags.ToLabel(),
				Type:       "virtualmachine",
				Image:      c.defaultImage,
				LocationID: locationID,
				Resources: ResourcesConfig{
					VCPUCount: defaultVCPUs,
					RAMGb:     defaultRAMGB,
					StorageGb: defaultStorageGB,
					GPUs: map[string]GPUCount{
						gpuName: {Count: 1},
					},
				},
				// REQUIRED: TensorDock rejects Ubuntu VMs without SSH port forwarding
				// Error: "SSH port (22) must be forwarded for Ubuntu VMs"
				// Note: TensorDock may assign a different external port (e.g., 20456)
				PortForwards: []PortForward{
					{Protocol: "tcp", InternalPort: 22, ExternalPort: 22},
				},
			},
		},
	}

	// Configure SSH key installation via cloud-init
	// The ssh_key API field is required but doesn't work, so we use runcmd
	if req.SSHPublicKey != "" {
		createReq.Data.Attributes.SSHKey = req.SSHPublicKey
		createReq.Data.Attributes.CloudInit = buildSSHKeyCloudInit(req.SSHPublicKey)
	}

	reqURL := c.buildURL("/instances")

	body, err := json.Marshal(createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	c.debugLog("CreateInstance request URL: %s", reqURL)
	c.debugLog("CreateInstance request body: %s", string(body))

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

	c.debugLog("CreateInstance response status: %d", resp.StatusCode)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	c.debugLog("CreateInstance response body: %s", string(respBody))

	// Handle non-success status codes
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, c.parseCreateError(respBody, resp.StatusCode)
	}

	// TensorDock sometimes returns HTTP 200 with an error in the body
	if err := c.checkBodyForError(respBody); err != nil {
		return nil, err
	}

	// Parse successful response
	var result CreateInstanceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(respBody))
	}

	if result.Data.ID == "" {
		return nil, fmt.Errorf("TensorDock returned empty instance ID (body: %s)", string(respBody))
	}

	log.Printf("[TensorDock] Instance created: ID=%s, Name=%s", result.Data.ID, result.Data.Name)

	// Note: Create response does NOT include IP address
	// Caller must poll GetInstanceStatus to get SSH connection details
	return &provider.InstanceInfo{
		ProviderInstanceID: result.Data.ID,
		SSHHost:            "", // Will be populated by GetInstanceStatus
		SSHPort:            22, // Default, but may change - check GetInstanceStatus
		SSHUser:            "root",
		Status:             result.Data.Status,
	}, nil
}

// DestroyInstance terminates a GPU instance.
//
// TensorDock instances are billed by the minute, so prompt cleanup is important.
// This method is idempotent - calling it on an already-deleted instance returns
// success (HTTP 404 is not treated as an error).
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) error {
	c.rateLimit()

	reqURL := c.buildURL(fmt.Sprintf("/instances/%s", instanceID))

	c.debugLog("DestroyInstance request URL: %s", reqURL)

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

	c.debugLog("DestroyInstance response status: %d", resp.StatusCode)

	// Treat 404 as success (instance already deleted)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return c.handleError(resp, "DestroyInstance")
	}

	log.Printf("[TensorDock] Instance destroyed: %s", instanceID)
	return nil
}

// GetInstanceStatus returns the current status of an instance.
//
// This is the primary method for:
//   - Getting the IP address after instance creation (not available in create response)
//   - Getting the actual SSH port (TensorDock may assign a different external port)
//   - Checking if the instance is running
//
// # Port Forwarding
//
// TensorDock assigns external ports dynamically. If you requested internal:22,
// you might receive external:20456. This method reads the actual port from
// the portForwards array in the response.
//
// # Typical Polling Pattern
//
//	for i := 0; i < 60; i++ {
//	    status, _ := client.GetInstanceStatus(ctx, instanceID)
//	    if status.SSHHost != "" {
//	        // Instance is ready for SSH
//	        break
//	    }
//	    time.Sleep(5 * time.Second)
//	}
func (c *Client) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	c.rateLimit()

	reqURL := c.buildURL(fmt.Sprintf("/instances/%s", instanceID))

	c.debugLog("GetInstanceStatus request URL: %s", reqURL)

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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	c.debugLog("GetInstanceStatus response: %s", string(respBody))

	var result InstanceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Find the actual SSH port from port forwards
	// TensorDock may assign a different external port than requested
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

// buildURLWithQueryAuth builds a URL with API credentials in query parameters.
// Used for the /locations endpoint which requires query param authentication.
func (c *Client) buildURLWithQueryAuth(path string) string {
	u, _ := url.Parse(c.baseURL + path)
	q := u.Query()
	q.Set("api_key", c.apiKey)
	q.Set("api_token", c.apiToken)
	u.RawQuery = q.Encode()
	return u.String()
}

// buildURL builds a URL without query authentication.
// Used for /instances endpoints which use Bearer token in headers.
func (c *Client) buildURL(path string) string {
	return c.baseURL + path
}

// setAuthHeader adds Bearer token authentication header
func (c *Client) setAuthHeader(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
}

// rateLimit enforces minimum interval between API requests
func (c *Client) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.lastRequest)
	if elapsed < c.minInterval {
		time.Sleep(c.minInterval - elapsed)
	}
	c.lastRequest = time.Now()
}

// handleError converts HTTP error responses to provider errors
func (c *Client) handleError(resp *http.Response, operation string) error {
	body, _ := io.ReadAll(resp.Body)
	message := string(body)

	c.debugLog("%s error (HTTP %d): %s", operation, resp.StatusCode, message)

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

// parseOfferID extracts location UUID and GPU name from an offer ID.
// Format: "tensordock-{locationUUID}-{gpuName}"
// Example: "tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb"
func parseOfferID(offerID string) (locationID, gpuName string, err error) {
	prefix := "tensordock-"
	if !strings.HasPrefix(offerID, prefix) {
		return "", "", fmt.Errorf("invalid offer ID format (missing prefix): %s", offerID)
	}

	remainder := strings.TrimPrefix(offerID, prefix)

	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 chars)
	// Need at least 36 (UUID) + 1 (dash) + 1 (GPU name char) = 38 chars
	if len(remainder) < 38 {
		return "", "", fmt.Errorf("invalid offer ID format (too short): %s", offerID)
	}

	locationID = remainder[:36]
	gpuName = remainder[37:] // Skip the dash after UUID

	return locationID, gpuName, nil
}

// buildSSHKeyCloudInit creates cloud-init configuration for SSH key installation.
//
// TensorDock's ssh_key API field doesn't actually install the key, and the
// ssh_authorized_keys cloud-init field is unreliable. The only reliable method
// is using runcmd with base64 encoding to avoid issues with special characters.
func buildSSHKeyCloudInit(publicKey string) *CloudInit {
	encodedKey := base64.StdEncoding.EncodeToString([]byte(publicKey))
	return &CloudInit{
		RunCmd: []string{
			"mkdir -p /root/.ssh",
			"chmod 700 /root/.ssh",
			fmt.Sprintf("echo '%s' | base64 -d >> /root/.ssh/authorized_keys", encodedKey),
			"chmod 600 /root/.ssh/authorized_keys",
			"chown -R root:root /root/.ssh",
		},
	}
}

// parseCreateError extracts error information from a failed create response
func (c *Client) parseCreateError(body []byte, statusCode int) error {
	// Try to parse as JSON array of errors (TensorDock validation errors)
	var validationErrors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Path    []string `json:"path"`
	}
	if err := json.Unmarshal(body, &validationErrors); err == nil && len(validationErrors) > 0 {
		messages := make([]string, len(validationErrors))
		for i, e := range validationErrors {
			messages[i] = e.Message
		}
		return provider.NewProviderError("tensordock", "CreateInstance", statusCode,
			strings.Join(messages, "; "), provider.ErrProviderError)
	}

	return provider.NewProviderError("tensordock", "CreateInstance", statusCode,
		string(body), provider.ErrProviderError)
}

// checkBodyForError checks if a successful HTTP response contains an error in the body
func (c *Client) checkBodyForError(body []byte) error {
	var errResp struct {
		Status int    `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Status >= 400 {
		if isStaleInventoryErrorMessage(errResp.Error) {
			return provider.NewProviderError("tensordock", "CreateInstance", errResp.Status,
				errResp.Error, provider.ErrOfferStaleInventory)
		}
		return fmt.Errorf("TensorDock CreateInstance failed: %s", errResp.Error)
	}
	return nil
}

// locationGPUToOffer converts a TensorDock location+GPU to a unified GPUOffer
func locationGPUToOffer(loc Location, gpu LocationGPU) models.GPUOffer {
	vram := parseVRAMFromName(gpu.DisplayName)
	location := fmt.Sprintf("%s, %s, %s", loc.City, loc.StateProvince, loc.Country)

	return models.GPUOffer{
		ID:                     fmt.Sprintf("tensordock-%s-%s", loc.ID, gpu.V0Name),
		Provider:               "tensordock",
		ProviderID:             loc.ID,
		GPUType:                normalizeGPUName(gpu.DisplayName),
		GPUCount:               gpu.MaxCount,
		VRAM:                   vram,
		PricePerHour:           gpu.PricePerHr,
		Location:               location,
		Reliability:            float64(loc.Tier) / 3.0, // Tier 1-3, normalize to 0-1
		Available:              true,
		MaxDuration:            0, // No maximum duration
		FetchedAt:              time.Now(),
		AvailabilityConfidence: TensorDockAvailabilityConfidence,
	}
}

// isStaleInventoryErrorMessage checks if an error indicates stale inventory data.
// These errors occur when TensorDock's /locations shows availability but
// the actual resources aren't available for provisioning.
func isStaleInventoryErrorMessage(msg string) bool {
	staleIndicators := []string{
		"no available nodes",
		"insufficient capacity",
		"not enough capacity",
		"resource unavailable",
		"out of stock",
	}
	msgLower := strings.ToLower(msg)
	for _, indicator := range staleIndicators {
		if strings.Contains(msgLower, indicator) {
			return true
		}
	}
	return false
}
