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
//     also doesn't work reliably. Use cloud-init runcmd to echo the key
//     directly to the authorized_keys file for both root and 'user' accounts.
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
)

const (
	// defaultBaseURL is the TensorDock API v2 endpoint
	defaultBaseURL = "https://dashboard.tensordock.com/api/v2"

	// Default timeouts for different operation types.
	// Different operations may have different latency characteristics:
	//   - ListOffers: Reading inventory is typically fast
	//   - CreateInstance: Creating can take longer due to resource allocation
	//   - GetInstanceStatus: Status checks should be quick
	//   - DestroyInstance: Destruction should be quick but may need confirmation
	defaultTimeoutListOffers     = 30 * time.Second
	defaultTimeoutCreateInstance = 60 * time.Second
	defaultTimeoutGetStatus      = 30 * time.Second
	defaultTimeoutDestroy        = 30 * time.Second
	defaultTimeoutListInstances  = 30 * time.Second

	// defaultImageName is Ubuntu 22.04 - better NVIDIA driver support than 24.04
	// BUG-009: ubuntu2404 is missing NVIDIA drivers and requires manual installation
	defaultImageName = "ubuntu2204"

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

// OperationTimeouts holds configurable timeouts for different API operations.
// Each operation can have its own timeout to account for different latency
// characteristics (e.g., create operations may need more time than status checks).
type OperationTimeouts struct {
	ListOffers     time.Duration
	CreateInstance time.Duration
	GetStatus      time.Duration
	Destroy        time.Duration
	ListInstances  time.Duration
}

// DefaultTimeouts returns the default timeout configuration.
func DefaultTimeouts() OperationTimeouts {
	return OperationTimeouts{
		ListOffers:     defaultTimeoutListOffers,
		CreateInstance: defaultTimeoutCreateInstance,
		GetStatus:      defaultTimeoutGetStatus,
		Destroy:        defaultTimeoutDestroy,
		ListInstances:  defaultTimeoutListInstances,
	}
}

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
	// With max shift of 10, we can shift up to 2^10 = 1024x the base backoff
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

// Client implements the provider.Provider interface for TensorDock.
// It handles authentication, rate limiting, and API communication.
// locationStats tracks provisioning success/failure rates per location.
// This enables dynamic availability confidence based on real-world success rates.
type locationStats struct {
	mu           sync.RWMutex
	attempts     map[string]int // locationID -> total attempts
	successes    map[string]int // locationID -> successful provisions
	lastAttempt  map[string]time.Time
	decayAfter   time.Duration // Reset stats after this duration of inactivity
}

// newLocationStats creates a new location stats tracker
func newLocationStats() *locationStats {
	return &locationStats{
		attempts:    make(map[string]int),
		successes:   make(map[string]int),
		lastAttempt: make(map[string]time.Time),
		decayAfter:  1 * time.Hour, // Reset stats after 1 hour of no attempts
	}
}

// recordAttempt records a provisioning attempt for a location
func (ls *locationStats) recordAttempt(locationID string, success bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	// Check for decay - if last attempt was long ago, reset stats
	if lastTime, ok := ls.lastAttempt[locationID]; ok {
		if time.Since(lastTime) > ls.decayAfter {
			ls.attempts[locationID] = 0
			ls.successes[locationID] = 0
		}
	}

	ls.attempts[locationID]++
	if success {
		ls.successes[locationID]++
	}
	ls.lastAttempt[locationID] = time.Now()
}

// getConfidence returns the success rate for a location.
// Returns a value between minConfidence (0.1) and maxConfidence (1.0).
// If no data is available, returns the default TensorDock confidence.
func (ls *locationStats) getConfidence(locationID string) float64 {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	const minConfidence = 0.1 // Never go below 10%
	const maxConfidence = 1.0

	attempts, hasAttempts := ls.attempts[locationID]
	if !hasAttempts || attempts == 0 {
		return TensorDockAvailabilityConfidence // Default 50%
	}

	successes := ls.successes[locationID]
	rate := float64(successes) / float64(attempts)

	// Clamp to minimum confidence
	if rate < minConfidence {
		return minConfidence
	}
	if rate > maxConfidence {
		return maxConfidence
	}

	return rate
}

// getStats returns human-readable stats for a location
func (ls *locationStats) getStats(locationID string) (attempts, successes int, confidence float64) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	attempts = ls.attempts[locationID]
	successes = ls.successes[locationID]
	confidence = ls.getConfidence(locationID)
	return
}

type Client struct {
	// Authentication credentials
	apiKey   string // Also called "Authorization ID" in TensorDock dashboard
	apiToken string // API Token from TensorDock dashboard

	// Configuration
	baseURL        string
	httpClient     *http.Client
	defaultImage   string
	timeouts       OperationTimeouts
	circuitBreaker *circuitBreaker

	// Rate limiting to avoid 429 errors
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration

	// Debug mode for troubleshooting API issues
	debugEnabled bool

	// Structured logging
	logger *slog.Logger

	// Dynamic availability tracking (stale inventory fix)
	locationStats *locationStats
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

// WithTimeouts sets custom timeouts for different API operations.
// Pass a modified OperationTimeouts struct to customize specific operations.
//
// Example:
//
//	timeouts := tensordock.DefaultTimeouts()
//	timeouts.CreateInstance = 2 * time.Minute
//	client := tensordock.NewClient(key, token, tensordock.WithTimeouts(timeouts))
func WithTimeouts(timeouts OperationTimeouts) ClientOption {
	return func(c *Client) {
		c.timeouts = timeouts
	}
}

// WithCircuitBreaker configures the circuit breaker for API calls.
// The circuit breaker helps prevent cascading failures by temporarily
// blocking requests when the API is experiencing issues.
//
// Example:
//
//	config := tensordock.DefaultCircuitBreakerConfig()
//	config.FailureThreshold = 3
//	client := tensordock.NewClient(key, token, tensordock.WithCircuitBreaker(config))
func WithCircuitBreaker(config CircuitBreakerConfig) ClientOption {
	return func(c *Client) {
		c.circuitBreaker = newCircuitBreaker(config)
	}
}

// WithLogger sets a custom logger for the client.
// If not provided, slog.Default() is used.
//
// Example:
//
//	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
//	client := tensordock.NewClient(key, token, tensordock.WithLogger(logger))
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// debugLog logs a message if debug mode is enabled.
// SECURITY: This function redacts sensitive credentials before logging.
func (c *Client) debugLog(format string, args ...interface{}) {
	if c.debugEnabled {
		// Redact any credentials that might appear in the log message
		message := fmt.Sprintf(format, args...)
		message = redactCredentials(message)
		c.logger.Debug(message, slog.String("provider", "tensordock"))
	}
}

// redactCredentials removes sensitive credentials from log messages.
// This handles both URL query parameters and JSON body fields.
//
// Redacted patterns:
//   - api_key=xxx -> api_key=REDACTED (URL query params)
//   - api_token=xxx -> api_token=REDACTED (URL query params)
//   - "ssh_key":"xxx" -> "ssh_key":"REDACTED" (JSON body)
//   - echo 'base64...' | base64 -d >> (cloud-init SSH key commands)
func redactCredentials(s string) string {
	// Redact URL query parameters
	s = redactQueryParam(s, "api_key")
	s = redactQueryParam(s, "api_token")

	// Redact SSH keys in JSON body (handles "ssh_key":"value")
	s = redactJSONField(s, "ssh_key")

	// Redact base64-encoded SSH keys in cloud-init commands
	// Pattern: echo 'base64data' | base64 -d >>
	s = redactBase64EchoCommand(s)

	return s
}

// redactJSONField replaces the value of a JSON string field with REDACTED.
// Handles: "fieldName":"value" -> "fieldName":"REDACTED"
func redactJSONField(s, fieldName string) string {
	// Match "fieldName":"value" where value is a JSON string
	// The pattern handles escaped quotes within the value
	prefix := `"` + fieldName + `":"`
	var result strings.Builder
	remaining := s

	for {
		idx := strings.Index(remaining, prefix)
		if idx == -1 {
			result.WriteString(remaining)
			break
		}

		// Write everything up to and including the prefix
		result.WriteString(remaining[:idx+len(prefix)])

		// Move past the prefix to find the end of the JSON string value
		remaining = remaining[idx+len(prefix):]

		// Find the closing quote (handling escaped quotes)
		valueEnd := findJSONStringEnd(remaining)
		if valueEnd == -1 {
			// Malformed JSON, just write REDACTED and the rest
			result.WriteString("REDACTED")
			result.WriteString(remaining)
			remaining = ""
		} else {
			result.WriteString("REDACTED")
			remaining = remaining[valueEnd:]
		}
	}

	return result.String()
}

// findJSONStringEnd finds the index of the closing quote of a JSON string,
// handling escaped characters. Returns the index of the closing quote,
// or -1 if not found.
func findJSONStringEnd(s string) int {
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == '"' {
			return i
		}
	}
	return -1
}

// redactBase64EchoCommand redacts base64-encoded data in cloud-init echo commands.
// Pattern: echo 'base64data' | base64 -d
func redactBase64EchoCommand(s string) string {
	// Look for echo 'xxx' | base64 pattern (single quotes)
	prefix := "echo '"
	suffix := "' | base64"
	var result strings.Builder
	remaining := s

	for {
		idx := strings.Index(remaining, prefix)
		if idx == -1 {
			result.WriteString(remaining)
			break
		}

		// Check if this is followed by the base64 pipe
		afterPrefix := remaining[idx+len(prefix):]
		endQuote := strings.Index(afterPrefix, "'")
		if endQuote == -1 {
			// No closing quote, write and continue
			result.WriteString(remaining[:idx+len(prefix)])
			remaining = afterPrefix
			continue
		}

		// Check if it's followed by | base64
		afterQuote := afterPrefix[endQuote:]
		if strings.HasPrefix(afterQuote, suffix) {
			// This is a base64 echo command, redact it
			result.WriteString(remaining[:idx+len(prefix)])
			result.WriteString("REDACTED")
			remaining = afterQuote
		} else {
			// Not a base64 command, keep the original
			result.WriteString(remaining[:idx+len(prefix)])
			remaining = afterPrefix
		}
	}

	return result.String()
}

// redactQueryParam replaces the value of a URL query parameter with REDACTED.
// Handles both middle-of-string (&param=value&) and end-of-string (&param=value) cases.
func redactQueryParam(s, param string) string {
	// Match param=value where value continues until & or end of string
	// This handles: ?api_key=secret&other=val or &api_key=secret&other=val
	prefix := param + "="
	var result strings.Builder
	remaining := s

	for {
		idx := strings.Index(remaining, prefix)
		if idx == -1 {
			// No more occurrences, write the rest
			result.WriteString(remaining)
			break
		}

		// Write everything up to and including the prefix
		result.WriteString(remaining[:idx+len(prefix)])

		// Move past the prefix
		remaining = remaining[idx+len(prefix):]

		// Find the end of the value (next & or end of string)
		valueEnd := strings.Index(remaining, "&")
		if valueEnd == -1 {
			// Value goes to end of string
			result.WriteString("REDACTED")
			remaining = ""
		} else {
			// Value ends at &, skip the value but keep the &
			result.WriteString("REDACTED")
			remaining = remaining[valueEnd:]
		}
	}

	return result.String()
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
		apiKey:         apiKey,
		apiToken:       apiToken,
		baseURL:        defaultBaseURL,
		httpClient:     &http.Client{}, // Timeout set per-operation
		defaultImage:   defaultImageName,
		timeouts:       DefaultTimeouts(),
		circuitBreaker: newCircuitBreaker(DefaultCircuitBreakerConfig()),
		minInterval:    time.Second, // Conservative rate limit
		logger:         slog.Default(),
		locationStats:  newLocationStats(), // Dynamic availability tracking
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
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) (offers []models.GPUOffer, err error) {
	startTime := time.Now()

	// Check circuit breaker before making request
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

	// Apply operation-specific timeout
	ctx, cancel := c.contextWithTimeout(ctx, c.timeouts.ListOffers)
	defer cancel()

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
	// Use dynamic availability confidence based on historical success rates
	offers = make([]models.GPUOffer, 0)
	for _, location := range result.Data.Locations {
		for _, gpu := range location.GPUs {
			offer := locationGPUToOffer(location, gpu)
			// Override confidence with dynamic value from location stats
			offer.AvailabilityConfidence = c.locationStats.getConfidence(location.ID)
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
func (c *Client) ListAllInstances(ctx context.Context) (instances []provider.ProviderInstance, err error) {
	startTime := time.Now()

	// Check circuit breaker before making request
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

	// Apply operation-specific timeout
	ctx, cancel := c.contextWithTimeout(ctx, c.timeouts.ListInstances)
	defer cancel()

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
		// Authentication/authorization errors should be returned as errors,
		// not silently ignored with an empty list
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, c.handleError(resp, "ListAllInstances")
		}
		// Return empty list for other errors (e.g., no instances, 404)
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
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (info *provider.InstanceInfo, err error) {
	startTime := time.Now()

	// Check circuit breaker before making request
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

	// Apply operation-specific timeout (create operations may take longer)
	ctx, cancel := c.contextWithTimeout(ctx, c.timeouts.CreateInstance)
	defer cancel()

	// Parse offer ID to extract location UUID and GPU name
	locationID, gpuName, err := parseOfferID(req.OfferID)
	if err != nil {
		return nil, err
	}

	// Record provisioning attempt for dynamic availability tracking
	// This runs after the function returns, recording success/failure
	defer func() {
		success := err == nil && info != nil && info.ProviderInstanceID != ""
		c.locationStats.recordAttempt(locationID, success)
		if !success && err != nil {
			c.logger.Debug("provisioning attempt recorded",
				slog.String("location_id", locationID),
				slog.Bool("success", false),
				slog.Float64("new_confidence", c.locationStats.getConfidence(locationID)))
		}
	}()

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
				// Request dedicated IP for direct port access (all ports exposed)
				// This is more reliable than port forwarding which was being ignored
				UseDedicatedIP: true,
			},
		},
	}

	// Configure SSH key installation via cloud-init
	// The ssh_key API field is required but doesn't work, so we use runcmd
	if req.SSHPublicKey != "" {
		// Validate SSH public key before using it
		if err := ValidateSSHPublicKey(req.SSHPublicKey); err != nil {
			return nil, fmt.Errorf("SSH key validation failed: %w", err)
		}
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

	c.logger.Info("instance created",
		slog.String("provider", "tensordock"),
		slog.String("instance_id", result.Data.ID),
		slog.String("name", result.Data.Name),
	)

	// Note: Create response does NOT include IP address
	// Caller must poll GetInstanceStatus to get SSH connection details
	return &provider.InstanceInfo{
		ProviderInstanceID: result.Data.ID,
		SSHHost:            "", // Will be populated by GetInstanceStatus
		SSHPort:            22, // Default, but may change - check GetInstanceStatus
		SSHUser:            "user", // TensorDock creates a 'user' account with sudo access
		Status:             result.Data.Status,
	}, nil
}

// DestroyInstance terminates a GPU instance.
//
// TensorDock instances are billed by the minute, so prompt cleanup is important.
// This method is idempotent - calling it on an already-deleted instance returns
// success (HTTP 404 is not treated as an error).
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) (err error) {
	startTime := time.Now()

	// Validate instance ID to prevent path traversal and other attacks
	if err := ValidateInstanceID(instanceID); err != nil {
		return err
	}

	// Check circuit breaker before making request
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

	// Apply operation-specific timeout
	ctx, cancel := c.contextWithTimeout(ctx, c.timeouts.Destroy)
	defer cancel()

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

	c.logger.Info("instance destroyed",
		slog.String("provider", "tensordock"),
		slog.String("instance_id", instanceID),
	)
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
func (c *Client) GetInstanceStatus(ctx context.Context, instanceID string) (status *provider.InstanceStatus, err error) {
	startTime := time.Now()

	// Validate instance ID to prevent path traversal and other attacks
	if err := ValidateInstanceID(instanceID); err != nil {
		return nil, err
	}

	// Check circuit breaker before making request
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

	// Apply operation-specific timeout
	ctx, cancel := c.contextWithTimeout(ctx, c.timeouts.GetStatus)
	defer cancel()

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
	portMappings := make(map[int]int)
	for _, pf := range result.PortForwards {
		// Populate all port mappings (internal -> external)
		portMappings[pf.InternalPort] = pf.ExternalPort
		// Track SSH port specifically
		if pf.InternalPort == 22 {
			sshPort = pf.ExternalPort
		}
	}

	return &provider.InstanceStatus{
		Status:  result.Status,
		Running: result.Status == "running",
		SSHHost: result.IPAddress,
		SSHPort: sshPort,
		SSHUser: "user", // TensorDock creates a 'user' account with sudo access
		// Port mappings for HTTP API access (entrypoint mode workloads)
		PublicIP: result.IPAddress,
		Ports:    portMappings,
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

// contextWithTimeout creates a context with an operation-specific timeout.
// If the parent context already has a deadline earlier than the timeout,
// the parent's deadline is used instead.
func (c *Client) contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	// If parent already has an earlier deadline, don't extend it
	if deadline, ok := parent.Deadline(); ok {
		if time.Until(deadline) < timeout {
			return context.WithCancel(parent)
		}
	}
	return context.WithTimeout(parent, timeout)
}

// checkCircuitBreaker returns an error if the circuit breaker is open.
// If the circuit is open, it also logs the current backoff duration.
func (c *Client) checkCircuitBreaker() error {
	if !c.circuitBreaker.allow() {
		backoff := c.circuitBreaker.getBackoff()
		c.debugLog("Circuit breaker is open, backoff: %v", backoff)
		return fmt.Errorf("%w: retry after %v", ErrCircuitOpen, backoff)
	}
	return nil
}

// recordAPIResult records the result of an API call to the circuit breaker.
// It should be called after each API request completes.
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

// recordAPIMetrics records API call metrics including response time and call count.
// This should be called after each API request completes.
func (c *Client) recordAPIMetrics(operation string, startTime time.Time, err error) {
	duration := time.Since(startTime)
	metrics.RecordProviderAPIResponseTime("tensordock", operation, duration)

	status := "success"
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			status = "circuit_open"
		} else {
			status = "error"
		}
	}
	metrics.RecordProviderAPICall("tensordock", operation, status)

	// Update circuit breaker state metric
	metrics.UpdateProviderCircuitBreakerState("tensordock", int(c.circuitBreaker.State()))
}

// maxErrorMessageLength is the maximum length for error messages.
// Longer messages are truncated to prevent memory issues and log bloat.
const maxErrorMessageLength = 1000

// sanitizeErrorMessage truncates and cleans error messages for safe logging and storage.
// - Truncates to maxErrorMessageLength characters
// - Adds truncation indicator if message was cut
// - Removes potentially sensitive data patterns
func sanitizeErrorMessage(message string) string {
	// Truncate if too long
	if len(message) > maxErrorMessageLength {
		message = message[:maxErrorMessageLength] + "... [truncated]"
	}

	// Clean up common problematic patterns
	// Remove any embedded newlines that could cause log injection
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")

	return message
}

// handleError converts HTTP error responses to provider errors
func (c *Client) handleError(resp *http.Response, operation string) error {
	body, _ := io.ReadAll(resp.Body)
	message := sanitizeErrorMessage(string(body))

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
//
// Bug #61: Improved error messages to help users understand expected format
func parseOfferID(offerID string) (locationID, gpuName string, err error) {
	// Bug #61: Trim whitespace and make prefix check case-insensitive
	offerID = strings.TrimSpace(offerID)
	prefix := "tensordock-"

	// Check if it looks like a bare UUID (common user error)
	if !strings.HasPrefix(strings.ToLower(offerID), prefix) {
		// Check if it's a bare UUID pattern (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
		if len(offerID) == 36 && strings.Count(offerID, "-") == 4 {
			return "", "", fmt.Errorf("invalid offer ID format: '%s' appears to be a bare UUID. "+
				"TensorDock offer IDs must be in format 'tensordock-{locationUUID}-{gpuName}'. "+
				"Use 'gpu-shopper inventory' to get valid offer IDs", offerID)
		}
		return "", "", fmt.Errorf("invalid offer ID format: '%s' is missing required 'tensordock-' prefix. "+
			"Expected format: 'tensordock-{locationUUID}-{gpuName}'. "+
			"Example: 'tensordock-1a779525-4c04-4f2c-aa45-58b47d54bb38-geforcertx3090-pcie-24gb'", offerID)
	}

	// Use case-insensitive prefix removal
	remainder := offerID[len(prefix):]

	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 chars)
	// Need at least 36 (UUID) + 1 (dash) + 1 (GPU name char) = 38 chars
	if len(remainder) < 38 {
		return "", "", fmt.Errorf("invalid offer ID format: '%s' is too short. "+
			"Expected format: 'tensordock-{locationUUID}-{gpuName}'. "+
			"The location UUID should be 36 characters followed by a GPU name", offerID)
	}

	locationID = remainder[:36]
	gpuName = remainder[37:] // Skip the dash after UUID

	return locationID, gpuName, nil
}

// ErrInvalidSSHKey is returned when the provided SSH public key is invalid
var ErrInvalidSSHKey = errors.New("invalid SSH public key")

// ErrInvalidInstanceID is returned when the provided instance ID is invalid
var ErrInvalidInstanceID = errors.New("invalid instance ID")

// maxInstanceIDLength is the maximum allowed length for instance IDs.
// TensorDock uses UUIDs (36 chars) but we allow some extra for safety.
const maxInstanceIDLength = 128

// ValidateInstanceID validates that an instance ID is well-formed.
// Instance IDs must be:
//   - Non-empty
//   - Not exceed maxInstanceIDLength characters
//   - Not contain path traversal characters
//
// Returns nil if valid, or an error describing the validation failure.
func ValidateInstanceID(instanceID string) error {
	if instanceID == "" {
		return fmt.Errorf("%w: empty instance ID", ErrInvalidInstanceID)
	}

	if len(instanceID) > maxInstanceIDLength {
		return fmt.Errorf("%w: instance ID too long (max %d characters)", ErrInvalidInstanceID, maxInstanceIDLength)
	}

	// Check for path traversal attempts
	if strings.Contains(instanceID, "/") || strings.Contains(instanceID, "\\") {
		return fmt.Errorf("%w: instance ID contains invalid characters", ErrInvalidInstanceID)
	}

	// Check for URL-encoded path traversal
	if strings.Contains(instanceID, "%2f") || strings.Contains(instanceID, "%2F") ||
		strings.Contains(instanceID, "%5c") || strings.Contains(instanceID, "%5C") {
		return fmt.Errorf("%w: instance ID contains invalid characters", ErrInvalidInstanceID)
	}

	return nil
}

// ValidateSSHPublicKey validates that a string is a valid SSH public key.
// It uses golang.org/x/crypto/ssh.ParseAuthorizedKey which supports the
// authorized_keys format (e.g., "ssh-rsa AAAA... comment").
//
// Returns nil if valid, or an error describing the validation failure.
func ValidateSSHPublicKey(publicKey string) error {
	if publicKey == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidSSHKey)
	}

	// Trim whitespace that might cause parsing issues
	publicKey = strings.TrimSpace(publicKey)

	// ParseAuthorizedKey parses the authorized_keys format which includes
	// the key type, base64-encoded key data, and optional comment
	_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSSHKey, err)
	}

	return nil
}

// buildSSHKeyCloudInit creates cloud-init configuration for SSH key installation.
//
// TensorDock's ssh_key API field doesn't actually install the key, and the
// ssh_authorized_keys cloud-init field is unreliable. The most reliable method
// is using write_files with base64 encoding.
//
// We install keys for BOTH root and user accounts because:
// - TensorDock's default cloud-init creates a "user" account
// - Some images may use root directly
// - This ensures SSH access regardless of the default user
//
// IMPORTANT: The caller should validate the SSH key using ValidateSSHPublicKey
// before calling this function.
func buildSSHKeyCloudInit(publicKey string) *CloudInit {
	return buildCloudInit(publicKey, true)
}

// buildCloudInit creates cloud-init configuration for SSH key installation and
// optionally NVIDIA driver installation.
//
// BUG-009: TensorDock images may be missing NVIDIA drivers. When installNvidiaDrivers
// is true, we add commands to install the driver if not present.
func buildCloudInit(publicKey string, installNvidiaDrivers bool) *CloudInit {
	// TensorDock's cloud-init write_files may not support encoding field,
	// and runs before runcmd (which creates directories).
	// Use runcmd only for reliable SSH key installation with proper ordering.
	//
	// We install keys for both root and the default 'user' account that
	// TensorDock creates, since the SSH user may vary.
	//
	// Shell-escape the key to handle any special characters.
	escapedKey := shellEscapeSingleQuote(publicKey)

	runCmds := []string{
		// Create directories with proper permissions first
		"mkdir -p /root/.ssh",
		"chmod 700 /root/.ssh",
		// Write SSH key for root
		fmt.Sprintf("echo '%s' > /root/.ssh/authorized_keys", escapedKey),
		"chmod 600 /root/.ssh/authorized_keys",
		"chown root:root /root/.ssh/authorized_keys",
		// Create user's .ssh directory
		"mkdir -p /home/user/.ssh",
		"chmod 700 /home/user/.ssh",
		"chown user:user /home/user/.ssh",
		// Write SSH key for user
		fmt.Sprintf("echo '%s' > /home/user/.ssh/authorized_keys", escapedKey),
		"chmod 600 /home/user/.ssh/authorized_keys",
		"chown user:user /home/user/.ssh/authorized_keys",
	}

	// BUG-009: Install NVIDIA drivers if not present
	// This runs in background to not block SSH access, with a marker file to indicate completion
	if installNvidiaDrivers {
		runCmds = append(runCmds,
			// Check if nvidia-smi exists; if not, install drivers
			// Using nohup and background execution to not block cloud-init completion
			// The installation may require a reboot, but we warn about this in logs
			"if ! command -v nvidia-smi &> /dev/null; then "+
				"echo 'NVIDIA driver not found, installing nvidia-driver-550...' >> /var/log/cloud-init-nvidia.log && "+
				"apt-get update >> /var/log/cloud-init-nvidia.log 2>&1 && "+
				"DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-driver-550 >> /var/log/cloud-init-nvidia.log 2>&1 && "+
				"echo 'NVIDIA driver installed. A reboot may be required.' >> /var/log/cloud-init-nvidia.log; "+
				"fi",
		)
	}

	return &CloudInit{
		RunCmd: runCmds,
	}
}

// shellEscapeSingleQuote escapes a string for use inside single quotes in shell.
// Single quotes are replaced with: '\” (end quote, escaped quote, start quote)
func shellEscapeSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// parseCreateError extracts error information from a failed create response
func (c *Client) parseCreateError(body []byte, statusCode int) error {
	// Try to parse as JSON array of errors (TensorDock validation errors)
	var validationErrors []struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Path    []string `json:"path"`
	}
	if err := json.Unmarshal(body, &validationErrors); err == nil && len(validationErrors) > 0 {
		messages := make([]string, len(validationErrors))
		for i, e := range validationErrors {
			messages[i] = e.Message
		}
		msg := sanitizeErrorMessage(strings.Join(messages, "; "))
		return provider.NewProviderError("tensordock", "CreateInstance", statusCode,
			msg, provider.ErrProviderError)
	}

	msg := sanitizeErrorMessage(string(body))
	return provider.NewProviderError("tensordock", "CreateInstance", statusCode,
		msg, provider.ErrProviderError)
}

// checkBodyForError checks if a successful HTTP response contains an error in the body
func (c *Client) checkBodyForError(body []byte) error {
	var errResp struct {
		Status int    `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Status >= 400 {
		msg := sanitizeErrorMessage(errResp.Error)
		if isStaleInventoryErrorMessage(errResp.Error) {
			return provider.NewProviderError("tensordock", "CreateInstance", errResp.Status,
				msg, provider.ErrOfferStaleInventory)
		}
		return fmt.Errorf("TensorDock CreateInstance failed: %s", msg)
	}
	return nil
}

// locationGPUToOffer converts a TensorDock location+GPU to a unified GPUOffer
func locationGPUToOffer(loc Location, gpu LocationGPU) models.GPUOffer {
	vram := parseVRAMFromName(gpu.DisplayName)
	location := fmt.Sprintf("%s, %s, %s", loc.City, loc.StateProvince, loc.Country)

	// Bug #37 fix: Cap reliability to 1.0 in case Tier > 3
	reliability := float64(loc.Tier) / 3.0
	if reliability > 1.0 {
		reliability = 1.0
	}

	return models.GPUOffer{
		ID:                     fmt.Sprintf("tensordock-%s-%s", loc.ID, gpu.V0Name),
		Provider:               "tensordock",
		ProviderID:             loc.ID,
		GPUType:                normalizeGPUName(gpu.DisplayName),
		GPUCount:               gpu.MaxCount,
		VRAM:                   vram,
		PricePerHour:           gpu.PricePerHr,
		Location:               location,
		Reliability:            reliability,
		Available:              true,
		MaxDuration:            0, // No maximum duration
		FetchedAt:              time.Now(),
		AvailabilityConfidence: TensorDockAvailabilityConfidence,
	}
}

// isStaleInventoryErrorMessage checks if an error indicates stale inventory data.
// These errors occur when TensorDock's /locations shows availability but
// the actual resources aren't available for provisioning.
//
// Extended patterns cover:
//   - Direct unavailability messages (no nodes, insufficient capacity)
//   - Resource constraints (GPU unavailable, memory limits)
//   - Datacenter/location issues (temporarily unavailable, maintenance)
//   - Demand-related issues (high demand, sold out)
func isStaleInventoryErrorMessage(msg string) bool {
	staleIndicators := []string{
		// Direct unavailability
		"no available nodes",
		"insufficient capacity",
		"not enough capacity",
		"resource unavailable",
		"out of stock",
		"no nodes available",
		"nodes unavailable",

		// GPU-specific
		"gpu unavailable",
		"gpu not available",
		"no gpus available",
		"requested gpu", // "requested GPU not available"
		"gpu capacity",  // "GPU capacity exceeded"
		"no matching gpu",
		"gpu is not available",

		// Location/datacenter issues
		"datacenter unavailable",
		"location unavailable",
		"temporarily unavailable",
		"under maintenance",
		"region unavailable",
		"site unavailable",

		// Demand-related
		"high demand",
		"sold out",
		"fully allocated",
		"at capacity",
		"no capacity",
		"capacity limit",
		"cannot allocate",
		"allocation failed",

		// Resource limits
		"resource limit",
		"quota exceeded",
		"limit reached",
		"maximum", // covers "maximum reached", "maximum instances reached"

		// Network resource issues (BUG-010)
		"no available public ips",
		"no available public ip",
		"no public ip",
	}
	msgLower := strings.ToLower(msg)
	for _, indicator := range staleIndicators {
		if strings.Contains(msgLower, indicator) {
			return true
		}
	}
	return false
}
