package lifecycle

import (
	"fmt"
	"time"

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

// HardMaxExceededError indicates extension would exceed 12-hour hard max
type HardMaxExceededError struct {
	SessionID       string
	CurrentDuration time.Duration
	RequestedHours  int
	HardMaxHours    int
}

func (e *HardMaxExceededError) Error() string {
	return fmt.Sprintf(
		"extension would exceed %d-hour hard max: session %s has been running for %.1f hours, requested %d more hours",
		e.HardMaxHours, e.SessionID, e.CurrentDuration.Hours(), e.RequestedHours,
	)
}
