package models

import "time"

// CompatibleTemplate represents a template that can run on a specific GPU offer.
// Used to show users which templates are compatible with each offer.
type CompatibleTemplate struct {
	HashID string `json:"hash_id"`
	Name   string `json:"name"`
	Image  string `json:"image,omitempty"`
}

// GPUOffer represents an available GPU instance for rent
type GPUOffer struct {
	ID                     string    `json:"id"`
	Provider               string    `json:"provider"`                // "vastai" | "tensordock"
	ProviderID             string    `json:"provider_id"`             // Provider's ID for this offer
	GPUType                string    `json:"gpu_type"`                // "RTX 4090", "A100", etc.
	GPUCount               int       `json:"gpu_count"`               // Number of GPUs
	VRAM                   int       `json:"vram_gb"`                 // VRAM in GB
	PricePerHour           float64   `json:"price_per_hour"`          // USD per hour
	Location               string    `json:"location"`                // Geographic location
	Reliability            float64   `json:"reliability"`             // 0-1 score if available
	Available              bool      `json:"available"`               // Currently available
	MaxDuration            int       `json:"max_duration_hours"`      // 0 = unlimited
	FetchedAt              time.Time `json:"fetched_at"`              // When this offer was fetched
	AvailabilityConfidence float64   `json:"availability_confidence"` // 0-1 confidence that offer is actually available (default 1.0)
	CUDAVersion            float64   `json:"cuda_version,omitempty"`  // Max supported CUDA version (e.g., 12.9). Only for Vast.ai.

	// CompatibleTemplates lists templates that can run on this offer.
	// Only populated when include_templates=true is requested, and only for Vast.ai offers.
	CompatibleTemplates []CompatibleTemplate `json:"compatible_templates,omitempty"`
}

// OfferFilter defines criteria for filtering GPU offers
type OfferFilter struct {
	Provider                  string  `json:"provider,omitempty"`                    // Filter by provider
	GPUType                   string  `json:"gpu_type,omitempty"`                    // Filter by GPU type
	MinVRAM                   int     `json:"min_vram,omitempty"`                    // Minimum VRAM in GB
	MaxPrice                  float64 `json:"max_price,omitempty"`                   // Maximum price per hour
	Location                  string  `json:"location,omitempty"`                    // Region/location filter
	MinReliability            float64 `json:"min_reliability,omitempty"`             // Minimum reliability score
	MinGPUCount               int     `json:"min_gpu_count,omitempty"`               // Minimum GPU count
	MinAvailabilityConfidence float64 `json:"min_availability_confidence,omitempty"` // Minimum availability confidence (0-1)
	MinCUDAVersion            float64 `json:"min_cuda_version,omitempty"`            // Minimum CUDA version (e.g., 12.9)
}

// MatchesFilter checks if the offer matches the given filter
func (o *GPUOffer) MatchesFilter(f OfferFilter) bool {
	if f.Provider != "" && o.Provider != f.Provider {
		return false
	}
	if f.GPUType != "" && o.GPUType != f.GPUType {
		return false
	}
	if f.MinVRAM > 0 && o.VRAM < f.MinVRAM {
		return false
	}
	if f.MaxPrice > 0 && o.PricePerHour > f.MaxPrice {
		return false
	}
	if f.Location != "" && o.Location != f.Location {
		return false
	}
	if f.MinReliability > 0 && o.Reliability < f.MinReliability {
		return false
	}
	if f.MinGPUCount > 0 && o.GPUCount < f.MinGPUCount {
		return false
	}
	if f.MinAvailabilityConfidence > 0 && o.AvailabilityConfidence < f.MinAvailabilityConfidence {
		return false
	}
	if f.MinCUDAVersion > 0 && o.CUDAVersion < f.MinCUDAVersion {
		return false
	}
	return true
}

// GetEffectiveAvailabilityConfidence returns the availability confidence,
// defaulting to 1.0 if not explicitly set (for backwards compatibility)
func (o *GPUOffer) GetEffectiveAvailabilityConfidence() float64 {
	if o.AvailabilityConfidence == 0 {
		return 1.0
	}
	return o.AvailabilityConfidence
}
