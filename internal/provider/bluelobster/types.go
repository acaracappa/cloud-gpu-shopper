package bluelobster

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// BlueLobsterAvailabilityConfidence is the confidence level for Blue Lobster offers.
// Blue Lobster provides real-time availability data, so confidence is high.
const BlueLobsterAvailabilityConfidence = 1.0

// =============================================================================
// Availability API Types (GET /available)
// =============================================================================

// AvailableResponse is the response from the availability endpoint.
type AvailableResponse struct {
	Data []AvailableInstance `json:"data"`
}

// AvailableInstance represents an available instance type with its regions.
type AvailableInstance struct {
	ID           string       `json:"id"`
	InstanceType InstanceType `json:"instance_type"`
	Regions      []Region     `json:"regions_with_capacity_available"`
}

// InstanceType describes a Blue Lobster instance configuration.
type InstanceType struct {
	Name              string       `json:"name"`
	Description       string       `json:"description"`
	GPUDescription    string       `json:"gpu_description"`
	PriceCentsPerHour int          `json:"price_cents_per_hour"`
	Specs             InstanceSpec `json:"specs"`
}

// InstanceSpec describes the hardware specifications of an instance type.
// GPUModel is polymorphic: the API may return a string or []string.
type InstanceSpec struct {
	VCPUs      int             `json:"vcpus"`
	MemoryGiB  int             `json:"memory_gib"`
	StorageGiB int             `json:"storage_gib"`
	GPUs       int             `json:"gpus"`
	GPUModel   json.RawMessage `json:"gpu_model"`
}

// ParseGPUModel extracts the GPU model name from the polymorphic GPUModel field.
// It tries to unmarshal as a string first, then as []string (returning the first element).
// Returns empty string if no GPU model can be determined.
func (s *InstanceSpec) ParseGPUModel() string {
	if len(s.GPUModel) == 0 {
		return ""
	}

	// Try as a plain string first
	var model string
	if err := json.Unmarshal(s.GPUModel, &model); err == nil {
		return model
	}

	// Try as []string
	var models []string
	if err := json.Unmarshal(s.GPUModel, &models); err == nil && len(models) > 0 {
		return models[0]
	}

	return ""
}

// Region represents a geographic region where instances are available.
type Region struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Location    RegionLocation `json:"location"`
}

// RegionLocation provides geographic details for a region.
type RegionLocation struct {
	City    string `json:"city"`
	State   string `json:"state"`
	Country string `json:"country"`
}

// =============================================================================
// Launch Instance API Types (POST /instances)
// =============================================================================

// LaunchInstanceRequest is the request body for launching a new instance.
type LaunchInstanceRequest struct {
	Region       string            `json:"region"`
	InstanceType string            `json:"instance_type"`
	Username     string            `json:"username"`
	SSHKey       string            `json:"ssh_key,omitempty"`
	Name         string            `json:"name,omitempty"`
	TemplateName string            `json:"template_name,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// LaunchInstanceResponse is the response from launching an instance.
type LaunchInstanceResponse struct {
	Data LaunchData `json:"data"`
}

// LaunchData contains the result of a launch request.
type LaunchData struct {
	InstanceIDs []string `json:"instance_ids"`
	TaskID      string   `json:"task_id"`
	AssignedIP  string   `json:"assigned_ip"`
	Status      string   `json:"status"`
}

// =============================================================================
// Task API Types (GET /tasks/{id})
// =============================================================================

// TaskResponse represents the status of an asynchronous task.
type TaskResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
	Params    struct {
		VMUUID string `json:"vm_uuid"`
	} `json:"params"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// =============================================================================
// VM Instance API Types (GET /instances/{id})
// =============================================================================

// VMInstance represents a running Blue Lobster VM instance.
type VMInstance struct {
	UUID              string            `json:"uuid"`
	Name              string            `json:"name"`
	HostID            string            `json:"host_id"`
	Region            string            `json:"region"`
	IPAddress         string            `json:"ip_address"`
	InternalIP        string            `json:"internal_ip"`
	CPUCores          int               `json:"cpu_cores"`
	Memory            int               `json:"memory"`
	Storage           int               `json:"storage"`
	GPUCount          int               `json:"gpu_count"`
	GPUModel          string            `json:"gpu_model"`
	PowerStatus       string            `json:"power_status"`
	CreatedAt         string            `json:"created_at"`
	Metadata          map[string]string `json:"metadata"`
	InstanceType      string            `json:"instance_type"`
	PriceCentsPerHour int               `json:"price_cents_per_hour"`
	VMUsername        string            `json:"vm_username"`
}

// =============================================================================
// Delete Instance API Types (DELETE /instances/{id})
// =============================================================================

// DeleteInstanceResponse is the response from deleting an instance.
type DeleteInstanceResponse struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	InstanceID string `json:"instance_id"`
}

// =============================================================================
// Error Response Types
// =============================================================================

// ErrorResponse represents an API error.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ErrorDetailResponse wraps an ErrorResponse in a detail field.
type ErrorDetailResponse struct {
	Detail ErrorResponse `json:"detail"`
}

// =============================================================================
// Conversion Methods
// =============================================================================

// ToGPUOffer converts an AvailableInstance and a specific Region to a unified GPUOffer.
// The offer ID format is "bluelobster:{instance_type_name}:{region_name}".
func (a AvailableInstance) ToGPUOffer(region Region) models.GPUOffer {
	gpuModel := a.InstanceType.Specs.ParseGPUModel()
	gpuName := normalizeGPUName(gpuModel)
	vram := parseVRAMFromDescription(a.InstanceType.GPUDescription)

	// Build location string from region location
	location := region.Location.City
	if region.Location.State != "" {
		location += ", " + region.Location.State
	}
	if region.Location.Country != "" {
		location += ", " + region.Location.Country
	}

	// Price is in cents per hour; convert to dollars
	pricePerHour := float64(a.InstanceType.PriceCentsPerHour) / 100.0

	offerID := fmt.Sprintf("bluelobster:%s:%s", a.InstanceType.Name, region.Name)

	return models.GPUOffer{
		ID:                     offerID,
		Provider:               "bluelobster",
		ProviderID:             a.ID,
		GPUType:                gpuName,
		GPUCount:               a.InstanceType.Specs.GPUs,
		VRAM:                   vram,
		PricePerHour:           pricePerHour,
		Location:               location,
		Reliability:            0,
		Available:              true,
		MaxDuration:            0,
		FetchedAt:              time.Now(),
		AvailabilityConfidence: BlueLobsterAvailabilityConfidence,
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// normalizeGPUName strips common vendor prefixes from GPU names for consistency.
// Examples:
//   - "NVIDIA RTX A5000" -> "RTX A5000"
//   - "GeForce RTX 4090" -> "RTX 4090"
//   - "Quadro RTX 6000"  -> "RTX 6000"
func normalizeGPUName(name string) string {
	name = strings.TrimSpace(name)
	prefixes := []string{"NVIDIA ", "GeForce ", "Quadro "}
	for _, prefix := range prefixes {
		name = strings.TrimPrefix(name, prefix)
	}
	return name
}

// parseVRAMFromDescription extracts VRAM in GB from a GPU description string.
// It looks for a number immediately before " GB)" in strings like "1x RTX A5000 (24 GB)".
// Returns 0 if no VRAM can be determined.
func parseVRAMFromDescription(desc string) int {
	// Look for pattern: number followed by " GB)"
	idx := strings.Index(desc, " GB)")
	if idx < 0 {
		return 0
	}

	// Walk backwards from the space before "GB)" to find the number
	numStr := ""
	for i := idx - 1; i >= 0; i-- {
		c := desc[i]
		if c >= '0' && c <= '9' {
			numStr = string(c) + numStr
		} else {
			break
		}
	}

	if numStr == "" {
		return 0
	}

	vram := 0
	for _, c := range numStr {
		vram = vram*10 + int(c-'0')
	}
	return vram
}

// parseOfferID splits a Blue Lobster offer ID into its components.
// Offer IDs have the format "bluelobster:{instance_type}:{region}".
// Returns an error if the format is invalid.
func parseOfferID(offerID string) (instanceType, region string, err error) {
	parts := strings.SplitN(offerID, ":", 3)
	if len(parts) != 3 || parts[0] != "bluelobster" {
		return "", "", fmt.Errorf("invalid Blue Lobster offer ID: %s", offerID)
	}
	return parts[1], parts[2], nil
}
