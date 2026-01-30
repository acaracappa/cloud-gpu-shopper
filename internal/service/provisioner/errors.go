package provisioner

import "fmt"

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

// HeartbeatTimeoutError indicates the agent failed to send heartbeat in time
type HeartbeatTimeoutError struct {
	SessionID string
	Timeout   string
}

func (e *HeartbeatTimeoutError) Error() string {
	return fmt.Sprintf("agent heartbeat timeout for session %s after %s", e.SessionID, e.Timeout)
}
