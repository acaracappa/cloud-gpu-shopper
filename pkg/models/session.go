package models

import "time"

// SessionStatus represents the current state of a GPU session
type SessionStatus string

const (
	StatusPending      SessionStatus = "pending"      // Session created, not yet provisioned
	StatusProvisioning SessionStatus = "provisioning" // Provider instance being created
	StatusRunning      SessionStatus = "running"      // Instance running and SSH accessible
	StatusStopping     SessionStatus = "stopping"     // Destruction in progress
	StatusStopped      SessionStatus = "stopped"      // Successfully terminated
	StatusFailed       SessionStatus = "failed"       // Failed to provision or crashed
)

// WorkloadType represents the type of workload for the session
type WorkloadType string

const (
	WorkloadLLM         WorkloadType = "llm"         // LLM inference hosting (generic)
	WorkloadLLMVLLM     WorkloadType = "llm_vllm"    // LLM inference via vLLM
	WorkloadLLMTGI      WorkloadType = "llm_tgi"     // LLM inference via TGI
	WorkloadTraining    WorkloadType = "training"    // ML model training
	WorkloadBatch       WorkloadType = "batch"       // Batch processing job
	WorkloadInteractive WorkloadType = "interactive" // Interactive SSH session
)

// LaunchMode determines how the instance is configured
type LaunchMode string

const (
	// LaunchModeSSH configures the instance for interactive SSH access
	LaunchModeSSH LaunchMode = "ssh"
	// LaunchModeEntrypoint configures the instance to run a specific workload
	LaunchModeEntrypoint LaunchMode = "entrypoint"
)

// StoragePolicy determines what happens to storage after session ends
type StoragePolicy string

const (
	StoragePreserve StoragePolicy = "preserve" // Keep storage after shutdown
	StorageDestroy  StoragePolicy = "destroy"  // Delete storage after shutdown
)

// Session represents an active GPU rental session
type Session struct {
	ID         string        `json:"id"`
	ConsumerID string        `json:"consumer_id"`
	Provider   string        `json:"provider"`
	ProviderID string        `json:"provider_instance_id"`
	OfferID    string        `json:"offer_id"`
	GPUType    string        `json:"gpu_type"`
	GPUCount   int           `json:"gpu_count"`
	Status     SessionStatus `json:"status"`
	Error      string        `json:"error,omitempty"`

	// Connection details (SSH mode)
	SSHHost       string `json:"ssh_host,omitempty"`
	SSHPort       int    `json:"ssh_port,omitempty"`
	SSHUser       string `json:"ssh_user,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"` // Only returned once at creation
	SSHPublicKey  string `json:"-"`                         // Stored but not exposed

	// API endpoint details (entrypoint mode)
	LaunchMode  LaunchMode `json:"launch_mode,omitempty"`
	APIEndpoint string     `json:"api_endpoint,omitempty"` // Full URL to API (e.g., http://host:port)
	APIPort     int        `json:"api_port,omitempty"`     // Mapped API port

	// Workload configuration (entrypoint mode)
	DockerImage  string `json:"docker_image,omitempty"`
	ModelID      string `json:"model_id,omitempty"`     // HuggingFace model ID
	Quantization string `json:"quantization,omitempty"` // Quantization method
	ExposedPorts []int  `json:"exposed_ports,omitempty"`

	// Template-based provisioning (Vast.ai)
	TemplateHashID string `json:"template_hash_id,omitempty"` // Vast.ai template hash_id
	TemplateName   string `json:"template_name,omitempty"`    // Template name for display

	// Storage configuration
	DiskGB int `json:"disk_gb,omitempty"` // Disk space in GB (cannot be changed after creation)

	// Auto-retry configuration (set at creation)
	AutoRetry  bool   `json:"auto_retry,omitempty"`
	MaxRetries int    `json:"max_retries,omitempty"`
	RetryScope string `json:"retry_scope,omitempty"` // "same_gpu", "same_vram", "any"

	// Retry tracking (set by provisioner)
	RetryCount    int    `json:"retry_count,omitempty"`
	RetryParentID string `json:"retry_parent_id,omitempty"` // Original session that triggered retry
	RetryChildID  string `json:"retry_child_id,omitempty"`  // New session created by retry
	FailedOffers  string `json:"failed_offers,omitempty"`   // Comma-separated list of offer IDs that failed

	// Configuration
	WorkloadType    WorkloadType  `json:"workload_type"`
	ReservationHrs  int           `json:"reservation_hours"`
	HardMaxOverride bool          `json:"hard_max_override"`
	IdleThreshold   int           `json:"idle_threshold_minutes"` // 0 = disabled
	StoragePolicy   StoragePolicy `json:"storage_policy"`

	// Cost tracking
	PricePerHour float64 `json:"price_per_hour"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
}

// CreateSessionRequest is the request to create a new session
type CreateSessionRequest struct {
	ConsumerID     string        `json:"consumer_id" binding:"required"`
	OfferID        string        `json:"offer_id" binding:"required"`
	WorkloadType   WorkloadType  `json:"workload_type" binding:"required"`
	ReservationHrs int           `json:"reservation_hours" binding:"required,min=1,max=12"`
	IdleThreshold  int           `json:"idle_threshold_minutes,omitempty"`
	StoragePolicy  StoragePolicy `json:"storage_policy,omitempty"`

	// Entrypoint mode configuration
	LaunchMode   LaunchMode `json:"launch_mode,omitempty"`   // "ssh" or "entrypoint"
	DockerImage  string     `json:"docker_image,omitempty"`  // Custom Docker image
	ModelID      string     `json:"model_id,omitempty"`      // HuggingFace model ID
	ExposedPorts []int      `json:"exposed_ports,omitempty"` // Ports to expose (e.g., 8000)
	Quantization string     `json:"quantization,omitempty"`  // Quantization method

	// Template-based provisioning (Vast.ai)
	// If TemplateHashID is set, use the template instead of building config from DockerImage
	TemplateHashID string `json:"template_hash_id,omitempty"` // Vast.ai template hash_id

	// Storage configuration
	DiskGB int `json:"disk_gb,omitempty"` // Disk space in GB (cannot be changed after creation)

	// Auto-retry configuration
	AutoRetry  bool   `json:"auto_retry,omitempty"`
	MaxRetries int    `json:"max_retries,omitempty"`
	RetryScope string `json:"retry_scope,omitempty"` // "same_gpu", "same_vram", "any"

	// Internal fields (set by handler, not from JSON)
	TemplateRecommendedDiskGB    int           `json:"-"` // Template's recommended disk, used for estimation floor
	TemplateRecommendedSSHTimeout time.Duration `json:"-"` // BUG-005: Template's recommended SSH timeout for heavy images
}

// SessionResponse is the API response for a session (hides sensitive fields after creation)
type SessionResponse struct {
	ID             string        `json:"id"`
	ConsumerID     string        `json:"consumer_id"`
	Provider       string        `json:"provider"`
	GPUType        string        `json:"gpu_type"`
	GPUCount       int           `json:"gpu_count"`
	Status         SessionStatus `json:"status"`
	Error          string        `json:"error,omitempty"`
	SSHHost        string        `json:"ssh_host,omitempty"`
	SSHPort        int           `json:"ssh_port,omitempty"`
	SSHUser        string        `json:"ssh_user,omitempty"`
	LaunchMode     LaunchMode    `json:"launch_mode,omitempty"`
	APIEndpoint    string        `json:"api_endpoint,omitempty"`
	APIPort        int           `json:"api_port,omitempty"`
	ModelID        string        `json:"model_id,omitempty"`
	TemplateHashID string        `json:"template_hash_id,omitempty"` // Vast.ai template used
	TemplateName   string        `json:"template_name,omitempty"`    // Template name for display
	DiskGB         int           `json:"disk_gb,omitempty"`          // Disk space in GB
	WorkloadType   WorkloadType  `json:"workload_type"`
	ReservationHrs int           `json:"reservation_hours"`
	PricePerHour   float64       `json:"price_per_hour"`
	CreatedAt      time.Time     `json:"created_at"`
	ExpiresAt      time.Time     `json:"expires_at"`

	// Retry tracking
	AutoRetry     bool   `json:"auto_retry,omitempty"`
	RetryCount    int    `json:"retry_count,omitempty"`
	RetryParentID string `json:"retry_parent_id,omitempty"`
	RetryChildID  string `json:"retry_child_id,omitempty"`
	FailedOffers  string `json:"failed_offers,omitempty"`
}

// ToResponse converts a Session to a SessionResponse (without secrets)
func (s *Session) ToResponse() SessionResponse {
	return SessionResponse{
		ID:             s.ID,
		ConsumerID:     s.ConsumerID,
		Provider:       s.Provider,
		GPUType:        s.GPUType,
		GPUCount:       s.GPUCount,
		Status:         s.Status,
		Error:          s.Error,
		SSHHost:        s.SSHHost,
		SSHPort:        s.SSHPort,
		SSHUser:        s.SSHUser,
		LaunchMode:     s.LaunchMode,
		APIEndpoint:    s.APIEndpoint,
		APIPort:        s.APIPort,
		ModelID:        s.ModelID,
		TemplateHashID: s.TemplateHashID,
		TemplateName:   s.TemplateName,
		DiskGB:         s.DiskGB,
		WorkloadType:   s.WorkloadType,
		ReservationHrs: s.ReservationHrs,
		PricePerHour:   s.PricePerHour,
		CreatedAt:      s.CreatedAt,
		ExpiresAt:      s.ExpiresAt,
		AutoRetry:      s.AutoRetry,
		RetryCount:     s.RetryCount,
		RetryParentID:  s.RetryParentID,
		RetryChildID:   s.RetryChildID,
		FailedOffers:   s.FailedOffers,
	}
}

// IsActive returns true if the session is in an active state
func (s *Session) IsActive() bool {
	return s.Status == StatusPending ||
		s.Status == StatusProvisioning ||
		s.Status == StatusRunning
}

// IsTerminal returns true if the session is in a terminal state
func (s *Session) IsTerminal() bool {
	return s.Status == StatusStopped || s.Status == StatusFailed
}

// SessionListFilter defines parameters for listing sessions
type SessionListFilter struct {
	ConsumerID string
	Status     SessionStatus
	Provider   string // Bug #100 fix: Add provider filter
	Limit      int
}
