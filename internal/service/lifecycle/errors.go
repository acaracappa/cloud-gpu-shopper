package lifecycle

import (
	"fmt"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// SessionTerminalError indicates the session is in a terminal state
type SessionTerminalError struct {
	ID     string
	Status models.SessionStatus
}

func (e *SessionTerminalError) Error() string {
	return fmt.Sprintf("session %s is in terminal state: %s", e.ID, e.Status)
}

// SessionNotFoundError indicates the session was not found
type SessionNotFoundError struct {
	ID string
}

func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("session not found: %s", e.ID)
}
