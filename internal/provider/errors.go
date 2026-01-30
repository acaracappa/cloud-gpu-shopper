package provider

import (
	"errors"
	"fmt"
	"net/http"
)

// ProviderError wraps an error with provider context
type ProviderError struct {
	Provider   string
	Operation  string
	StatusCode int
	Message    string
	Err        error
}

func (e *ProviderError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s %s failed (HTTP %d): %s", e.Provider, e.Operation, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s %s failed: %s", e.Provider, e.Operation, e.Message)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// NewProviderError creates a new ProviderError
func NewProviderError(provider, operation string, statusCode int, message string, err error) *ProviderError {
	return &ProviderError{
		Provider:   provider,
		Operation:  operation,
		StatusCode: statusCode,
		Message:    message,
		Err:        err,
	}
}

// IsRateLimitError checks if the error is a rate limit error
func IsRateLimitError(err error) bool {
	if errors.Is(err, ErrProviderRateLimit) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == http.StatusTooManyRequests
	}
	return false
}

// IsAuthError checks if the error is an authentication error
func IsAuthError(err error) bool {
	if errors.Is(err, ErrProviderAuth) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == http.StatusUnauthorized || pe.StatusCode == http.StatusForbidden
	}
	return false
}

// IsNotFoundError checks if the error is a not found error
func IsNotFoundError(err error) bool {
	if errors.Is(err, ErrInstanceNotFound) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == http.StatusNotFound
	}
	return false
}

// IsRetryable checks if the error is retryable
func IsRetryable(err error) bool {
	if IsRateLimitError(err) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		// Server errors are generally retryable
		return pe.StatusCode >= 500 && pe.StatusCode < 600
	}
	return false
}

// IsStaleInventoryError checks if the error indicates stale inventory
// This typically means the offer appeared available but failed to provision
// because the inventory data was out of date
func IsStaleInventoryError(err error) bool {
	if errors.Is(err, ErrOfferStaleInventory) {
		return true
	}
	if errors.Is(err, ErrOfferUnavailable) {
		return true
	}
	return false
}

// ShouldRetryWithDifferentOffer checks if provisioning failed due to
// stale inventory and we should automatically try a different offer
func ShouldRetryWithDifferentOffer(err error) bool {
	return IsStaleInventoryError(err)
}
