package vastai

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// Note: models import is used for both GPUOffer and VastTemplate conversions

// Predefined Docker images for different launch modes
const (
	// ImageSSHBase is the default SSH-enabled base image for interactive access
	// Using NVIDIA CUDA Ubuntu 22.04 image - stable and widely supported on Vast.ai
	ImageSSHBase = "nvidia/cuda:12.2.0-runtime-ubuntu22.04"

	// ImageVLLM is the vLLM inference server image (official)
	ImageVLLM = "vllm/vllm-openai:latest"

	// ImageTGI is the Text Generation Inference server image
	ImageTGI = "ghcr.io/huggingface/text-generation-inference:latest"

	// ImageOllama is the Ollama server image
	ImageOllama = "ollama/ollama:latest"
)

// Default ports for inference servers
const (
	DefaultVLLMPort = 8000
	DefaultTGIPort  = 80
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

// ToHostProperties converts a Bundle to a map of properties for template filter matching.
// Property names match those used in template extra_filters JSON.
func (b Bundle) ToHostProperties() map[string]interface{} {
	return map[string]interface{}{
		"cuda_max_good":      b.CudaMaxGood,
		"compute_cap":        b.ComputeCap,
		"gpu_total_ram":      b.GPUTotalRam, // MB
		"gpu_ram":            b.GPURam,      // MB (effective available)
		"cpu_arch":           b.CPUArch,
		"num_gpus":           strconv.Itoa(b.NumGPUs),
		"gpu_name":           b.GPUName,
		"disk_space":         b.DiskSpace,
		"reliability2":       b.Reliability,
		"verified":           b.Verified,
		"rentable":           b.Rentable,
		"static_ip":          b.StaticIP,
		"driver_version":     b.DriverVersion,
		"inet_down":          b.InetDown,
		"inet_up":            b.InetUp,
		"dph_total":          b.DphTotal,
		"geolocation":        b.Geolocation,
		"cpu_cores_effective": b.CPUCoresEffective,
	}
}

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

// PortBinding represents a Docker-style port binding from Vast.ai
// Example: {"HostIp": "0.0.0.0", "HostPort": "33526"}
type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
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

	// Port mappings - Vast.ai returns Docker-style port bindings
	// Format: {"8000/tcp": [{"HostIp": "0.0.0.0", "HostPort": "33526"}]}
	Ports map[string][]PortBinding `json:"ports"`

	// Direct port info (for machines with open ports)
	DirectPortCount int `json:"direct_port_count"`
	DirectPortStart int `json:"direct_port_start"`
	DirectPortEnd   int `json:"direct_port_end"`

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

	// Jupyter access
	JupyterURL   string `json:"jupyter_url"`
	JupyterToken string `json:"jupyter_token"`
}

// CreateInstanceRequest is the request body for creating an instance
type CreateInstanceRequest struct {
	ClientID  string            `json:"client_id"`
	Image     string            `json:"image,omitempty"`
	DiskSpace int               `json:"disk"`
	Label     string            `json:"label"`
	OnStart   string            `json:"onstart,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	RunType   string            `json:"runtype,omitempty"`
	SSHKey    string            `json:"ssh_key,omitempty"` // SSH public key for instance access

	// Entrypoint mode fields
	Args  string `json:"args,omitempty"`  // Container arguments (for runtype=args)
	Ports string `json:"ports,omitempty"` // Port mappings (e.g., "8000/http")

	// Template-based provisioning
	// If TemplateHashID is set, use the template instead of building config from Image/Env
	// Request params override template defaults. Env vars are merged (request wins conflicts).
	TemplateHashID string `json:"template_hash_id,omitempty"`
}

// CreateInstanceResponse is the response from creating an instance
type CreateInstanceResponse struct {
	Success     bool   `json:"success"`
	NewContract int    `json:"new_contract"`
	Error       string `json:"error,omitempty"`
}

// TemplatesResponse is the response from GET /template/
type TemplatesResponse struct {
	Templates []Template `json:"templates"`
}

// Template represents a Vast.ai template from the API
type Template struct {
	ID                   int     `json:"id"`
	HashID               string  `json:"hash_id"`
	Name                 string  `json:"name"`
	Image                string  `json:"image"`
	Tag                  string  `json:"tag,omitempty"`
	Env                  string  `json:"env,omitempty"`
	OnStart              string  `json:"onstart,omitempty"`
	RunType              string  `json:"runtype,omitempty"`
	ArgsStr              string  `json:"args_str,omitempty"`
	UseSSH               bool    `json:"use_ssh"`
	SSHDirect            bool    `json:"ssh_direct"`
	Recommended          bool    `json:"recommended"`
	RecommendedDiskSpace float64 `json:"recommended_disk_space,omitempty"`
	ExtraFilters         string  `json:"extra_filters,omitempty"`
	CreatorID            int     `json:"creator_id,omitempty"`
	CreatedAt            float64 `json:"created_at,omitempty"` // Unix timestamp
	CountCreated         int     `json:"count_created,omitempty"`
}

// ToModel converts the API Template to a models.VastTemplate
func (t Template) ToModel() models.VastTemplate {
	var createdAt time.Time
	if t.CreatedAt > 0 {
		createdAt = time.Unix(int64(t.CreatedAt), 0)
	}
	return models.VastTemplate{
		ID:                   t.ID,
		HashID:               t.HashID,
		Name:                 t.Name,
		Image:                t.Image,
		Tag:                  t.Tag,
		Env:                  t.Env,
		OnStart:              t.OnStart,
		RunType:              t.RunType,
		ArgsStr:              t.ArgsStr,
		UseSSH:               t.UseSSH,
		SSHDirect:            t.SSHDirect,
		Recommended:          t.Recommended,
		RecommendedDiskSpace: int(t.RecommendedDiskSpace),
		ExtraFilters:         t.ExtraFilters,
		CreatorID:            t.CreatorID,
		CreatedAt:            createdAt,
		CountCreated:         t.CountCreated,
	}
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

// BuildVLLMArgs builds container arguments for vLLM server
func BuildVLLMArgs(config *provider.WorkloadConfig) string {
	if config == nil || config.ModelID == "" {
		return ""
	}

	args := []string{
		"--model", config.ModelID,
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", DefaultVLLMPort),
	}

	// GPU memory utilization (default 0.9)
	gpuMemUtil := config.GPUMemoryUtil
	if gpuMemUtil <= 0 {
		gpuMemUtil = 0.9
	}
	args = append(args, "--gpu-memory-utilization", fmt.Sprintf("%.2f", gpuMemUtil))

	// Quantization
	if config.Quantization != "" {
		args = append(args, "--quantization", config.Quantization)
	}

	// Max model length
	if config.MaxModelLen > 0 {
		args = append(args, "--max-model-len", fmt.Sprintf("%d", config.MaxModelLen))
	}

	// Tensor parallelism
	if config.TensorParallel > 1 {
		args = append(args, "--tensor-parallel-size", fmt.Sprintf("%d", config.TensorParallel))
	}

	return strings.Join(args, " ")
}

// BuildVLLMEnvVars builds environment variables for vLLM template deployment
// This is the preferred method for Vast.ai vLLM template which uses VLLM_MODEL and VLLM_ARGS
func BuildVLLMEnvVars(config *provider.WorkloadConfig) map[string]string {
	if config == nil || config.ModelID == "" {
		return nil
	}

	env := make(map[string]string)

	// Primary env var: model to load
	env["VLLM_MODEL"] = config.ModelID

	// Build args for VLLM_ARGS env var
	var args []string

	// GPU memory utilization
	gpuMemUtil := config.GPUMemoryUtil
	if gpuMemUtil <= 0 {
		gpuMemUtil = 0.9
	}
	args = append(args, fmt.Sprintf("--gpu-memory-utilization %.2f", gpuMemUtil))

	// Quantization
	if config.Quantization != "" {
		args = append(args, fmt.Sprintf("--quantization %s", config.Quantization))
	}

	// Max model length
	if config.MaxModelLen > 0 {
		args = append(args, fmt.Sprintf("--max-model-len %d", config.MaxModelLen))
	}

	// Tensor parallelism
	if config.TensorParallel > 1 {
		args = append(args, fmt.Sprintf("--tensor-parallel-size %d", config.TensorParallel))
	}

	if len(args) > 0 {
		env["VLLM_ARGS"] = strings.Join(args, " ")
	}

	return env
}

// BuildTGIArgs builds container arguments for Text Generation Inference server
func BuildTGIArgs(config *provider.WorkloadConfig) string {
	if config == nil || config.ModelID == "" {
		return ""
	}

	args := []string{
		"--model-id", config.ModelID,
		"--hostname", "0.0.0.0",
		"--port", fmt.Sprintf("%d", DefaultTGIPort),
	}

	// Quantization
	if config.Quantization != "" {
		args = append(args, "--quantize", config.Quantization)
	}

	// Max model length (TGI uses max-input-length and max-total-tokens)
	if config.MaxModelLen > 0 {
		args = append(args, "--max-input-length", fmt.Sprintf("%d", config.MaxModelLen/2))
		args = append(args, "--max-total-tokens", fmt.Sprintf("%d", config.MaxModelLen))
	}

	// Tensor parallelism (TGI uses num-shard)
	if config.TensorParallel > 1 {
		args = append(args, "--num-shard", fmt.Sprintf("%d", config.TensorParallel))
	}

	return strings.Join(args, " ")
}

// GetImageForWorkload returns the appropriate Docker image for a workload type
func GetImageForWorkload(workloadType provider.WorkloadType) string {
	switch workloadType {
	case provider.WorkloadTypeVLLM:
		return ImageVLLM
	case provider.WorkloadTypeTGI:
		return ImageTGI
	default:
		return ImageSSHBase
	}
}

// GetPortForWorkload returns the default port for a workload type
func GetPortForWorkload(workloadType provider.WorkloadType) int {
	switch workloadType {
	case provider.WorkloadTypeVLLM:
		return DefaultVLLMPort
	case provider.WorkloadTypeTGI:
		return DefaultTGIPort
	default:
		return 0
	}
}

// FormatPortsString formats ports for the Vast.ai API
func FormatPortsString(ports []int) string {
	if len(ports) == 0 {
		return ""
	}

	var portStrs []string
	for _, p := range ports {
		portStrs = append(portStrs, fmt.Sprintf("%d/http", p))
	}
	return strings.Join(portStrs, ",")
}

// ParsePortMappings converts Docker-style port bindings to a simple container->external port map
// Input format: {"8000/tcp": [{"HostIp": "0.0.0.0", "HostPort": "33526"}]}
// Output format: map[8000]33526
func (inst *Instance) ParsePortMappings() map[int]int {
	if inst.Ports == nil {
		return nil
	}

	result := make(map[int]int)
	for portSpec, bindings := range inst.Ports {
		if len(bindings) == 0 {
			continue
		}

		// Parse container port from "8000/tcp" or "8000/http" format
		containerPort := parsePortFromSpec(portSpec)
		if containerPort <= 0 {
			continue
		}

		// Get the external port from the first binding
		externalPort := parsePortNumber(bindings[0].HostPort)
		if externalPort <= 0 {
			continue
		}

		result[containerPort] = externalPort
	}

	return result
}

// parsePortFromSpec extracts the port number from a Docker port spec like "8000/tcp"
func parsePortFromSpec(spec string) int {
	// Split on "/" to separate port from protocol
	parts := strings.Split(spec, "/")
	if len(parts) == 0 {
		return 0
	}

	port, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return port
}

// parsePortNumber converts a port string to int
func parsePortNumber(s string) int {
	port, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return port
}
