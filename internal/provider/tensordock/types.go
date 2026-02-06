package tensordock

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// Locations API Types (GET /locations)
// =============================================================================

// LocationsResponse is the response from GET /locations.
// This endpoint returns all data centers and their available GPU types.
// Authentication: Query parameters (api_key, api_token)
type LocationsResponse struct {
	Data LocationsData `json:"data"`
}

// LocationsData contains the locations array
type LocationsData struct {
	Locations []Location `json:"locations"`
}

// Location represents a TensorDock data center location.
// Each location has a unique UUID and contains multiple GPU types.
type Location struct {
	ID            string        `json:"id"`            // UUID, e.g., "1a779525-4c04-4f2c-aa45-58b47d54bb38"
	City          string        `json:"city"`          // e.g., "Chicago"
	StateProvince string        `json:"stateprovince"` // e.g., "Illinois"
	Country       string        `json:"country"`       // e.g., "United States"
	Tier          int           `json:"tier"`          // 1-3, higher is more reliable
	GPUs          []LocationGPU `json:"gpus"`          // Available GPU types at this location
}

// LocationGPU represents GPU availability at a location.
// WARNING: Availability data is frequently stale. GPUs shown as available
// may fail to provision with "No available nodes found".
type LocationGPU struct {
	V0Name          string          `json:"v0Name"`           // Internal name, e.g., "geforcertx3090-pcie-24gb"
	DisplayName     string          `json:"displayName"`      // Human name, e.g., "NVIDIA GeForce RTX 3090 PCIe 24GB"
	MaxCount        int             `json:"max_count"`        // Maximum GPUs available (often inaccurate)
	PricePerHr      float64         `json:"price_per_hr"`     // Price per hour in USD
	Resources       GPUResources    `json:"resources"`        // Resource limits
	NetworkFeatures NetworkFeatures `json:"network_features"` // Network capabilities
	Pricing         ResourcePricing `json:"pricing"`          // Per-resource pricing
}

// GPUResources describes resource limits for a GPU type
type GPUResources struct {
	MaxVCPUs     int `json:"max_vcpus"`
	MaxRAMGb     int `json:"max_ram_gb"`
	MaxStorageGb int `json:"max_storage_gb"`
}

// NetworkFeatures describes network capabilities at a location
type NetworkFeatures struct {
	DedicatedIPAvailable    bool `json:"dedicated_ip_available"`
	PortForwardingAvailable bool `json:"port_forwarding_available"`
	NetworkStorageAvailable bool `json:"network_storage_available"`
}

// ResourcePricing describes per-resource pricing (in addition to GPU cost)
type ResourcePricing struct {
	PerVCPUHr      float64 `json:"per_vcpu_hr"`
	PerGBRAMHr     float64 `json:"per_gb_ram_hr"`
	PerGBStorageHr float64 `json:"per_gb_storage_hr"`
}

// =============================================================================
// Instances List API Types (GET /instances)
// =============================================================================

// InstancesResponse is the response from GET /instances.
// NOTE: TensorDock's API is inconsistent - sometimes returns {"data": [...]}
// (array directly) and sometimes {"data": {"instances": [...]}}.
// The client handles both formats.
type InstancesResponse struct {
	Data InstancesData `json:"data"`
}

// InstancesData contains the instances array (nested format)
type InstancesData struct {
	Instances []Instance `json:"instances"`
}

// Instance represents a TensorDock VM instance from the list endpoint.
// Note: Field names use mixed casing (ipAddress vs gpu_model) - this matches the API.
type Instance struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`    // "running", "stopped", "creating", etc.
	IPAddress    string    `json:"ipAddress"` // camelCase in API response
	GPUModel     string    `json:"gpu_model"` // snake_case in API response
	GPUCount     int       `json:"gpu_count"`
	VCPUs        int       `json:"vcpus"`
	RAMGb        int       `json:"ram_gb"`
	StorageGb    int       `json:"storage_gb"`
	PricePerHour float64   `json:"price_per_hour"`
	CreatedAt    time.Time `json:"created_at"`
	LocationID   string    `json:"location_id"`
}

// =============================================================================
// Instance Detail API Types (GET /instances/{id})
// =============================================================================

// InstanceResponse is the response from GET /instances/{id}.
// NOTE: This endpoint returns the instance directly (NOT wrapped in "data").
// This is different from POST /instances which wraps the response.
//
// Example response:
//
//	{
//	  "type": "virtualmachine",
//	  "id": "468b716a-6747-4cbe-9f13-afc153a21c14",
//	  "name": "shopper-ssh-test-1234",
//	  "status": "running",
//	  "ipAddress": "174.94.145.71",
//	  "portForwards": [{"internal_port": 22, "external_port": 20456}],
//	  "rateHourly": 0.272999
//	}
type InstanceResponse struct {
	Type         string        `json:"type"`
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	IPAddress    string        `json:"ipAddress"`    // camelCase
	PortForwards []PortForward `json:"portForwards"` // camelCase
	RateHourly   float64       `json:"rateHourly"`   // Actual hourly rate
}

// =============================================================================
// Create Instance API Types (POST /instances)
// =============================================================================

// CreateInstanceRequest is the request body for creating an instance.
// TensorDock uses JSON:API style with data.type and data.attributes.
type CreateInstanceRequest struct {
	Data CreateInstanceData `json:"data"`
}

// CreateInstanceData wraps the create request attributes
type CreateInstanceData struct {
	Type       string                   `json:"type"` // Always "virtualmachine"
	Attributes CreateInstanceAttributes `json:"attributes"`
}

// CreateInstanceAttributes contains the instance configuration.
//
// Key behaviors discovered through testing:
//
//   - PortForwards: REQUIRED for Ubuntu VMs. TensorDock returns error
//     "SSH port (22) must be forwarded for Ubuntu VMs" if omitted.
//     TensorDock may assign a different external port than requested.
//
//   - SSHKey: Required field but TensorDock doesn't actually use it to
//     install the key. Must use CloudInit.RunCmd for reliable installation.
//
//   - CloudInit: Use runcmd with base64-encoded SSH key for reliable
//     key installation. The ssh_authorized_keys field doesn't work.
type CreateInstanceAttributes struct {
	Name           string          `json:"name"`                     // Instance name (use shopper label format)
	Type           string          `json:"type"`                     // Always "virtualmachine"
	Image          string          `json:"image"`                    // OS image: ubuntu2404, ubuntu2204, debian12, etc.
	LocationID     string          `json:"location_id"`              // Location UUID from /locations
	Resources      ResourcesConfig `json:"resources"`                // CPU, RAM, storage, GPUs
	PortForwards   []PortForward   `json:"port_forwards,omitempty"`  // Port forwarding rules (if not using dedicated IP)
	UseDedicatedIP bool            `json:"useDedicatedIp,omitempty"` // Request a dedicated public IP for direct port access
	SSHKey         string          `json:"ssh_key,omitempty"`        // Required but doesn't work - use CloudInit
	CloudInit      *CloudInit      `json:"cloud_init,omitempty"`     // For SSH key installation
}

// PortForward specifies a port forwarding rule.
// IMPORTANT: TensorDock may assign a different external port than requested.
// For example, you request internal:22 -> external:22, but receive external:20456.
// Always check GetInstanceStatus to get the actual assigned port.
type PortForward struct {
	Protocol     string `json:"protocol"`      // "tcp" or "udp"
	InternalPort int    `json:"internal_port"` // Port inside the VM
	ExternalPort int    `json:"external_port"` // Requested external port (may be changed by TensorDock)
}

// ResourcesConfig specifies instance resources
type ResourcesConfig struct {
	VCPUCount int                 `json:"vcpu_count"` // Number of vCPUs
	RAMGb     int                 `json:"ram_gb"`     // RAM in GB
	StorageGb int                 `json:"storage_gb"` // Storage in GB (minimum 100)
	GPUs      map[string]GPUCount `json:"gpus"`       // GPU type -> count mapping
}

// GPUCount specifies the count for a GPU model
type GPUCount struct {
	Count int `json:"count"`
}

// CloudInit contains cloud-init configuration.
//
// TensorDock uses standard cloud-init fields but with important caveats:
//
//   - ssh_authorized_keys: Does NOT work reliably on TensorDock
//   - write_files: Most reliable way to install SSH keys
//   - runcmd: For post-boot commands (permissions, etc.)
//
// Recommended SSH key installation pattern (using write_files + runcmd):
//
//	CloudInit{
//	    WriteFiles: []WriteFile{
//	        {
//	            Path:        "/root/.ssh/authorized_keys",
//	            Content:     "<base64-encoded-key>",
//	            Encoding:    "b64",
//	            Permissions: "0600",
//	            Owner:       "root:root",
//	        },
//	    },
//	    RunCmd: []string{
//	        "mkdir -p /root/.ssh",
//	        "chmod 700 /root/.ssh",
//	    },
//	}
//
// Cloud-init execution takes ~60-90 seconds after instance boot.
type CloudInit struct {
	Packages          []string    `json:"packages,omitempty"`
	PackageUpdate     bool        `json:"package_update,omitempty"`
	PackageUpgrade    bool        `json:"package_upgrade,omitempty"`
	WriteFiles        []WriteFile `json:"write_files,omitempty"`         // For file creation (most reliable for SSH keys)
	RunCmd            []string    `json:"runcmd,omitempty"`              // Commands to run after boot
	SSHAuthorizedKeys []string    `json:"ssh_authorized_keys,omitempty"` // Does NOT work - use WriteFiles
}

// WriteFile represents a file to be written by cloud-init.
// This is the most reliable method for SSH key installation on TensorDock.
type WriteFile struct {
	Path        string `json:"path"`                  // Absolute path to the file
	Content     string `json:"content"`               // File content (can be base64-encoded)
	Encoding    string `json:"encoding,omitempty"`    // "b64" for base64-encoded content
	Permissions string `json:"permissions,omitempty"` // File permissions (e.g., "0600")
	Owner       string `json:"owner,omitempty"`       // Owner in "user:group" format
}

// CreateInstanceResponse is the response from POST /instances.
// NOTE: Unlike GET /instances/{id}, this response IS wrapped in "data".
//
// Example response:
//
//	{
//	  "data": {
//	    "type": "virtualmachine",
//	    "id": "468b716a-6747-4cbe-9f13-afc153a21c14",
//	    "name": "shopper-ssh-test-1234",
//	    "status": "running"
//	  }
//	}
//
// IMPORTANT: The create response does NOT include the IP address.
// You must poll GetInstanceStatus to get the IP (typically 5-30 seconds).
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

// =============================================================================
// Helper Functions
// =============================================================================

// normalizeGPUName converts TensorDock GPU display names to standardized names.
// Examples:
//   - "NVIDIA GeForce RTX 4090 PCIe 24GB" -> "RTX 4090"
//   - "NVIDIA A100 PCIe 80GB" -> "A100"
//   - "GeForce RTX 3090 PCIe 24GB" -> "RTX 3090"
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

// parseVRAMFromName extracts VRAM in GB from a GPU display name.
// The search is case-insensitive, matching GB, Gb, gb, etc.
// Examples:
//   - "NVIDIA GeForce RTX 4090 PCIe 24GB" -> 24
//   - "NVIDIA A100 PCIe 80GB" -> 80
//   - "RTX 3090 24gb" -> 24
//   - "Some GPU 48Gb" -> 48
//   - "Some GPU" -> 0 (no VRAM found)
func parseVRAMFromName(name string) int {
	// Look for patterns like "24GB", "48GB", "24gb", "48Gb", etc. (case-insensitive)
	re := regexp.MustCompile(`(?i)(\d+)\s*GB`)
	matches := re.FindStringSubmatch(name)
	if len(matches) >= 2 {
		if vram, err := strconv.Atoi(matches[1]); err == nil {
			return vram
		}
	}
	return 0
}
