package provisioner

import (
	"errors"
	"fmt"

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
