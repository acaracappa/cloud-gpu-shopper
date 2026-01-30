package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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

	// HeartbeatAge tracks the age of the last heartbeat for each session
	HeartbeatAge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gpu_heartbeat_age_seconds",
			Help: "Seconds since last heartbeat for each session",
		},
		[]string{"session_id"},
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

	// IdleShutdowns counts idle shutdown events
	IdleShutdowns = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gpu_idle_shutdowns_total",
			Help: "Total number of sessions terminated due to idle threshold exceeded",
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

// UpdateHeartbeatAge updates the heartbeat age for a session
func UpdateHeartbeatAge(sessionID string, ageSeconds float64) {
	HeartbeatAge.WithLabelValues(sessionID).Set(ageSeconds)
}

// RemoveHeartbeatAge removes the heartbeat age metric for a session
func RemoveHeartbeatAge(sessionID string) {
	HeartbeatAge.DeleteLabelValues(sessionID)
}

// RecordHardMaxEnforced increments the hard max enforcement counter
func RecordHardMaxEnforced() {
	HardMaxEnforced.Inc()
}

// RecordIdleShutdown increments the idle shutdown counter
func RecordIdleShutdown() {
	IdleShutdowns.Inc()
}

// RecordCost adds to the cost accrued counter
func RecordCost(provider string, amount float64) {
	CostAccrued.WithLabelValues(provider).Add(amount)
}

// RecordBudgetAlert increments the budget alert counter
func RecordBudgetAlert(alertType string) {
	BudgetAlerts.WithLabelValues(alertType).Inc()
}
