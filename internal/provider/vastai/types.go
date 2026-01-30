package vastai

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// BundlesResponse is the response from GET /bundles/
type BundlesResponse struct {
	Offers []Bundle `json:"offers"`
}

// Bundle represents a Vast.ai GPU offer
type Bundle struct {
	ID            int `json:"id"`
	AskContractID int `json:"ask_contract_id"`
	BundleID      int `json:"bundle_id"`
	MachineID     int `json:"machine_id"`
	HostID        int `json:"host_id"`

	// GPU info
	GPUName     string  `json:"gpu_name"`
	GPURam      float64 `json:"gpu_ram"`       // MB
	GPUTotalRam float64 `json:"gpu_total_ram"` // MB
	NumGPUs     int     `json:"num_gpus"`
	GPUFrac     float64 `json:"gpu_frac"`
	GPUArch     string  `json:"gpu_arch"`
	ComputeCap  int     `json:"compute_cap"`

	// CPU info
	CPUName           string  `json:"cpu_name"`
	CPUCores          int     `json:"cpu_cores"`
	CPUCoresEffective float64 `json:"cpu_cores_effective"`
	CPUGhz            float64 `json:"cpu_ghz"`
	CPURam            int     `json:"cpu_ram"` // MB
	CPUArch           string  `json:"cpu_arch"`

	// Storage
	DiskSpace float64 `json:"disk_space"` // GB
	DiskName  string  `json:"disk_name"`
	DiskBw    float64 `json:"disk_bw"`

	// Network
	InetDown     float64 `json:"inet_down"`
	InetUp       float64 `json:"inet_up"`
	InetDownCost float64 `json:"inet_down_cost"`
	InetUpCost   float64 `json:"inet_up_cost"`

	// Pricing
	DphBase     float64 `json:"dph_base"`  // Base price per hour
	DphTotal    float64 `json:"dph_total"` // Total price per hour
	MinBid      float64 `json:"min_bid"`
	StorageCost float64 `json:"storage_cost"`

	// Location
	Geolocation string `json:"geolocation"`
	Geolocode   int    `json:"geolocode"`

	// Status
	Rentable    bool    `json:"rentable"`
	Rented      bool    `json:"rented"`
	Reliability float64 `json:"reliability2"` // Note: reliability2 is the correct field

	// Other
	DriverVersion string  `json:"driver_version"`
	OSVersion     string  `json:"os_version"`
	CudaMaxGood   float64 `json:"cuda_max_good"`
	Verified      bool    `json:"verified"`
	Verification  string  `json:"verification"`
	StaticIP      bool    `json:"static_ip"`
	PublicIPAddr  string  `json:"public_ipaddr"`
	StartDate     float64 `json:"start_date"`
	EndDate       float64 `json:"end_date"`
	Duration      float64 `json:"duration"`
}

// VastAIAvailabilityConfidence is the confidence level for Vast.ai offers.
// Vast.ai's inventory is generally more accurate than other providers,
// with real-time availability tracking.
const VastAIAvailabilityConfidence = 0.9

// ToGPUOffer converts a Vast.ai Bundle to a unified GPUOffer
func (b Bundle) ToGPUOffer() models.GPUOffer {
	return models.GPUOffer{
		ID:           fmt.Sprintf("vastai-%d", b.ID),
		Provider:     "vastai",
		ProviderID:   fmt.Sprintf("%d", b.ID),
		GPUType:      normalizeGPUName(b.GPUName),
		GPUCount:     b.NumGPUs,
		VRAM:         int(b.GPURam / 1024), // Convert MB to GB
		PricePerHour: b.DphTotal,
		Location:     b.Geolocation,
		Reliability:  b.Reliability,
		Available:    b.Rentable && !b.Rented,
		MaxDuration:  0, // Vast.ai doesn't have max duration
		FetchedAt:    time.Now(),
		// Vast.ai inventory is generally reliable
		AvailabilityConfidence: VastAIAvailabilityConfidence,
	}
}

// InstancesResponse is the response from GET /instances/
type InstancesResponse struct {
	Instances []Instance `json:"instances"`
}

// Instance represents a Vast.ai instance
type Instance struct {
	ID             int    `json:"id"`
	MachineID      int    `json:"machine_id"`
	HostID         int    `json:"host_id"`
	Label          string `json:"label"`
	ActualStatus   string `json:"actual_status"`
	IntendedStatus string `json:"intended_status"`
	CurState       string `json:"cur_state"`

	// Connection info
	SSHHost  string `json:"ssh_host"`
	SSHPort  int    `json:"ssh_port"`
	PublicIP string `json:"public_ipaddr"`

	// GPU info
	GPUName string  `json:"gpu_name"`
	NumGPUs int     `json:"num_gpus"`
	GPURam  float64 `json:"gpu_ram"`

	// Pricing
	DphTotal float64 `json:"dph_total"`

	// Timing
	StartDate float64 `json:"start_date"`
	Duration  float64 `json:"duration"`

	// Image
	ImageUUID    string `json:"image_uuid"`
	ImageRuntype string `json:"image_runtype"`
}

// CreateInstanceRequest is the request body for creating an instance
type CreateInstanceRequest struct {
	ClientID  string            `json:"client_id"`
	Image     string            `json:"image"`
	DiskSpace int               `json:"disk"`
	Label     string            `json:"label"`
	OnStart   string            `json:"onstart,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	RunType   string            `json:"runtype,omitempty"`
}

// CreateInstanceResponse is the response from creating an instance
type CreateInstanceResponse struct {
	Success     bool   `json:"success"`
	NewContract int    `json:"new_contract"`
	Error       string `json:"error,omitempty"`
}

// normalizeGPUName converts Vast.ai GPU names to standardized names
func normalizeGPUName(name string) string {
	name = strings.TrimSpace(name)

	// Common normalizations
	replacements := map[string]string{
		"GeForce RTX ": "RTX ",
		"NVIDIA ":      "",
		"Tesla ":       "",
	}

	for old, new := range replacements {
		name = strings.ReplaceAll(name, old, new)
	}

	return name
}
