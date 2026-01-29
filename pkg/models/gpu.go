package models

import "time"

// GPUOffer represents an available GPU instance for rent
type GPUOffer struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`       // "vastai" | "tensordock"
	ProviderID   string    `json:"provider_id"`    // Provider's ID for this offer
	GPUType      string    `json:"gpu_type"`       // "RTX 4090", "A100", etc.
	GPUCount     int       `json:"gpu_count"`      // Number of GPUs
	VRAM         int       `json:"vram_gb"`        // VRAM in GB
	PricePerHour float64   `json:"price_per_hour"` // USD per hour
	Location     string    `json:"location"`       // Geographic location
	Reliability  float64   `json:"reliability"`    // 0-1 score if available
	Available    bool      `json:"available"`      // Currently available
	MaxDuration  int       `json:"max_duration_hours"` // 0 = unlimited
	FetchedAt    time.Time `json:"fetched_at"`     // When this offer was fetched
}

// OfferFilter defines criteria for filtering GPU offers
type OfferFilter struct {
	Provider       string  `json:"provider,omitempty"`        // Filter by provider
	GPUType        string  `json:"gpu_type,omitempty"`        // Filter by GPU type
	MinVRAM        int     `json:"min_vram,omitempty"`        // Minimum VRAM in GB
	MaxPrice       float64 `json:"max_price,omitempty"`       // Maximum price per hour
	Location       string  `json:"location,omitempty"`        // Region/location filter
	MinReliability float64 `json:"min_reliability,omitempty"` // Minimum reliability score
	MinGPUCount    int     `json:"min_gpu_count,omitempty"`   // Minimum GPU count
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
	return true
}
