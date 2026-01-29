package tensordock

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LocationsResponse is the response from GET /locations
type LocationsResponse struct {
	Data LocationsData `json:"data"`
}

// LocationsData contains the locations array
type LocationsData struct {
	Locations []Location `json:"locations"`
}

// Location represents a TensorDock data center location
type Location struct {
	ID            string       `json:"id"`
	City          string       `json:"city"`
	StateProvince string       `json:"stateprovince"`
	Country       string       `json:"country"`
	Tier          int          `json:"tier"`
	GPUs          []LocationGPU `json:"gpus"`
}

// LocationGPU represents GPU availability at a location
type LocationGPU struct {
	V0Name          string           `json:"v0Name"`
	DisplayName     string           `json:"displayName"`
	MaxCount        int              `json:"max_count"`
	PricePerHr      float64          `json:"price_per_hr"`
	Resources       GPUResources     `json:"resources"`
	NetworkFeatures NetworkFeatures  `json:"network_features"`
	Pricing         ResourcePricing  `json:"pricing"`
}

// GPUResources describes resource limits for a GPU type
type GPUResources struct {
	MaxVCPUs     int `json:"max_vcpus"`
	MaxRAMGb     int `json:"max_ram_gb"`
	MaxStorageGb int `json:"max_storage_gb"`
}

// NetworkFeatures describes network capabilities
type NetworkFeatures struct {
	DedicatedIPAvailable     bool `json:"dedicated_ip_available"`
	PortForwardingAvailable  bool `json:"port_forwarding_available"`
	NetworkStorageAvailable  bool `json:"network_storage_available"`
}

// ResourcePricing describes per-resource pricing
type ResourcePricing struct {
	PerVCPUHr      float64 `json:"per_vcpu_hr"`
	PerGBRAMHr     float64 `json:"per_gb_ram_hr"`
	PerGBStorageHr float64 `json:"per_gb_storage_hr"`
}

// InstancesResponse is the response from GET /instances
type InstancesResponse struct {
	Data InstancesData `json:"data"`
}

// InstancesData contains the instances array
type InstancesData struct {
	Instances []Instance `json:"instances"`
}

// Instance represents a TensorDock VM instance
type Instance struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	IPAddress    string    `json:"ip_address"`
	GPUModel     string    `json:"gpu_model"`
	GPUCount     int       `json:"gpu_count"`
	VCPUs        int       `json:"vcpus"`
	RAMGb        int       `json:"ram_gb"`
	StorageGb    int       `json:"storage_gb"`
	PricePerHour float64   `json:"price_per_hour"`
	CreatedAt    time.Time `json:"created_at"`
	LocationID   string    `json:"location_id"`
}

// InstanceResponse is the response from GET /instances/{id}
type InstanceResponse struct {
	Data InstanceData `json:"data"`
}

// InstanceData wraps instance attributes
type InstanceData struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes InstanceAttributes `json:"attributes"`
}

// InstanceAttributes contains instance details
type InstanceAttributes struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	IPAddress string    `json:"ip_address"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateInstanceRequest is the request body for creating an instance
type CreateInstanceRequest struct {
	Data CreateInstanceData `json:"data"`
}

// CreateInstanceData wraps the create request
type CreateInstanceData struct {
	Type       string                   `json:"type"`
	Attributes CreateInstanceAttributes `json:"attributes"`
}

// CreateInstanceAttributes contains the instance configuration
type CreateInstanceAttributes struct {
	Name       string          `json:"name"`
	Image      string          `json:"image"`
	LocationID string          `json:"location_id"`
	Resources  ResourcesConfig `json:"resources"`
	SSHKey     string          `json:"ssh_key,omitempty"`
	CloudInit  *CloudInit      `json:"cloud_init,omitempty"`
}

// ResourcesConfig specifies instance resources
type ResourcesConfig struct {
	VCPUCount int        `json:"vcpu_count"`
	RAMGb     int        `json:"ram_gb"`
	StorageGb int        `json:"storage_gb"`
	GPUs      GPUsConfig `json:"gpus"`
}

// GPUsConfig specifies GPU configuration
type GPUsConfig struct {
	Model string `json:"model"`
	Count int    `json:"count"`
}

// CloudInit contains cloud-init configuration
type CloudInit struct {
	Commands []string `json:"commands,omitempty"`
}

// CreateInstanceResponse is the response from POST /instances
type CreateInstanceResponse struct {
	Data CreateInstanceResponseData `json:"data"`
}

// CreateInstanceResponseData contains the created instance info
type CreateInstanceResponseData struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes InstanceAttributes `json:"attributes"`
}

// normalizeGPUName converts TensorDock GPU names to standardized names
func normalizeGPUName(name string) string {
	name = strings.TrimSpace(name)

	// Remove common prefixes
	prefixes := []string{"NVIDIA ", "GeForce ", "Tesla "}
	for _, prefix := range prefixes {
		name = strings.TrimPrefix(name, prefix)
	}

	// Remove VRAM suffix (e.g., " PCIe 24GB")
	re := regexp.MustCompile(`\s*PCIe\s*\d+GB$`)
	name = re.ReplaceAllString(name, "")

	return name
}

// parseVRAMFromName extracts VRAM in GB from display name
func parseVRAMFromName(name string) int {
	// Look for patterns like "24GB", "48GB", etc.
	re := regexp.MustCompile(`(\d+)\s*GB`)
	matches := re.FindStringSubmatch(name)
	if len(matches) >= 2 {
		if vram, err := strconv.Atoi(matches[1]); err == nil {
			return vram
		}
	}
	return 0
}
