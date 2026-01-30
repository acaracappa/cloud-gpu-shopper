package provider

import (
	"context"
	"errors"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// Common errors returned by providers
var (
	ErrProviderRateLimit = errors.New("provider rate limit exceeded")
	ErrProviderAuth      = errors.New("provider authentication failed")
	ErrInstanceNotFound  = errors.New("instance not found")
	ErrOfferUnavailable  = errors.New("offer no longer available")
	ErrProviderError     = errors.New("provider API error")
	ErrInvalidResponse   = errors.New("invalid provider response")
)

// ProviderFeature represents optional features a provider may support
type ProviderFeature string

const (
	FeatureIdleDetection ProviderFeature = "idle_detection"
	FeatureInstanceTags  ProviderFeature = "instance_tags"
	FeatureSpotPricing   ProviderFeature = "spot_pricing"
	FeatureCustomImages  ProviderFeature = "custom_images"
)

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

// CreateInstanceRequest contains all data needed to provision an instance
type CreateInstanceRequest struct {
	OfferID      string            // Provider's offer/bundle ID
	SessionID    string            // Our session ID for tagging
	SSHPublicKey string            // SSH public key to inject
	DockerImage  string            // Docker image to run (our agent)
	EnvVars      map[string]string // Environment variables for the container
	OnStartCmd   string            // Command to run on startup
	Tags         models.InstanceTags
}

// InstanceInfo contains details about a provisioned instance
type InstanceInfo struct {
	ProviderInstanceID string
	SSHHost            string
	SSHPort            int
	SSHUser            string
	Status             string
	ActualPricePerHour float64 // Actual price (may differ from offer)
}

// InstanceStatus represents the current state of a provider instance
type InstanceStatus struct {
	Status    string    // "running", "stopped", "starting", "error"
	Running   bool      // Convenience flag
	StartedAt time.Time // When the instance started
	Error     string    // Error message if status is "error"
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
