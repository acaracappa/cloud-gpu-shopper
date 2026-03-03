package provisioner

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// ErrNotFound is returned when a record is not found
var ErrNotFound = errors.New("record not found")

// DestroyVerificationError indicates instance destruction couldn't be verified
type DestroyVerificationError struct {
	SessionID  string
	ProviderID string
	Attempts   int
}

func (e *DestroyVerificationError) Error() string {
	return fmt.Sprintf("failed to verify destruction of session %s (provider: %s) after %d attempts",
		e.SessionID, e.ProviderID, e.Attempts)
}

// ProviderNotFoundError indicates the requested provider doesn't exist
type ProviderNotFoundError struct {
	Name string
}

func (e *ProviderNotFoundError) Error() string {
	return fmt.Sprintf("provider not found: %s", e.Name)
}

// SessionNotFoundError indicates the requested session doesn't exist
type SessionNotFoundError struct {
	ID string
}

func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("session not found: %s", e.ID)
}

// DuplicateSessionError indicates a consumer already has an active session for the given offer
type DuplicateSessionError struct {
	ConsumerID string
	OfferID    string
	SessionID  string
	Status     models.SessionStatus
}

func (e *DuplicateSessionError) Error() string {
	return fmt.Sprintf("consumer %s already has active session %s for offer %s (status: %s)",
		e.ConsumerID, e.SessionID, e.OfferID, e.Status)
}

// StaleInventoryError indicates provisioning failed due to stale/outdated inventory
// This suggests the offer appeared available but was not actually available.
// Callers should consider retrying with a different offer.
type StaleInventoryError struct {
	OfferID     string
	Provider    string
	OriginalErr error
}

func (e *StaleInventoryError) Error() string {
	return fmt.Sprintf("offer %s from %s unavailable (stale inventory): %v - consider retrying with a different offer",
		e.OfferID, e.Provider, e.OriginalErr)
}

func (e *StaleInventoryError) Unwrap() error {
	return e.OriginalErr
}

// InsufficientDiskError indicates the requested disk space is too small for the model
type InsufficientDiskError struct {
	RequestedGB   int
	MinimumGB     int
	RecommendedGB int
	Estimation    *DiskEstimation
}

func (e *InsufficientDiskError) Error() string {
	msg := fmt.Sprintf("insufficient disk space: %d GB requested, minimum %d GB required (recommended: %d GB)",
		e.RequestedGB, e.MinimumGB, e.RecommendedGB)
	if e.Estimation != nil {
		msg += " — breakdown: " + e.Estimation.FormatBreakdown()
	}
	return msg
}

// IsRetryableWithDifferentOffer returns true if the error indicates we should
// automatically try a different offer (e.g., stale inventory errors)
func IsRetryableWithDifferentOffer(err error) bool {
	var staleErr *StaleInventoryError
	return errors.As(err, &staleErr)
}

// classifySSHError categorizes SSH connection errors for logging.
// Returns: error_type (connection_refused, timeout, auth_failed, etc.)
func classifySSHError(err error) string {
	if err == nil {
		return "none"
	}

	errStr := err.Error()

	if strings.Contains(errStr, "connection refused") {
		return "connection_refused"
	}

	if strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection timed out") ||
		strings.Contains(errStr, "deadline exceeded") {
		return "timeout"
	}

	if strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "network is unreachable") {
		return "network_unreachable"
	}

	if strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "lookup") {
		return "dns_failed"
	}

	if strings.Contains(errStr, "SSH handshake failed") ||
		strings.Contains(errStr, "unable to authenticate") ||
		strings.Contains(errStr, "permission denied") {
		return "auth_failed"
	}

	if strings.Contains(errStr, "failed to parse private key") {
		return "key_parse_failed"
	}

	if strings.Contains(errStr, "failed to create session") ||
		strings.Contains(errStr, "verify command failed") {
		return "command_failed"
	}

	if strings.Contains(errStr, "unexpected packet") {
		return "connection_closed"
	}

	if strings.Contains(errStr, "EOF") {
		return "connection_closed"
	}

	return "unknown"
}

// classifyInstanceStopReason provides a more descriptive failure reason based on
// the instance status and error message from the provider.
func classifyInstanceStopReason(status, errorMsg string) string {
	base := fmt.Sprintf("instance stopped unexpectedly: %s", status)

	if errorMsg != "" {
		base += fmt.Sprintf(" (%s)", errorMsg)
	}

	lower := strings.ToLower(status + " " + errorMsg)
	switch {
	case strings.Contains(lower, "loading"):
		base += " — likely cause: image pull failed, disk full, or driver incompatibility"
	case strings.Contains(lower, "error"):
		base += " — likely cause: runtime crash, OOM, or configuration error"
	case strings.Contains(lower, "exited"):
		base += " — likely cause: entrypoint failed or OOM kill"
	}

	return base
}

// ClassifyProvisionError categorizes provisioning errors for consumer apps.
// Returns an error type string and whether the consumer should retry.
func ClassifyProvisionError(err error) (errorType string, retrySuggested bool) {
	if err == nil {
		return "none", false
	}
	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "ssh") || strings.Contains(msg, "timeout"):
		return "ssh_timeout", true
	case strings.Contains(msg, "stopped") || strings.Contains(msg, "terminated"):
		return "instance_stopped", true
	case strings.Contains(msg, "connection refused"):
		return "provider_unavailable", true
	case strings.Contains(msg, "not found") || strings.Contains(msg, "invalid"):
		return "invalid_request", false
	default:
		return "provision_failed", true
	}
}
