package mockprovider

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Server is the mock Vast.ai API server
type Server struct {
	state  *State
	router *gin.Engine
	logger *slog.Logger
}

// NewServer creates a new mock provider server
func NewServer(state *State) *Server {
	if state == nil {
		state = NewState()
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	s := &Server{
		state:  state,
		router: router,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	s.setupRoutes()
	return s
}

// Router returns the gin router for testing
func (s *Server) Router() *gin.Engine {
	return s.router
}

// State returns the underlying state for test manipulation
func (s *Server) State() *State {
	return s.state
}

func (s *Server) setupRoutes() {
	// Vast.ai API endpoints
	s.router.GET("/bundles/", s.handleListOffers)
	s.router.GET("/bundles", s.handleListOffers) // Without trailing slash

	s.router.PUT("/asks/:id/", s.handleCreateInstance)
	s.router.PUT("/asks/:id", s.handleCreateInstance)

	s.router.GET("/instances/", s.handleListInstances)
	s.router.GET("/instances", s.handleListInstances)

	s.router.GET("/instances/:id/", s.handleGetInstance)
	s.router.GET("/instances/:id", s.handleGetInstance)

	s.router.DELETE("/instances/:id/", s.handleDestroyInstance)
	s.router.DELETE("/instances/:id", s.handleDestroyInstance)

	// SSH key attachment (Vast.ai API)
	s.router.POST("/instances/:id/ssh/", s.handleAttachSSHKey)
	s.router.POST("/instances/:id/ssh", s.handleAttachSSHKey)

	// Health check
	s.router.GET("/health", s.handleHealth)

	// Test control endpoints
	s.router.POST("/_test/reset", s.handleTestReset)
	s.router.POST("/_test/config", s.handleTestConfig)
	s.router.POST("/_test/orphan", s.handleTestCreateOrphan)
}

// BundlesResponse matches Vast.ai API response format
type BundlesResponse struct {
	Offers []OfferResponse `json:"offers"`
}

// OfferResponse matches Vast.ai offer format
type OfferResponse struct {
	ID              int     `json:"id"`
	MachineID       int     `json:"machine_id"`
	GPUName         string  `json:"gpu_name"`
	NumGPUs         int     `json:"num_gpus"`
	GPURam          int     `json:"gpu_ram"`
	DPHTotal        float64 `json:"dph_total"`
	Verified        bool    `json:"verified"`
	Reliability     float64 `json:"reliability2"`
	DLPerf          float64 `json:"dlperf"`
	InetUp          float64 `json:"inet_up"`
	InetDown        float64 `json:"inet_down"`
	Rented          bool    `json:"rented"`
	PublicIPAddr    string  `json:"public_ipaddr"`
	DirectPortStart int     `json:"direct_port_start"`
	CUDAMaxGood     string  `json:"cuda_max_good"`
	DriverVersion   string  `json:"driver_version"`
}

func (s *Server) handleListOffers(c *gin.Context) {
	offers := s.state.ListOffers()

	response := BundlesResponse{
		Offers: make([]OfferResponse, len(offers)),
	}

	for i, offer := range offers {
		// Parse ID to int for API compatibility
		idInt, _ := strconv.Atoi(strings.TrimPrefix(offer.ID, "offer-"))
		machineInt, _ := strconv.Atoi(strings.TrimPrefix(offer.MachineID, "machine-"))

		response.Offers[i] = OfferResponse{
			ID:              idInt,
			MachineID:       machineInt,
			GPUName:         offer.GPUName,
			NumGPUs:         offer.NumGPUs,
			GPURam:          offer.VRAM * 1024, // Convert GB to MB
			DPHTotal:        offer.DPHTotal,
			Verified:        offer.Verified,
			Reliability:     offer.Reliability,
			DLPerf:          offer.DLPerf,
			InetUp:          offer.InetUp,
			InetDown:        offer.InetDown,
			Rented:          offer.Rented,
			PublicIPAddr:    offer.SSHHost,
			DirectPortStart: offer.SSHPort,
			CUDAMaxGood:     offer.CUDAVersion,
			DriverVersion:   offer.DriverVersion,
		}
	}

	c.JSON(http.StatusOK, response)
}

// CreateInstanceRequest matches Vast.ai create instance request
type CreateInstanceRequest struct {
	ClientID string            `json:"client_id"`
	Image    string            `json:"image"`
	Env      map[string]string `json:"env"`
	Disk     float64           `json:"disk"`
	Label    string            `json:"label"`
	OnStart  string            `json:"onstart"`
	RunType  string            `json:"runtype"`
	SSHKey   string            `json:"ssh_key"`
}

// CreateInstanceResponse matches Vast.ai create response
type CreateInstanceResponse struct {
	Success     bool   `json:"success"`
	NewContract int    `json:"new_contract"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) handleCreateInstance(c *gin.Context) {
	offerIDStr := c.Param("id")

	var req CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, CreateInstanceResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Convert offer ID to internal format
	offerID := fmt.Sprintf("offer-%s", offerIDStr)

	// Check if offer exists with original ID format
	if _, ok := s.state.GetOffer(offerID); !ok {
		// Try with the ID as-is
		offerID = offerIDStr
	}

	instance, err := s.state.CreateInstance(offerID, req.Label, req.Env, req.OnStart)
	if err != nil {
		s.logger.Error("failed to create instance", "error", err, "offer_id", offerID)
		c.JSON(http.StatusBadRequest, CreateInstanceResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	contractID, _ := strconv.Atoi(instance.ID)
	c.JSON(http.StatusOK, CreateInstanceResponse{
		Success:     true,
		NewContract: contractID,
	})
}

// InstancesResponse matches Vast.ai instances list response
type InstancesResponse struct {
	Instances []InstanceResponse `json:"instances"`
}

// InstanceResponse matches Vast.ai instance format
type InstanceResponse struct {
	ID           int     `json:"id"`
	MachineID    int     `json:"machine_id"`
	ActualStatus string  `json:"actual_status"`
	SSHHost      string  `json:"ssh_host"`
	SSHPort      int     `json:"ssh_port"`
	Label        string  `json:"label"`
	GPUName      string  `json:"gpu_name"`
	NumGPUs      int     `json:"num_gpus"`
	DPHTotal     float64 `json:"dph_total"`
	StartDate    float64 `json:"start_date"`
	StatusMsg    string  `json:"status_msg"`
	CurState     string  `json:"cur_state"`
}

func (s *Server) handleListInstances(c *gin.Context) {
	instances := s.state.ListInstances()

	response := InstancesResponse{
		Instances: make([]InstanceResponse, len(instances)),
	}

	for i, inst := range instances {
		idInt, _ := strconv.Atoi(inst.ID)
		machineInt, _ := strconv.Atoi(strings.TrimPrefix(inst.MachineID, "machine-"))

		response.Instances[i] = InstanceResponse{
			ID:           idInt,
			MachineID:    machineInt,
			ActualStatus: inst.ActualStatus,
			SSHHost:      inst.SSHHost,
			SSHPort:      inst.SSHPort,
			Label:        inst.Label,
			GPUName:      inst.GPUName,
			NumGPUs:      inst.NumGPUs,
			DPHTotal:     inst.DPHTotal,
			StartDate:    float64(inst.StartedAt.Unix()),
			StatusMsg:    string(inst.Status),
			CurState:     inst.ActualStatus,
		}
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleGetInstance(c *gin.Context) {
	instanceID := c.Param("id")

	instance, ok := s.state.GetInstance(instanceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	idInt, _ := strconv.Atoi(instance.ID)
	machineInt, _ := strconv.Atoi(strings.TrimPrefix(instance.MachineID, "machine-"))

	response := InstanceResponse{
		ID:           idInt,
		MachineID:    machineInt,
		ActualStatus: instance.ActualStatus,
		SSHHost:      instance.SSHHost,
		SSHPort:      instance.SSHPort,
		Label:        instance.Label,
		GPUName:      instance.GPUName,
		NumGPUs:      instance.NumGPUs,
		DPHTotal:     instance.DPHTotal,
		StartDate:    float64(instance.StartedAt.Unix()),
		StatusMsg:    string(instance.Status),
		CurState:     instance.ActualStatus,
	}

	c.JSON(http.StatusOK, response)
}

// DestroyResponse matches Vast.ai destroy response
type DestroyResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleDestroyInstance(c *gin.Context) {
	instanceID := c.Param("id")

	if err := s.state.DestroyInstance(instanceID); err != nil {
		s.logger.Error("failed to destroy instance", "error", err, "instance_id", instanceID)
		c.JSON(http.StatusBadRequest, DestroyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, DestroyResponse{Success: true})
}

// AttachSSHKeyRequest matches Vast.ai SSH key attachment request
type AttachSSHKeyRequest struct {
	SSHKey string `json:"ssh_key"`
}

// AttachSSHKeyResponse matches Vast.ai SSH key attachment response
type AttachSSHKeyResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleAttachSSHKey(c *gin.Context) {
	instanceID := c.Param("id")

	var req AttachSSHKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, AttachSSHKeyResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	// Verify instance exists
	if _, ok := s.state.GetInstance(instanceID); !ok {
		c.JSON(http.StatusNotFound, AttachSSHKeyResponse{
			Success: false,
			Error:   "instance not found",
		})
		return
	}

	c.JSON(http.StatusOK, AttachSSHKeyResponse{Success: true})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"type":   "mock-vastai-provider",
	})
}

// Test control handlers

func (s *Server) handleTestReset(c *gin.Context) {
	s.state.Reset()
	c.JSON(http.StatusOK, gin.H{"status": "reset"})
}

// TestConfig is the configuration for test behavior
type TestConfig struct {
	CreateDelayMs  int    `json:"create_delay_ms"`
	DestroyDelayMs int    `json:"destroy_delay_ms"`
	FailCreate     bool   `json:"fail_create"`
	FailDestroy    bool   `json:"fail_destroy"`
	FailCreateMsg  string `json:"fail_create_msg"`
	FailDestroyMsg string `json:"fail_destroy_msg"`
}

func (s *Server) handleTestConfig(c *gin.Context) {
	var config TestConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if config.CreateDelayMs > 0 {
		s.state.SetCreateDelay(time.Duration(config.CreateDelayMs) * time.Millisecond)
	}
	if config.DestroyDelayMs > 0 {
		s.state.SetDestroyDelay(time.Duration(config.DestroyDelayMs) * time.Millisecond)
	}
	s.state.SetFailCreate(config.FailCreate, config.FailCreateMsg)
	s.state.SetFailDestroy(config.FailDestroy, config.FailDestroyMsg)

	c.JSON(http.StatusOK, gin.H{"status": "configured"})
}

// TestOrphanRequest is the request to create an orphan instance
type TestOrphanRequest struct {
	Label string `json:"label"`
}

func (s *Server) handleTestCreateOrphan(c *gin.Context) {
	var req TestOrphanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	instance := s.state.CreateOrphanInstance(req.Label)
	c.JSON(http.StatusOK, gin.H{
		"instance_id": instance.ID,
		"label":       instance.Label,
	})
}

// Run starts the server on the specified address
func (s *Server) Run(addr string) error {
	s.logger.Info("starting mock provider server", "addr", addr)
	return s.router.Run(addr)
}

// ServeHTTP implements http.Handler for testing
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// Helper to parse the raw body for Vast.ai style requests
func parseRawJSON(c *gin.Context, v interface{}) error {
	body, err := c.GetRawData()
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
