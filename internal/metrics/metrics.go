package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTP request metrics for API server
var (
	// HTTPRequestDuration tracks the duration of HTTP requests
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests by method, path, and status",
			Buckets: prometheus.DefBuckets, // Default: .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestsTotal counts the total number of HTTP requests
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests by method, path, and status",
		},
		[]string{"method", "path", "status"},
	)
)

// Critical safety metrics for GPU session management
var (
	// SessionsActive tracks the number of active sessions by provider and status
	SessionsActive = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gpu_sessions_active",
			Help: "Number of active GPU sessions by provider and status",
		},
		[]string{"provider", "status"},
	)

	// OrphansDetected counts the total number of orphaned instances detected
	OrphansDetected = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_orphans_detected_total",
			Help: "Total number of orphaned instances detected (provider instance without DB record)",
		},
	)

	// DestroyFailures counts failed destroy attempts
	DestroyFailures = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_destroy_failures_total",
			Help: "Total number of failed instance destroy attempts",
		},
	)

	// ReconciliationMismatches counts state mismatches found during reconciliation
	ReconciliationMismatches = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_reconciliation_mismatches_total",
			Help: "Total number of state mismatches found during provider reconciliation",
		},
	)

	// SSHVerifyDuration tracks how long SSH verification takes
	SSHVerifyDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gpu_ssh_verify_duration_seconds",
			Help:    "Duration of SSH verification by provider",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s to ~17min
		},
		[]string{"provider"},
	)

	// SSHVerifyFailures counts SSH verification failures
	SSHVerifyFailures = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_ssh_verify_failures_total",
			Help: "Total number of SSH verification failures",
		},
	)

	// SSHVerifyAttempts tracks the number of attempts needed for successful SSH verification
	SSHVerifyAttempts = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gpu_ssh_verify_attempts",
			Help:    "Number of attempts needed for SSH verification by provider",
			Buckets: prometheus.LinearBuckets(1, 1, 10), // 1 to 10 attempts
		},
		[]string{"provider"},
	)

	// SSHVerifyErrorTypes counts SSH verification errors by type
	SSHVerifyErrorTypes = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_ssh_verify_errors_by_type",
			Help: "SSH verification errors by provider and error type (connection_refused, timeout, auth_failed, etc.)",
		},
		[]string{"provider", "error_type"},
	)

	// APIVerifyDuration tracks how long API verification takes (entrypoint mode)
	APIVerifyDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gpu_api_verify_duration_seconds",
			Help:    "Duration of API verification by provider (entrypoint mode)",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s to ~68min (model loading can be slow)
		},
		[]string{"provider"},
	)

	// APIVerifyFailures counts API verification failures
	APIVerifyFailures = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_api_verify_failures_total",
			Help: "Total number of API verification failures (entrypoint mode)",
		},
	)

	// ProviderAPIErrors counts API errors by provider and operation
	ProviderAPIErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_provider_api_errors_total",
			Help: "Total number of provider API errors by provider and operation",
		},
		[]string{"provider", "operation"},
	)

	// Additional useful metrics

	// SessionsCreated counts total sessions created
	SessionsCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_sessions_created_total",
			Help: "Total number of GPU sessions created by provider",
		},
		[]string{"provider"},
	)

	// SessionsDestroyed counts total sessions destroyed
	SessionsDestroyed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_sessions_destroyed_total",
			Help: "Total number of GPU sessions destroyed by provider and reason",
		},
		[]string{"provider", "reason"},
	)

	// HardMaxEnforced counts hard max enforcement events
	HardMaxEnforced = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_hard_max_enforced_total",
			Help: "Total number of sessions terminated due to 12-hour hard max",
		},
	)

	// GhostsDetected counts ghost sessions (DB record without provider instance)
	GhostsDetected = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_ghosts_detected_total",
			Help: "Total number of ghost sessions detected (DB record without provider instance)",
		},
	)

	// ProvisioningDuration tracks how long provisioning takes
	ProvisioningDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gpu_provisioning_duration_seconds",
			Help:    "Duration of session provisioning by provider",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s to ~17min
		},
		[]string{"provider"},
	)

	// CostAccrued tracks total cost accrued
	CostAccrued = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_cost_accrued_usd",
			Help: "Total cost accrued in USD by provider",
		},
		[]string{"provider"},
	)

	// BudgetAlerts counts budget alert events
	BudgetAlerts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_budget_alerts_total",
			Help: "Total number of budget alerts by type (warning, exceeded)",
		},
		[]string{"alert_type"},
	)

	// ProviderAPIResponseTime tracks API response times by provider and operation
	// This helps identify slow operations and potential performance issues
	ProviderAPIResponseTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "gpu_provider_api_response_time_seconds",
			Help: "Response time of provider API calls by provider and operation",
			// Buckets: 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s, 10s, 30s, 60s
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0},
		},
		[]string{"provider", "operation"},
	)

	// ProviderAPICallsTotal counts total API calls by provider, operation, and status
	ProviderAPICallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_provider_api_calls_total",
			Help: "Total number of provider API calls by provider, operation, and status",
		},
		[]string{"provider", "operation", "status"},
	)

	// ProviderCircuitBreakerState tracks circuit breaker state by provider
	// Values: 0 = closed, 1 = open, 2 = half-open
	ProviderCircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gpu_provider_circuit_breaker_state",
			Help: "Current state of provider circuit breaker (0=closed, 1=open, 2=half-open)",
		},
		[]string{"provider"},
	)

	// SessionRetryAttempts counts auto-retry attempts
	SessionRetryAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_session_retry_attempts_total",
			Help: "Total number of session auto-retry attempts by provider, scope, and reason",
		},
		[]string{"provider", "scope", "reason"},
	)

	// SessionRetrySuccesses counts successful auto-retries
	SessionRetrySuccesses = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_session_retry_successes_total",
			Help: "Total number of successful session auto-retries by provider and scope",
		},
		[]string{"provider", "scope"},
	)

	// SessionRetryExhausted counts cases where all retries were exhausted
	SessionRetryExhausted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_session_retry_exhausted_total",
			Help: "Total number of sessions where all retry attempts were exhausted",
		},
		[]string{"provider", "scope"},
	)

	// SessionDiskAvailableGB tracks available disk space observed post-provision
	SessionDiskAvailableGB = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gpu_session_disk_available_gb",
			Help: "Available disk space in GB observed on instance post-provision",
		},
		[]string{"provider"},
	)

	// OfferFailuresRecorded counts offer provisioning failures by provider, GPU type, and failure type
	OfferFailuresRecorded = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gpu_offer_failures_total",
			Help: "Offer provisioning failures by provider, GPU type, and failure type",
		},
		[]string{"provider", "gpu_type", "failure_type"},
	)
)

// Helper functions for common metric operations

// RecordSessionCreated increments the session created counter
func RecordSessionCreated(provider string) {
	SessionsCreated.WithLabelValues(provider).Inc()
}

// RecordSessionDestroyed increments the session destroyed counter
func RecordSessionDestroyed(provider, reason string) {
	SessionsDestroyed.WithLabelValues(provider, reason).Inc()
}

// RecordOrphanDetected increments the orphan counter and reconciliation mismatches
func RecordOrphanDetected() {
	OrphansDetected.Inc()
	ReconciliationMismatches.Inc()
}

// RecordGhostDetected increments the ghost counter and reconciliation mismatches
func RecordGhostDetected() {
	GhostsDetected.Inc()
	ReconciliationMismatches.Inc()
}

// RecordDestroyFailure increments the destroy failure counter
func RecordDestroyFailure() {
	DestroyFailures.Inc()
}

// RecordProviderError increments the provider API error counter
func RecordProviderError(provider, operation string) {
	ProviderAPIErrors.WithLabelValues(provider, operation).Inc()
}

// UpdateSessionStatus updates the active sessions gauge
func UpdateSessionStatus(provider, oldStatus, newStatus string) {
	if oldStatus != "" {
		SessionsActive.WithLabelValues(provider, oldStatus).Dec()
	}
	if newStatus != "" {
		SessionsActive.WithLabelValues(provider, newStatus).Inc()
	}
}

// RecordSSHVerifyDuration records how long SSH verification took
func RecordSSHVerifyDuration(provider string, duration time.Duration) {
	SSHVerifyDuration.WithLabelValues(provider).Observe(duration.Seconds())
}

// RecordSSHVerifyFailure increments the SSH verify failure counter
func RecordSSHVerifyFailure() {
	SSHVerifyFailures.Inc()
}

// RecordSSHVerifyAttempts records the number of attempts for a successful SSH verification
func RecordSSHVerifyAttempts(provider string, attempts int) {
	SSHVerifyAttempts.WithLabelValues(provider).Observe(float64(attempts))
}

// RecordSSHVerifyError records an SSH verification error by type
func RecordSSHVerifyError(provider, errorType string) {
	SSHVerifyErrorTypes.WithLabelValues(provider, errorType).Inc()
}

// RecordHardMaxEnforced increments the hard max enforcement counter
func RecordHardMaxEnforced() {
	HardMaxEnforced.Inc()
}

// RecordCost adds to the cost accrued counter
func RecordCost(provider string, amount float64) {
	CostAccrued.WithLabelValues(provider).Add(amount)
}

// RecordBudgetAlert increments the budget alert counter
func RecordBudgetAlert(alertType string) {
	BudgetAlerts.WithLabelValues(alertType).Inc()
}

// RecordAPIVerifyDuration records how long API verification took
func RecordAPIVerifyDuration(provider string, duration time.Duration) {
	APIVerifyDuration.WithLabelValues(provider).Observe(duration.Seconds())
}

// RecordAPIVerifyFailure increments the API verify failure counter
func RecordAPIVerifyFailure() {
	APIVerifyFailures.Inc()
}

// RecordProvisioningDuration records how long session provisioning took
// Bug #57 fix: Add helper function for provisioning duration metric
func RecordProvisioningDuration(provider string, duration time.Duration) {
	ProvisioningDuration.WithLabelValues(provider).Observe(duration.Seconds())
}

// RecordProviderAPIResponseTime records the response time for a provider API call
func RecordProviderAPIResponseTime(provider, operation string, duration time.Duration) {
	ProviderAPIResponseTime.WithLabelValues(provider, operation).Observe(duration.Seconds())
}

// RecordProviderAPICall records a provider API call with its status
// status should be "success", "error", or "circuit_open"
func RecordProviderAPICall(provider, operation, status string) {
	ProviderAPICallsTotal.WithLabelValues(provider, operation, status).Inc()
}

// UpdateProviderCircuitBreakerState updates the circuit breaker state metric
// state should be 0 (closed), 1 (open), or 2 (half-open)
func UpdateProviderCircuitBreakerState(provider string, state int) {
	ProviderCircuitBreakerState.WithLabelValues(provider).Set(float64(state))
}

// RecordHTTPRequest records the duration and increments the counter for an HTTP request
func RecordHTTPRequest(method, path, status string, duration time.Duration) {
	HTTPRequestDuration.WithLabelValues(method, path, status).Observe(duration.Seconds())
	HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
}

// RecordRetryAttempt increments the retry attempt counter
func RecordRetryAttempt(provider, scope, reason string) {
	SessionRetryAttempts.WithLabelValues(provider, scope, reason).Inc()
}

// RecordRetrySuccess increments the retry success counter
func RecordRetrySuccess(provider, scope string) {
	SessionRetrySuccesses.WithLabelValues(provider, scope).Inc()
}

// RecordRetryExhausted increments the retry exhausted counter
func RecordRetryExhausted(provider, scope string) {
	SessionRetryExhausted.WithLabelValues(provider, scope).Inc()
}

// RecordDiskAvailable sets the disk available gauge for a provider
func RecordDiskAvailable(provider string, gb float64) {
	SessionDiskAvailableGB.WithLabelValues(provider).Set(gb)
}

// RecordOfferFailure increments the offer failure counter
func RecordOfferFailure(provider, gpuType, failureType string) {
	OfferFailuresRecorded.WithLabelValues(provider, gpuType, failureType).Inc()
}

// SessionCount holds the count of sessions for a provider/status combination
type SessionCount struct {
	Provider string
	Status   string
	Count    int
}

// InitializeSessionMetrics populates gauges from database state on startup.
// This ensures metrics reflect reality before any reconciliation runs,
// preventing negative gauge values from state transitions.
func InitializeSessionMetrics(ctx context.Context, counts []SessionCount) error {
	for _, c := range counts {
		SessionsActive.WithLabelValues(c.Provider, c.Status).Set(float64(c.Count))
	}
	slog.Info("initialized session metrics from database",
		slog.Int("label_combinations", len(counts)))
	return nil
}
