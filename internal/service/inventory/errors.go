package inventory

import (
	"fmt"
	"strings"
)

// ProviderNotFoundError indicates the requested provider doesn't exist
type ProviderNotFoundError struct {
	Name string
}

func (e *ProviderNotFoundError) Error() string {
	return fmt.Sprintf("provider not found: %s", e.Name)
}

// OfferNotFoundError indicates the requested offer doesn't exist
type OfferNotFoundError struct {
	ID string
}

func (e *OfferNotFoundError) Error() string {
	return fmt.Sprintf("offer not found: %s", e.ID)
}

// AllProvidersFailed indicates all providers failed to respond
type AllProvidersFailed struct {
	Errors []error
}

func (e *AllProvidersFailed) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		msgs[i] = err.Error()
	}
	return fmt.Sprintf("all providers failed: %s", strings.Join(msgs, "; "))
}

func (e *AllProvidersFailed) Unwrap() []error {
	return e.Errors
}
