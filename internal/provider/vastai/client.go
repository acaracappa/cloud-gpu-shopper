package vastai

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
)

const (
	defaultBaseURL = "https://console.vast.ai/api/v0"
	defaultTimeout = 30 * time.Second
)

// Bug #48: Circuit breaker configuration for Vast.ai
// CircuitBreakerState represents the current state of the circuit breaker
type CircuitBreakerState int

const (
	// CircuitClosed is the normal operating state - requests are allowed
	CircuitClosed CircuitBreakerState = iota
	// CircuitOpen means too many failures occurred - requests are blocked
	CircuitOpen
	// CircuitHalfOpen allows a test request through to check if service recovered
	CircuitHalfOpen
)

// CircuitBreakerConfig configures the circuit breaker behavior
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit
	FailureThreshold int
	// ResetTimeout is how long to wait before transitioning from Open to HalfOpen
	ResetTimeout time.Duration
	// MaxBackoff is the maximum backoff duration for exponential backoff
	MaxBackoff time.Duration
	// BaseBackoff is the initial backoff duration
	BaseBackoff time.Duration
}

// DefaultCircuitBreakerConfig returns sensible defaults for the circuit breaker
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
	consecutiveWaits int // For exponential backoff
}

// newCircuitBreaker creates a new circuit breaker with the given configuration
func newCircuitBreaker(config CircuitBreakerConfig) *circuitBreaker {
	return &circuitBreaker{
		state:  CircuitClosed,
		config: config,
	}
}

// allow returns true if a request should be allowed, false if circuit is open
func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastStateChange) > cb.config.ResetTimeout {
			cb.state = CircuitHalfOpen
			cb.lastStateChange = time.Now()
			return true
		}
		return false
	case CircuitHalfOpen:
		// Allow one test request
		return true
	default:
		return true
	}
}

// recordSuccess records a successful request
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

// recordFailure records a failed request and potentially opens the circuit
func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	if cb.state == CircuitHalfOpen {
		// Failed while testing - go back to open
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

// getBackoff returns the current backoff duration using exponential backoff
func (cb *circuitBreaker) getBackoff() time.Duration {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.consecutiveWaits == 0 {
		return cb.config.BaseBackoff
	}

	// Cap consecutiveWaits to prevent integer overflow in bit shift
	waits := cb.consecutiveWaits
	const maxShift = 10
	if waits > maxShift {
		waits = maxShift
	}

	// Exponential backoff: base * 2^(waits-1), capped at maxBackoff
	backoff := cb.config.BaseBackoff * time.Duration(1<<uint(waits-1))
	if backoff > cb.config.MaxBackoff {
		backoff = cb.config.MaxBackoff
	}
	return backoff
}

// State returns the current circuit breaker state (for monitoring/testing)
func (cb *circuitBreaker) State() CircuitBreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// ErrCircuitOpen is returned when the circuit breaker is open
var ErrCircuitOpen = errors.New("circuit breaker is open")

// templateCacheTTL is how long templates are cached before refetching.
// Templates change infrequently, so use a longer TTL than inventory.
const templateCacheTTL = 1 * time.Hour

// templateCache holds cached templates
type templateCache struct {
	templates []models.VastTemplate
	fetchedAt time.Time
	mu        sync.RWMutex
}

// bundleCache holds cached bundles for template compatibility matching
type bundleCache struct {
	bundles   map[int]Bundle // keyed by bundle ID
	fetchedAt time.Time
	mu        sync.RWMutex
}

// Client implements the provider.Provider interface for Vast.ai
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client

	// Rate limiting
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration

	// Bug #48: Circuit breaker for API calls
	circuitBreaker *circuitBreaker

	// Template cache
	templates *templateCache

	// Bundle cache for template compatibility matching
	bundles *bundleCache
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

// WithCircuitBreaker configures the circuit breaker for API calls (Bug #48)
func WithCircuitBreaker(config CircuitBreakerConfig) ClientOption {
	return func(c *Client) {
		c.circuitBreaker = newCircuitBreaker(config)
	}
}

// NewClient creates a new Vast.ai client
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:         apiKey,
		baseURL:        defaultBaseURL,
		httpClient:     &http.Client{Timeout: defaultTimeout},
		minInterval:    time.Second,                                      // Default 1 request per second
		circuitBreaker: newCircuitBreaker(DefaultCircuitBreakerConfig()), // Bug #48
		templates:      &templateCache{},
		bundles:        &bundleCache{bundles: make(map[int]Bundle)},
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
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) (offers []models.GPUOffer, err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("ListOffers", startTime, err)
		return nil, err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("ListOffers", startTime, err)
	}()

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

	// Cache bundles for template compatibility matching
	c.bundles.mu.Lock()
	c.bundles.bundles = make(map[int]Bundle)
	for _, bundle := range result.Offers {
		c.bundles.bundles[bundle.ID] = bundle
	}
	c.bundles.fetchedAt = time.Now()
	c.bundles.mu.Unlock()

	offers = make([]models.GPUOffer, 0, len(result.Offers))
	for _, bundle := range result.Offers {
		offer := bundle.ToGPUOffer()
		if offer.MatchesFilter(filter) {
			offers = append(offers, offer)
		}
	}

	return offers, nil
}

// ListAllInstances returns all instances with our tags (for reconciliation)
func (c *Client) ListAllInstances(ctx context.Context) (instances []provider.ProviderInstance, err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("ListAllInstances", startTime, err)
		return nil, err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("ListAllInstances", startTime, err)
	}()

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

	instances = make([]provider.ProviderInstance, 0)
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
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (info *provider.InstanceInfo, err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("CreateInstance", startTime, err)
		return nil, err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("CreateInstance", startTime, err)
	}()

	c.rateLimit()

	// Build the create request based on launch mode
	createReq := c.buildCreateRequest(req)

	// Parse offer ID as bundle ID
	// Offer IDs are in format "vastai-{id}" or just "{id}"
	// Bug #60: Make prefix check case-insensitive and trim whitespace
	offerID := strings.TrimSpace(req.OfferID)
	if strings.HasPrefix(strings.ToLower(offerID), "vastai-") {
		offerID = offerID[7:] // Remove "vastai-" prefix (7 chars)
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

	instanceID := strconv.Itoa(result.NewContract)

	// Attach SSH key via separate API endpoint
	// LEARNING FROM LIVE TESTING: The ssh_key parameter in the create request doesn't
	// reliably register the key. We must call the dedicated SSH key attachment endpoint.
	//
	// Timing: After calling AttachSSHKey, the key takes approximately 10-15 seconds to
	// propagate to the instance. The SSH verification polling handles this automatically.
	if req.SSHPublicKey != "" {
		if err := c.AttachSSHKey(ctx, instanceID, req.SSHPublicKey); err != nil {
			// Log but don't fail - the instance is already created
			// The SSH verification will fail later if the key wasn't attached
			log.Printf("[Vast.ai] WARNING: failed to attach SSH key to instance %s: %v (SSH verification will fail)", instanceID, err)
		}
	}

	// Build instance info based on launch mode
	info = &provider.InstanceInfo{
		ProviderInstanceID: instanceID,
		SSHHost:            "", // Will be populated after instance starts
		SSHPort:            22,
		SSHUser:            "root",
		Status:             "creating",
	}

	// Set API port info for entrypoint mode
	if req.LaunchMode == provider.LaunchModeEntrypoint && len(req.ExposedPorts) > 0 {
		info.APIPort = req.ExposedPorts[0]
		info.APIPorts = make(map[int]int)
		for _, port := range req.ExposedPorts {
			info.APIPorts[port] = port // Actual mapping will be updated after instance starts
		}
	}

	return info, nil
}

// buildCreateRequest builds the Vast.ai create instance request based on launch mode
func (c *Client) buildCreateRequest(req provider.CreateInstanceRequest) CreateInstanceRequest {
	// Determine launch mode (default to SSH for backward compatibility)
	launchMode := req.LaunchMode
	if launchMode == "" {
		launchMode = provider.LaunchModeSSH
	}

	// Default disk space to 50GB if not specified
	diskSpace := 50
	if req.DiskGB > 0 {
		diskSpace = req.DiskGB
	}

	createReq := CreateInstanceRequest{
		ClientID:  "me",
		DiskSpace: diskSpace,
		Label:     req.Tags.ToLabel(),
	}

	// Template-based provisioning: if template_hash_id is provided, use it
	// Request params override template defaults. Env vars are merged (request wins conflicts).
	if req.TemplateHashID != "" {
		createReq.TemplateHashID = req.TemplateHashID
		// CRITICAL: Override runtype to ensure SSH access regardless of template settings
		// Templates may have RunType="jupyter" or "args" which don't provide SSH access
		createReq.RunType = "ssh_proxy"
		createReq.SSHKey = req.SSHPublicKey
		log.Printf("[Vast.ai] Creating instance with template %s, disk: %d GB, runtype: ssh_proxy", req.TemplateHashID, createReq.DiskSpace)
		// Add any additional env vars from the request (they'll be merged with template)
		if len(req.EnvVars) > 0 {
			createReq.Env = req.EnvVars
		}
		// Add on-start command if specified (overrides template)
		if req.OnStartCmd != "" {
			createReq.OnStart = req.OnStartCmd
		}
		log.Printf("[Vast.ai] Using template hash_id: %s", req.TemplateHashID)
		return createReq
	}

	// No template - build config manually based on launch mode
	switch launchMode {
	case provider.LaunchModeEntrypoint:
		createReq = c.buildEntrypointRequest(createReq, req)
	default: // LaunchModeSSH
		createReq = c.buildSSHRequest(createReq, req)
	}

	return createReq
}

// buildSSHRequest builds a request for SSH mode (interactive access)
func (c *Client) buildSSHRequest(createReq CreateInstanceRequest, req provider.CreateInstanceRequest) CreateInstanceRequest {
	// Use default SSH image if not specified
	image := req.DockerImage
	if image == "" {
		image = ImageSSHBase
	}

	createReq.Image = image
	createReq.RunType = "ssh_proxy" // Use SSH proxy mode (works on all machines)
	createReq.SSHKey = req.SSHPublicKey

	// Add environment variables if specified
	if len(req.EnvVars) > 0 {
		createReq.Env = req.EnvVars
	}

	// Add on-start command if specified
	if req.OnStartCmd != "" {
		createReq.OnStart = req.OnStartCmd
	}

	return createReq
}

// buildEntrypointRequest builds a request for entrypoint mode (workload execution)
// Uses Vast.ai template approach with environment variables (VLLM_MODEL, VLLM_ARGS)
func (c *Client) buildEntrypointRequest(createReq CreateInstanceRequest, req provider.CreateInstanceRequest) CreateInstanceRequest {
	// Determine image based on workload config or explicit setting
	image := req.DockerImage
	if image == "" && req.WorkloadConfig != nil {
		image = GetImageForWorkload(req.WorkloadConfig.Type)
	}
	if image == "" {
		image = ImageSSHBase // Fallback
	}

	createReq.Image = image

	// Use ssh_proxy mode to get SSH access alongside the workload
	// This allows debugging and monitoring via SSH while running the workload
	createReq.RunType = "ssh_proxy"

	// Initialize env vars map
	if createReq.Env == nil {
		createReq.Env = make(map[string]string)
	}

	// Add caller-provided environment variables first
	for k, v := range req.EnvVars {
		createReq.Env[k] = v
	}

	// Build workload-specific configuration using environment variables
	// This is the Vast.ai template approach - uses VLLM_MODEL and VLLM_ARGS
	if req.WorkloadConfig != nil {
		switch req.WorkloadConfig.Type {
		case provider.WorkloadTypeVLLM:
			// Use environment variables for vLLM template
			vllmEnv := BuildVLLMEnvVars(req.WorkloadConfig)
			for k, v := range vllmEnv {
				createReq.Env[k] = v
			}
			log.Printf("[Vast.ai] vLLM template config: VLLM_MODEL=%s", req.WorkloadConfig.ModelID)
		case provider.WorkloadTypeTGI:
			// TGI uses different env vars
			createReq.Env["MODEL_ID"] = req.WorkloadConfig.ModelID
			if req.WorkloadConfig.Quantization != "" {
				createReq.Env["QUANTIZE"] = req.WorkloadConfig.Quantization
			}
		}
	}

	// Configure port exposure
	ports := req.ExposedPorts
	if len(ports) == 0 && req.WorkloadConfig != nil {
		// Use default port for workload type
		defaultPort := GetPortForWorkload(req.WorkloadConfig.Type)
		if defaultPort > 0 {
			ports = []int{defaultPort}
		}
	}
	if len(ports) > 0 {
		createReq.Ports = FormatPortsString(ports)
	}

	// SSH key for debugging access
	if req.SSHPublicKey != "" {
		createReq.SSHKey = req.SSHPublicKey
	}

	return createReq
}

// AttachSSHKey attaches an SSH public key to an instance
func (c *Client) AttachSSHKey(ctx context.Context, instanceID string, sshPublicKey string) (err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("AttachSSHKey", startTime, err)
		return err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("AttachSSHKey", startTime, err)
	}()

	c.rateLimit()

	reqURL := fmt.Sprintf("%s/instances/%s/ssh/", c.baseURL, instanceID)

	body, err := json.Marshal(map[string]string{"ssh_key": sshPublicKey})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.handleError(resp, "AttachSSHKey")
	}

	return nil
}

// DestroyInstance tears down a GPU instance
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) (err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("DestroyInstance", startTime, err)
		return err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("DestroyInstance", startTime, err)
	}()

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
func (c *Client) GetInstanceStatus(ctx context.Context, instanceID string) (status *provider.InstanceStatus, err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("GetInstanceStatus", startTime, err)
		return nil, err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("GetInstanceStatus", startTime, err)
	}()

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
		// Port mappings for HTTP API access (vLLM, TGI, etc.)
		PublicIP: result.PublicIP,
		Ports:    result.ParsePortMappings(),
	}, nil
}

// ListTemplates returns available templates from Vast.ai
// Templates are cached for 15 minutes to reduce API calls.
// All templates are cached, and filtering is applied locally to ensure
// GetTemplate can find any template by hash_id.
func (c *Client) ListTemplates(ctx context.Context, filter models.TemplateFilter) (templates []models.VastTemplate, err error) {
	startTime := time.Now()

	// Bug #48: Check circuit breaker before making request
	if err := c.checkCircuitBreaker(); err != nil {
		c.recordAPIMetrics("ListTemplates", startTime, err)
		return nil, err
	}

	// Record result to circuit breaker and metrics when function returns
	defer func() {
		c.recordAPIResult(err)
		c.recordAPIMetrics("ListTemplates", startTime, err)
	}()

	// Check cache first
	c.templates.mu.RLock()
	cachedTemplates := c.templates.templates
	cachedAt := c.templates.fetchedAt
	c.templates.mu.RUnlock()

	if len(cachedTemplates) > 0 && time.Since(cachedAt) < templateCacheTTL {
		// Return filtered cached templates
		return c.filterTemplates(cachedTemplates, filter), nil
	}

	// Fetch from API - only fetch recommended templates
	c.rateLimit()

	// Only fetch recommended templates from Vast.ai using select_filters
	// These are the curated templates shown in the Vast.ai console
	// Non-recommended templates (2000+) are user-created and not maintained
	selectFilters := url.QueryEscape(`{"recommended":{"eq":true}}`)
	reqURL := fmt.Sprintf("%s/template/?select_filters=%s", c.baseURL, selectFilters)

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
		return nil, c.handleError(resp, "ListTemplates")
	}

	var result TemplatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to models
	allTemplates := make([]models.VastTemplate, 0, len(result.Templates))
	for _, t := range result.Templates {
		allTemplates = append(allTemplates, t.ToModel())
	}

	// Update cache with ALL templates
	c.templates.mu.Lock()
	c.templates.templates = allTemplates
	c.templates.fetchedAt = time.Now()
	c.templates.mu.Unlock()

	// Apply filter and return
	return c.filterTemplates(allTemplates, filter), nil
}

// filterTemplates applies the filter to a list of templates
func (c *Client) filterTemplates(templates []models.VastTemplate, filter models.TemplateFilter) []models.VastTemplate {
	if filter.Name == "" && filter.Image == "" && !filter.Recommended && !filter.UseSSH {
		return templates
	}

	result := make([]models.VastTemplate, 0)
	for _, t := range templates {
		if t.MatchesFilter(filter) {
			result = append(result, t)
		}
	}
	return result
}

// GetTemplate returns a specific template by hash_id.
// Only recommended templates are available; non-recommended templates will return ErrTemplateNotFound.
func (c *Client) GetTemplate(ctx context.Context, hashID string) (*models.VastTemplate, error) {
	// First try to get from cache
	c.templates.mu.RLock()
	cachedTemplates := c.templates.templates
	cachedAt := c.templates.fetchedAt
	c.templates.mu.RUnlock()

	if len(cachedTemplates) > 0 && time.Since(cachedAt) < templateCacheTTL {
		for _, t := range cachedTemplates {
			if t.HashID == hashID {
				return &t, nil
			}
		}
	}

	// Refresh cache and search again
	templates, err := c.ListTemplates(ctx, models.TemplateFilter{})
	if err != nil {
		return nil, err
	}

	for _, t := range templates {
		if t.HashID == hashID {
			return &t, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", provider.ErrTemplateNotFound, hashID)
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

// Bug #48: Circuit breaker helper methods

// checkCircuitBreaker returns an error if the circuit breaker is open
func (c *Client) checkCircuitBreaker() error {
	if !c.circuitBreaker.allow() {
		backoff := c.circuitBreaker.getBackoff()
		log.Printf("[Vast.ai] Circuit breaker is open, backoff: %v", backoff)
		return fmt.Errorf("%w: retry after %v", ErrCircuitOpen, backoff)
	}
	return nil
}

// recordAPIResult records the result of an API call to the circuit breaker
func (c *Client) recordAPIResult(err error) {
	if err == nil {
		c.circuitBreaker.recordSuccess()
		return
	}

	// Only count certain errors as failures for the circuit breaker
	// Don't count validation errors, not found errors, etc.
	var providerErr *provider.ProviderError
	if errors.As(err, &providerErr) {
		// Rate limits and server errors should trigger circuit breaker
		if providerErr.StatusCode >= 500 || providerErr.StatusCode == 429 {
			c.circuitBreaker.recordFailure()
			return
		}
	}

	// Network errors should trigger circuit breaker
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// Don't trigger for context cancellation by caller
		return
	}

	// Other network-level errors
	if strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "network is unreachable") {
		c.circuitBreaker.recordFailure()
	}
}

// recordAPIMetrics records API call metrics including response time and call count
func (c *Client) recordAPIMetrics(operation string, startTime time.Time, err error) {
	duration := time.Since(startTime)
	metrics.RecordProviderAPIResponseTime("vastai", operation, duration)

	status := "success"
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			status = "circuit_open"
		} else {
			status = "error"
		}
	}
	metrics.RecordProviderAPICall("vastai", operation, status)

	// Update circuit breaker state metric
	metrics.UpdateProviderCircuitBreakerState("vastai", int(c.circuitBreaker.State()))
}

// GetCompatibleTemplates returns templates compatible with the given offer ID.
// Compatibility is determined by matching template extra_filters against host properties.
func (c *Client) GetCompatibleTemplates(ctx context.Context, offerID string) ([]models.CompatibleTemplate, error) {
	// Parse the offer ID to get the bundle ID
	bundleID, err := c.parseOfferID(offerID)
	if err != nil {
		return nil, fmt.Errorf("invalid offer ID %q: %w", offerID, err)
	}

	// Get the bundle from cache
	c.bundles.mu.RLock()
	bundle, exists := c.bundles.bundles[bundleID]
	c.bundles.mu.RUnlock()

	if !exists {
		// Bundle not in cache, need to fetch offers to populate cache
		_, err := c.ListOffers(ctx, models.OfferFilter{})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch offers: %w", err)
		}

		// Try again
		c.bundles.mu.RLock()
		bundle, exists = c.bundles.bundles[bundleID]
		c.bundles.mu.RUnlock()

		if !exists {
			return nil, fmt.Errorf("offer not found: %s", offerID)
		}
	}

	// Get all recommended templates
	templates, err := c.ListTemplates(ctx, models.TemplateFilter{Recommended: true})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch templates: %w", err)
	}

	// Get host properties for matching
	hostProps := bundle.ToHostProperties()

	// Find compatible templates
	var compatible []models.CompatibleTemplate
	for _, t := range templates {
		filters, err := t.ParseExtraFilters()
		if err != nil {
			// Skip templates with malformed filters
			log.Printf("[Vast.ai] WARNING: template %q has malformed extra_filters: %v", t.Name, err)
			continue
		}

		// nil or empty filters = compatible with all hosts
		if filters == nil || filters.MatchesHost(hostProps) {
			compatible = append(compatible, models.CompatibleTemplate{
				HashID: t.HashID,
				Name:   t.Name,
				Image:  t.Image,
			})
		}
	}

	return compatible, nil
}

// parseOfferID extracts the bundle ID from an offer ID string
// Offer IDs are in format "vastai-{id}" or just "{id}"
func (c *Client) parseOfferID(offerID string) (int, error) {
	offerID = strings.TrimSpace(offerID)
	if strings.HasPrefix(strings.ToLower(offerID), "vastai-") {
		offerID = offerID[7:] // Remove "vastai-" prefix (7 chars)
	}
	return strconv.Atoi(offerID)
}
