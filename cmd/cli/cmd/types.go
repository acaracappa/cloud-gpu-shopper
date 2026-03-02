package cmd

import "github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"

// Re-export GPUOffer from models for CLI use
type GPUOffer = models.GPUOffer

// Session represents a session from the API (CLI-specific with string timestamps)
// Note: We use string for timestamps because the CLI receives JSON and displays them directly.
// The server's models.SessionResponse uses time.Time which serializes to RFC3339 strings.
type Session struct {
	ID           string  `json:"id"`
	ConsumerID   string  `json:"consumer_id"`
	Provider     string  `json:"provider"`
	GPUType      string  `json:"gpu_type"`
	GPUCount     int     `json:"gpu_count"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
	SSHHost      string  `json:"ssh_host,omitempty"`
	SSHPort      int     `json:"ssh_port,omitempty"`
	SSHUser      string  `json:"ssh_user,omitempty"`
	WorkloadType string  `json:"workload_type"`
	PricePerHour float64 `json:"price_per_hour"`
	CreatedAt    string  `json:"created_at"`
	ExpiresAt    string  `json:"expires_at"`
}

// SessionResponse is the response from session creation
type SessionResponse struct {
	Session       Session `json:"session"`
	SSHPrivateKey string  `json:"ssh_private_key,omitempty"`
}
