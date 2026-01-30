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
	ID            string        `json:"id"`
	City          string        `json:"city"`
	StateProvince string        `json:"stateprovince"`
	Country       string        `json:"country"`
	Tier          int           `json:"tier"`
	GPUs          []LocationGPU `json:"gpus"`
}

// LocationGPU represents GPU availability at a location
type LocationGPU struct {
	V0Name          string          `json:"v0Name"`
	DisplayName     string          `json:"displayName"`
	MaxCount        int             `json:"max_count"`
	PricePerHr      float64         `json:"price_per_hr"`
	Resources       GPUResources    `json:"resources"`
	NetworkFeatures NetworkFeatures `json:"network_features"`
	Pricing         ResourcePricing `json:"pricing"`
}

// GPUResources describes resource limits for a GPU type
type GPUResources struct {
	MaxVCPUs     int `json:"max_vcpus"`
	MaxRAMGb     int `json:"max_ram_gb"`
	MaxStorageGb int `json:"max_storage_gb"`
}

// NetworkFeatures describes network capabilities
type NetworkFeatures struct {
	DedicatedIPAvailable    bool `json:"dedicated_ip_available"`
	PortForwardingAvailable bool `json:"port_forwarding_available"`
	NetworkStorageAvailable bool `json:"network_storage_available"`
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
// Note: TensorDock API uses camelCase for some fields
type Instance struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	IPAddress    string    `json:"ipAddress"` // camelCase in API response
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
// Note: This endpoint returns the instance directly, NOT wrapped in "data"
type InstanceResponse struct {
	Type         string        `json:"type"`
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	IPAddress    string        `json:"ipAddress"` // camelCase in API response
	PortForwards []PortForward `json:"portForwards"`
	RateHourly   float64       `json:"rateHourly"`
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
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Image        string          `json:"image"`
	LocationID   string          `json:"location_id"`
	Resources    ResourcesConfig `json:"resources"`
	PortForwards []PortForward   `json:"port_forwards"`
	SSHKey       string          `json:"ssh_key,omitempty"`
	CloudInit    *CloudInit      `json:"cloud_init,omitempty"`
}

// PortForward specifies a port forwarding rule
type PortForward struct {
	Protocol     string `json:"protocol"`
	InternalPort int    `json:"internal_port"`
	ExternalPort int    `json:"external_port"`
}

// ResourcesConfig specifies instance resources
type ResourcesConfig struct {
	VCPUCount int                   `json:"vcpu_count"`
	RAMGb     int                   `json:"ram_gb"`
	StorageGb int                   `json:"storage_gb"`
	GPUs      map[string]GPUCount   `json:"gpus"`
}

// GPUCount specifies the count for a GPU model
type GPUCount struct {
	Count int `json:"count"`
}

// CloudInit contains cloud-init configuration
// TensorDock uses standard cloud-init fields: runcmd, packages, write_files, etc.
// Note: TensorDock's ssh_key API field doesn't work - must use ssh_authorized_keys in cloud-init
type CloudInit struct {
	Packages          []string `json:"packages,omitempty"`
	PackageUpdate     bool     `json:"package_update,omitempty"`
	PackageUpgrade    bool     `json:"package_upgrade,omitempty"`
	RunCmd            []string `json:"runcmd,omitempty"`
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys,omitempty"`
}

// CreateInstanceResponse is the response from POST /instances
// Note: Response is wrapped in "data" but has no "attributes" nesting
type CreateInstanceResponse struct {
	Data CreateInstanceResponseData `json:"data"`
}

// CreateInstanceResponseData contains the created instance info
type CreateInstanceResponseData struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
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
