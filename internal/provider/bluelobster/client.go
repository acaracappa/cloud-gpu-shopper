package bluelobster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

const (
	defaultBaseURL   = "https://api.bluelobster.ai/api/v1"
	defaultTimeout   = 30 * time.Second
	defaultTemplate  = "UBUNTU-22-04-NV"
	defaultSSHUser   = "ubuntu"
	defaultSSHPort   = 22
	taskPollInterval = 3 * time.Second
	taskPollTimeout  = 3 * time.Minute
)

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

// Client implements the provider.Provider interface for Blue Lobster
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

// WithRateLimit sets a custom rate limiter
func WithRateLimit(r rate.Limit, burst int) ClientOption {
	return func(c *Client) {
		c.limiter = rate.NewLimiter(r, burst)
	}
}

// WithCircuitBreaker configures the circuit breaker for API calls
func WithCircuitBreaker(config CircuitBreakerConfig) ClientOption {
	return func(c *Client) {
		c.circuitBreaker = newCircuitBreaker(config)
	}
}

// WithLogger sets a custom structured logger
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithDefaultTemplate sets the default OS template for instance provisioning
func WithDefaultTemplate(name string) ClientOption {
	return func(c *Client) {
		c.defaultTemplate = name
	}
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
func (c *Client) Name() string {
	return "bluelobster"
}

// SupportsFeature checks if the provider supports a specific feature
func (c *Client) SupportsFeature(feature provider.ProviderFeature) bool {
	switch feature {
	case provider.FeatureInstanceTags:
		return true // Blue Lobster supports instance metadata tags
	default:
		return false
	}
}

// ListOffers returns available GPU offers from Blue Lobster
func (c *Client) ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error) {
	// Stub - will be implemented in Task 3
	return nil, fmt.Errorf("bluelobster: ListOffers not yet implemented")
}

// ListAllInstances returns all instances with our tags (for reconciliation)
func (c *Client) ListAllInstances(ctx context.Context) ([]provider.ProviderInstance, error) {
	// Stub - will be implemented in Task 6
	return nil, fmt.Errorf("bluelobster: ListAllInstances not yet implemented")
}

// CreateInstance provisions a new GPU instance
func (c *Client) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
	// Stub - will be implemented in Task 4
	return nil, fmt.Errorf("bluelobster: CreateInstance not yet implemented")
}

// DestroyInstance tears down a GPU instance
func (c *Client) DestroyInstance(ctx context.Context, instanceID string) error {
	// Stub - will be implemented in Task 5
	return fmt.Errorf("bluelobster: DestroyInstance not yet implemented")
}

// GetInstanceStatus returns current status of an instance
func (c *Client) GetInstanceStatus(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
	// Stub - will be implemented in Task 5
	return nil, fmt.Errorf("bluelobster: GetInstanceStatus not yet implemented")
}

// =============================================================================
// Internal Helpers
// =============================================================================

// rateLimit waits for the rate limiter to allow the request
func (c *Client) rateLimit(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}

// checkCircuitBreaker returns an error if the circuit breaker is open
func (c *Client) checkCircuitBreaker() error {
	if !c.circuitBreaker.allow() {
		backoff := c.circuitBreaker.getBackoff()
		c.logger.Warn("circuit breaker is open", "provider", "bluelobster", "backoff", backoff)
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
	metrics.RecordProviderAPIResponseTime("bluelobster", operation, duration)

	status := "success"
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			status = "circuit_open"
		} else {
			status = "error"
		}
	}
	metrics.RecordProviderAPICall("bluelobster", operation, status)

	// Update circuit breaker state metric
	metrics.UpdateProviderCircuitBreakerState("bluelobster", int(c.circuitBreaker.State()))
}

// doRequest performs a full HTTP request lifecycle: check circuit breaker, rate limit,
// build request with X-API-Key header, execute, read body, handle errors, unmarshal JSON.
func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader, result interface{}) error {
	// Check circuit breaker
	if err := c.checkCircuitBreaker(); err != nil {
		return err
	}

	// Rate limit
	if err := c.rateLimit(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}

	// Build URL
	reqURL := c.baseURL + path

	// Build request
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication header
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.recordAPIResult(err)
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle error status codes
	if resp.StatusCode >= 400 {
		apiErr := c.parseError(resp.StatusCode, respBody, method+" "+path)
		return apiErr
	}

	// Unmarshal JSON into result if provided
	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// parseError attempts to extract a structured error from the API response body.
// It tries ErrorDetailResponse first, then ErrorResponse, then falls back to raw body.
func (c *Client) parseError(statusCode int, body []byte, operation string) error {
	message := string(body)

	// Try ErrorDetailResponse first ({"detail": {"error": "...", "message": "..."}})
	var detailResp ErrorDetailResponse
	if err := json.Unmarshal(body, &detailResp); err == nil && detailResp.Detail.Message != "" {
		message = detailResp.Detail.Message
		if detailResp.Detail.Error != "" {
			message = detailResp.Detail.Error + ": " + detailResp.Detail.Message
		}
		return c.mapHTTPError(statusCode, message, operation)
	}

	// Try ErrorResponse ({"error": "...", "message": "..."})
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && (errResp.Message != "" || errResp.Error != "") {
		if errResp.Message != "" {
			message = errResp.Message
		}
		if errResp.Error != "" {
			if message != errResp.Error {
				message = errResp.Error + ": " + message
			} else {
				message = errResp.Error
			}
		}
		return c.mapHTTPError(statusCode, message, operation)
	}

	// Fall back to raw body
	return c.mapHTTPError(statusCode, message, operation)
}

// mapHTTPError maps HTTP status codes to provider error types
func (c *Client) mapHTTPError(statusCode int, message, operation string) error {
	var baseErr error
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		baseErr = provider.ErrProviderAuth
	case http.StatusNotFound:
		baseErr = provider.ErrInstanceNotFound
	case http.StatusConflict:
		baseErr = provider.ErrOfferUnavailable
	case http.StatusTooManyRequests:
		baseErr = provider.ErrProviderRateLimit
	default:
		baseErr = provider.ErrProviderError
	}

	return provider.NewProviderError("bluelobster", operation, statusCode, message, baseErr)
}

// Ensure Client implements provider.Provider at compile time
var _ provider.Provider = (*Client)(nil)

