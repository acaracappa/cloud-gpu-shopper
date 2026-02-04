package provider

import (
	"context"
	"errors"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// Common errors returned by providers
var (
	ErrProviderRateLimit   = errors.New("provider rate limit exceeded")
	ErrProviderAuth        = errors.New("provider authentication failed")
	ErrInstanceNotFound    = errors.New("instance not found")
	ErrTemplateNotFound    = errors.New("template not found")
	ErrOfferUnavailable    = errors.New("offer no longer available")
	ErrOfferStaleInventory = errors.New("offer unavailable due to stale inventory - retry with different offer recommended")
	ErrProviderError       = errors.New("provider API error")
	ErrInvalidResponse     = errors.New("invalid provider response")
)

// ProviderFeature represents optional features a provider may support
type ProviderFeature string

const (
	FeatureIdleDetection ProviderFeature = "idle_detection"
	FeatureInstanceTags  ProviderFeature = "instance_tags"
	FeatureSpotPricing   ProviderFeature = "spot_pricing"
	FeatureCustomImages  ProviderFeature = "custom_images"
)

// LaunchMode determines how the instance is configured
type LaunchMode string

const (
	// LaunchModeSSH configures the instance for interactive SSH access
	LaunchModeSSH LaunchMode = "ssh"
	// LaunchModeEntrypoint configures the instance to run a specific workload (e.g., vLLM, TGI)
	LaunchModeEntrypoint LaunchMode = "entrypoint"
)

// WorkloadType represents the type of workload for entrypoint mode
type WorkloadType string

const (
	WorkloadTypeVLLM   WorkloadType = "vllm"
	WorkloadTypeTGI    WorkloadType = "tgi"
	WorkloadTypeCustom WorkloadType = "custom"
)

// WorkloadConfig contains configuration for entrypoint-mode workloads
type WorkloadConfig struct {
	Type           WorkloadType // "vllm", "tgi", "custom"
	ModelID        string       // HuggingFace model ID (e.g., "TinyLlama/TinyLlama-1.1B-Chat-v1.0")
	GPUMemoryUtil  float64      // GPU memory utilization (0.0-1.0, default 0.9)
	Quantization   string       // Quantization method (e.g., "awq", "gptq", "")
	MaxModelLen    int          // Maximum model context length
	TensorParallel int          // Number of GPUs for tensor parallelism
}

// Provider defines the interface for GPU cloud providers
type Provider interface {
	// Name returns the provider identifier ("vastai" | "tensordock")
	Name() string

	// ListOffers returns available GPU offers
	// Should respect rate limiting and return cached data if appropriate
	ListOffers(ctx context.Context, filter models.OfferFilter) ([]models.GPUOffer, error)

	// ListAllInstances returns all instances with our tags (for reconciliation)
	ListAllInstances(ctx context.Context) ([]ProviderInstance, error)

	// CreateInstance provisions a new GPU instance
	CreateInstance(ctx context.Context, req CreateInstanceRequest) (*InstanceInfo, error)

	// DestroyInstance tears down a GPU instance
	DestroyInstance(ctx context.Context, instanceID string) error

	// GetInstanceStatus returns current status of an instance
	GetInstanceStatus(ctx context.Context, instanceID string) (*InstanceStatus, error)

	// SupportsFeature checks if provider supports a specific feature
	SupportsFeature(feature ProviderFeature) bool
}

// TemplateProvider extends Provider with template management capabilities.
// Only providers that support templates (e.g., Vast.ai) implement this interface.
type TemplateProvider interface {
	Provider

	// ListTemplates returns available templates from the provider
	ListTemplates(ctx context.Context, filter models.TemplateFilter) ([]models.VastTemplate, error)

	// GetTemplate returns a specific template by hash_id
	GetTemplate(ctx context.Context, hashID string) (*models.VastTemplate, error)

	// GetCompatibleTemplates returns templates compatible with the given offer ID.
	// Compatibility is determined by matching template extra_filters against host properties.
	GetCompatibleTemplates(ctx context.Context, offerID string) ([]models.CompatibleTemplate, error)
}

// CreateInstanceRequest contains all data needed to provision an instance
type CreateInstanceRequest struct {
	OfferID      string            // Provider's offer/bundle ID
	SessionID    string            // Our session ID for tagging
	SSHPublicKey string            // SSH public key to inject
	DockerImage  string            // Docker image to run (our agent)
	EnvVars      map[string]string // Environment variables for the container
	OnStartCmd   string            // Command to run on startup
	Tags         models.InstanceTags

	// Dual launch mode support
	LaunchMode     LaunchMode      // "ssh" or "entrypoint" (default: ssh)
	Entrypoint     []string        // Container entrypoint args (for entrypoint mode)
	ExposedPorts   []int           // Ports to expose (e.g., 8000 for vLLM)
	WorkloadConfig *WorkloadConfig // Structured workload config (vllm, tgi, etc.)

	// Template-based provisioning (Vast.ai)
	// If TemplateHashID is set, use the template instead of building config from DockerImage/EnvVars
	TemplateHashID string // Vast.ai template hash_id (e.g., "4e17788f74f075dd9aab7d0d4427968f")

	// Storage configuration
	DiskGB int // Disk space in GB (cannot be changed after creation)
}

// InstanceInfo contains details about a provisioned instance
type InstanceInfo struct {
	ProviderInstanceID string
	SSHHost            string
	SSHPort            int
	SSHUser            string
	Status             string
	ActualPricePerHour float64 // Actual price (may differ from offer)

	// API endpoint info (for entrypoint mode)
	APIHost  string      // Public host for API access
	APIPort  int         // Port for API access (e.g., 8000 for vLLM)
	APIPorts map[int]int // Mapping of container port -> public port
}

// InstanceStatus represents the current state of a provider instance
type InstanceStatus struct {
	Status    string    // "running", "stopped", "starting", "error"
	Running   bool      // Convenience flag
	StartedAt time.Time // When the instance started
	Error     string    // Error message if status is "error"
	SSHHost   string    // SSH host (populated when running)
	SSHPort   int       // SSH port (populated when running)
	SSHUser   string    // SSH user (populated when running)

	// Port mappings for HTTP API access (entrypoint mode workloads)
	PublicIP string      // Public IP address of the instance
	Ports    map[int]int // Container port -> external port mapping (e.g., 8000 -> 33526)
}

// ProviderInstance represents an instance discovered during reconciliation
type ProviderInstance struct {
	ID           string
	Name         string // Instance name/label
	Status       string
	StartedAt    time.Time
	Tags         models.InstanceTags // Parsed from instance metadata
	PricePerHour float64
}

// IsOurs checks if this instance belongs to our shopper deployment
func (p ProviderInstance) IsOurs(deploymentID string) bool {
	return p.Tags.ShopperDeploymentID == deploymentID
}

// IsExpired checks if this instance is past its expiration time
func (p ProviderInstance) IsExpired() bool {
	return !p.Tags.ShopperExpiresAt.IsZero() && time.Now().After(p.Tags.ShopperExpiresAt)
}
