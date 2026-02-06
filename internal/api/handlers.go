package api

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/lifecycle"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/storage"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// Request/Response types

// ErrorResponse is the standard error response
type ErrorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

// HealthResponse is the health check response
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Services  map[string]string `json:"services,omitempty"`
}

// CreateSessionRequest is the request to create a new session
type CreateSessionRequest struct {
	ConsumerID     string `json:"consumer_id" binding:"required"`
	OfferID        string `json:"offer_id" binding:"required"`
	WorkloadType   string `json:"workload_type" binding:"required"`
	ReservationHrs int    `json:"reservation_hours" binding:"required,min=1,max=12"`
	IdleThreshold  int    `json:"idle_threshold_minutes,omitempty"`
	StoragePolicy  string `json:"storage_policy,omitempty"`

	// Entrypoint mode configuration
	LaunchMode   string `json:"launch_mode,omitempty"`   // "ssh" or "entrypoint"
	DockerImage  string `json:"docker_image,omitempty"`  // Custom Docker image
	ModelID      string `json:"model_id,omitempty"`      // HuggingFace model ID
	ExposedPorts []int  `json:"exposed_ports,omitempty"` // Ports to expose (e.g., 8000)
	Quantization string `json:"quantization,omitempty"`  // Quantization method

	// Template-based provisioning (Vast.ai)
	TemplateHashID string `json:"template_hash_id,omitempty"` // Vast.ai template hash_id

	// Storage configuration
	DiskGB int `json:"disk_gb,omitempty"` // Disk space in GB (cannot be changed after creation)

	// Auto-retry configuration
	AutoRetry  bool   `json:"auto_retry,omitempty"`  // Enable auto-reprovision on failure
	MaxRetries int    `json:"max_retries,omitempty"` // Max alternative offers to try (default 3, max 5)
	RetryScope string `json:"retry_scope,omitempty"` // "same_gpu", "same_vram", "any"
}

// ListTemplatesQuery defines query parameters for listing templates
type ListTemplatesQuery struct {
	Recommended bool   `form:"recommended"`
	UseSSH      bool   `form:"use_ssh"`
	Name        string `form:"name"`
	Image       string `form:"image"`
}

// CreateSessionResponse is the response after creating a session
type CreateSessionResponse struct {
	Session          models.SessionResponse `json:"session"`
	SSHPrivateKey    string                 `json:"ssh_private_key,omitempty"`
	RetriesAttempted int                    `json:"retries_attempted,omitempty"` // Number of retries before success
}

// ExtendSessionRequest is the request to extend a session
type ExtendSessionRequest struct {
	AdditionalHours int `json:"additional_hours" binding:"required,min=1,max=12"`
}

// ListSessionsQuery defines query parameters for listing sessions
type ListSessionsQuery struct {
	ConsumerID string `form:"consumer_id"`
	Status     string `form:"status"`
	Provider   string `form:"provider"` // Bug #100 fix: Add provider filter
	Limit      int    `form:"limit"`
}

// CostQuery defines query parameters for cost endpoints
type CostQueryParams struct {
	ConsumerID string `form:"consumer_id"`
	SessionID  string `form:"session_id"`
	StartDate  string `form:"start_date"`
	EndDate    string `form:"end_date"`
	Period     string `form:"period"` // "daily", "monthly"
}

// SessionDiagnosticsResponse contains diagnostic information for a session
type SessionDiagnosticsResponse struct {
	SessionID    string               `json:"session_id"`
	Status       models.SessionStatus `json:"status"`
	Provider     string               `json:"provider"`
	GPUType      string               `json:"gpu_type"`
	GPUCount     int                  `json:"gpu_count"`
	SSHHost      string               `json:"ssh_host,omitempty"`
	SSHPort      int                  `json:"ssh_port,omitempty"`
	SSHUser      string               `json:"ssh_user,omitempty"`
	LaunchMode   string               `json:"launch_mode,omitempty"`
	APIEndpoint  string               `json:"api_endpoint,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	ExpiresAt    time.Time            `json:"expires_at"`
	Uptime       string               `json:"uptime"`
	TimeToExpiry string               `json:"time_to_expiry"`
	SSHAvailable bool                 `json:"ssh_available"`
	Note         string               `json:"note"`
}

// Handlers

func (s *Server) handleHealth(c *gin.Context) {
	response := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now(),
		Services:  make(map[string]string),
	}

	// Check services
	if s.lifecycle != nil && s.lifecycle.IsRunning() {
		response.Services["lifecycle"] = "running"
	} else {
		response.Services["lifecycle"] = "stopped"
	}

	if s.inventory != nil {
		response.Services["inventory"] = "ok"
	}

	// Return 503 if not ready (e.g., during startup sweep)
	if !s.ready.Load() {
		response.Status = "unavailable"
		response.Services["ready"] = "false"
		c.JSON(http.StatusServiceUnavailable, response)
		return
	}

	response.Services["ready"] = "true"
	c.JSON(http.StatusOK, response)
}

// ReadyResponse is the readiness check response
type ReadyResponse struct {
	Ready     bool      `json:"ready"`
	Timestamp time.Time `json:"timestamp"`
}

func (s *Server) handleReady(c *gin.Context) {
	response := ReadyResponse{
		Ready:     s.ready.Load(),
		Timestamp: time.Now(),
	}

	if !s.ready.Load() {
		c.JSON(http.StatusServiceUnavailable, response)
		return
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleListInventory(c *gin.Context) {
	ctx := c.Request.Context()

	filter := models.OfferFilter{
		Provider: c.Query("provider"),
		GPUType:  c.Query("gpu_type"),
		Location: c.Query("location"),
	}

	// Bug #12-14: Validate numeric params - return 400 for invalid values
	if minVRAM := c.Query("min_vram"); minVRAM != "" {
		v, err := strconv.Atoi(minVRAM)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_vram: must be a valid integer, got %q", minVRAM),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_vram: must be non-negative, got %d", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MinVRAM = v
	}

	// Bug #12-14: Validate max_price - return 400 for invalid values
	if maxPrice := c.Query("max_price"); maxPrice != "" {
		v, err := strconv.ParseFloat(maxPrice, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid max_price: must be a valid number, got %q", maxPrice),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid max_price: must be non-negative, got %v", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MaxPrice = v
	}

	// Bug #24: Parse and validate gpu_count filter (treats it as min_gpu_count)
	if gpuCount := c.Query("gpu_count"); gpuCount != "" {
		v, err := strconv.Atoi(gpuCount)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid gpu_count: must be a valid integer, got %q", gpuCount),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid gpu_count: must be non-negative, got %d", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MinGPUCount = v
	}

	// Also support min_gpu_count parameter
	if minGPUCount := c.Query("min_gpu_count"); minGPUCount != "" {
		v, err := strconv.Atoi(minGPUCount)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_gpu_count: must be a valid integer, got %q", minGPUCount),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_gpu_count: must be non-negative, got %d", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MinGPUCount = v
	}

	// Bug #22: Parse and validate min_reliability filter
	if minReliability := c.Query("min_reliability"); minReliability != "" {
		v, err := strconv.ParseFloat(minReliability, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_reliability: must be a valid number, got %q", minReliability),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 || v > 1 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_reliability: must be between 0 and 1, got %v", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MinReliability = v
	}

	if minConfidence := c.Query("min_availability_confidence"); minConfidence != "" {
		v, err := strconv.ParseFloat(minConfidence, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_availability_confidence: must be a valid number, got %q", minConfidence),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 || v > 1 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_availability_confidence: must be between 0 and 1, got %v", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MinAvailabilityConfidence = v
	}

	if minCUDA := c.Query("min_cuda"); minCUDA != "" {
		v, err := strconv.ParseFloat(minCUDA, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_cuda: must be a valid number, got %q", minCUDA),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid min_cuda: must be non-negative, got %v", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		filter.MinCUDAVersion = v
	}

	// Template-aware filtering: apply template's extra_filters as offer constraints
	if templateHashID := c.Query("template_hash_id"); templateHashID != "" {
		templateProvider, err := s.inventory.GetTemplateProvider("vastai")
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     "template filtering requires Vast.ai provider: " + err.Error(),
				RequestID: c.GetString("request_id"),
			})
			return
		}

		tmpl, err := templateProvider.GetTemplate(ctx, templateHashID)
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:     "template not found: " + templateHashID,
				RequestID: c.GetString("request_id"),
			})
			return
		}

		// Parse template extra_filters and apply as offer constraints
		extraFilters, err := tmpl.ParseExtraFilters()
		if err != nil {
			log.Printf("[API] WARNING: template %q has malformed extra_filters: %v", tmpl.Name, err)
		} else if extraFilters != nil {
			if cudaFilter, ok := extraFilters["cuda_max_good"]; ok {
				if cudaFilter.Gte != nil && filter.MinCUDAVersion < *cudaFilter.Gte {
					filter.MinCUDAVersion = *cudaFilter.Gte
				}
				if cudaFilter.Gt != nil && filter.MinCUDAVersion < *cudaFilter.Gt+0.1 {
					filter.MinCUDAVersion = *cudaFilter.Gt + 0.1
				}
			}
			if vramFilter, ok := extraFilters["gpu_total_ram"]; ok {
				vramGB := 0
				if vramFilter.Gte != nil {
					vramGB = int(math.Ceil(*vramFilter.Gte / 1024))
				}
				if vramFilter.Gt != nil {
					vramGB = int(math.Ceil(*vramFilter.Gt/1024)) + 1
				}
				if vramGB > filter.MinVRAM {
					filter.MinVRAM = vramGB
				}
			}
		}

		// Template filtering implies Vast.ai provider
		if filter.Provider == "" {
			filter.Provider = "vastai"
		}
	}

	// Bug #11, #72: Parse and validate pagination params
	var limit, offset int
	if limitStr := c.Query("limit"); limitStr != "" {
		v, err := strconv.Atoi(limitStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid limit: must be a valid integer, got %q", limitStr),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		// Bug #72: Reject negative or zero limit
		if v <= 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid limit: must be positive, got %d", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		limit = v
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		v, err := strconv.Atoi(offsetStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid offset: must be a valid integer, got %q", offsetStr),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		if v < 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:     fmt.Sprintf("invalid offset: must be non-negative, got %d", v),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		offset = v
	}

	offers, err := s.inventory.ListOffers(ctx, filter)
	if err != nil {
		// Bug #2 fix: Return 400 for invalid provider, not 500
		status := http.StatusInternalServerError
		var providerNotFound *inventory.ProviderNotFoundError
		if errors.As(err, &providerNotFound) {
			status = http.StatusBadRequest
		}
		c.JSON(status, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Bug #11: Apply pagination
	totalCount := len(offers)
	if offset > 0 {
		if offset >= len(offers) {
			offers = []models.GPUOffer{}
		} else {
			offers = offers[offset:]
		}
	}
	if limit > 0 && limit < len(offers) {
		offers = offers[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"offers": offers,
		"count":  len(offers),
		"total":  totalCount,
	})
}

func (s *Server) handleGetOffer(c *gin.Context) {
	ctx := c.Request.Context()
	offerID := c.Param("id")

	offer, err := s.inventory.GetOffer(ctx, offerID)
	if err != nil {
		status := http.StatusInternalServerError
		if _, ok := err.(*inventory.OfferNotFoundError); ok {
			status = http.StatusNotFound
		}
		c.JSON(status, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, offer)
}

func (s *Server) handleCreateSession(c *gin.Context) {
	ctx := c.Request.Context()

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Bug #9: Sanitize validation errors to use JSON field names
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     sanitizeValidationError(err),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Get the offer from cache (spot market is fast - don't invalidate)
	offer, err := s.inventory.GetOffer(ctx, req.OfferID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "offer not found: " + req.OfferID,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Convert storage policy
	var storagePolicy models.StoragePolicy
	switch req.StoragePolicy {
	case "preserve":
		storagePolicy = models.StoragePreserve
	default:
		storagePolicy = models.StorageDestroy
	}

	// Convert launch mode
	var launchMode models.LaunchMode
	switch req.LaunchMode {
	case "entrypoint":
		launchMode = models.LaunchModeEntrypoint
	default:
		launchMode = models.LaunchModeSSH
	}

	// Create session
	createReq := models.CreateSessionRequest{
		ConsumerID:     req.ConsumerID,
		OfferID:        req.OfferID,
		WorkloadType:   models.WorkloadType(req.WorkloadType),
		ReservationHrs: req.ReservationHrs,
		IdleThreshold:  req.IdleThreshold,
		StoragePolicy:  storagePolicy,
		LaunchMode:     launchMode,
		DockerImage:    req.DockerImage,
		ModelID:        req.ModelID,
		ExposedPorts:   req.ExposedPorts,
		Quantization:   req.Quantization,
		TemplateHashID: req.TemplateHashID,
		DiskGB:         req.DiskGB,
		AutoRetry:      req.AutoRetry,
		MaxRetries:     req.MaxRetries,
		RetryScope:     req.RetryScope,
	}

	// Look up template's recommended disk space and SSH timeout (non-fatal if lookup fails)
	if req.TemplateHashID != "" {
		if templateProvider, err := s.inventory.GetTemplateProvider("vastai"); err == nil {
			if tmpl, err := templateProvider.GetTemplate(ctx, req.TemplateHashID); err == nil && tmpl != nil {
				createReq.TemplateRecommendedDiskGB = tmpl.RecommendedDiskSpace
				// BUG-005: Use template's recommended SSH timeout for heavy images
				createReq.TemplateRecommendedSSHTimeout = tmpl.GetRecommendedSSHTimeout()
			}
		}
	}

	session, err := s.provisioner.CreateSession(ctx, createReq, offer)
	if err != nil {
		var dupErr *provisioner.DuplicateSessionError
		if errors.As(err, &dupErr) {
			c.JSON(http.StatusConflict, ErrorResponse{
				Error:     err.Error(),
				RequestID: c.GetString("request_id"),
			})
			return
		}

		// Check for insufficient disk space error
		var diskErr *provisioner.InsufficientDiskError
		if errors.As(err, &diskErr) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":          err.Error(),
				"error_type":     "insufficient_disk",
				"requested_gb":   diskErr.RequestedGB,
				"minimum_gb":     diskErr.MinimumGB,
				"recommended_gb": diskErr.RecommendedGB,
				"breakdown":      diskErr.Estimation,
				"request_id":     c.GetString("request_id"),
			})
			return
		}

		// Check for stale inventory error - this means the offer appeared available
		// but provisioning failed, likely due to stale inventory data
		var staleErr *provisioner.StaleInventoryError
		if errors.As(err, &staleErr) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":           err.Error(),
				"error_type":      "stale_inventory",
				"offer_id":        staleErr.OfferID,
				"provider":        staleErr.Provider,
				"retry_suggested": true,
				"message":         "The selected offer is no longer available. Please refresh inventory and select a different offer.",
				"request_id":      c.GetString("request_id"),
			})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Return session with secrets (only shown once)
	c.JSON(http.StatusCreated, CreateSessionResponse{
		Session:          session.ToResponse(),
		SSHPrivateKey:    session.SSHPrivateKey,
		RetriesAttempted: session.RetryCount,
	})
}

func (s *Server) handleListSessions(c *gin.Context) {
	ctx := c.Request.Context()

	var query ListSessionsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Build filter from query parameters
	// Bug #100 fix: Parse provider query param and add to filter
	filter := models.SessionListFilter{
		ConsumerID: query.ConsumerID,
		Provider:   query.Provider,
		Limit:      query.Limit,
	}
	if query.Status != "" {
		filter.Status = models.SessionStatus(query.Status)
	}

	// Query sessions from the provisioner service
	sessions, err := s.provisioner.ListSessions(ctx, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to list sessions",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Convert to response format
	responses := make([]models.SessionResponse, len(sessions))
	for i, session := range sessions {
		responses[i] = session.ToResponse()
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": responses,
		"count":    len(responses),
	})
}

func (s *Server) handleGetSession(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("id")

	session, err := s.provisioner.GetSession(ctx, sessionID)
	if err != nil {
		// Check if the error is a not-found error (return 404) vs other errors (return 500)
		if errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:     err.Error(),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		// Internal error (DB issues, etc.)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to get session",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, session.ToResponse())
}

func (s *Server) handleSessionDone(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("id")

	if err := s.lifecycle.SignalDone(ctx, sessionID); err != nil {
		// Bug #1/#70 fix: Return proper HTTP status codes based on error type
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		} else {
			// Bug #70 fix: Return 409 Conflict for terminal sessions
			var terminalErr *lifecycle.SessionTerminalError
			if errors.As(err, &terminalErr) {
				status = http.StatusConflict
			}
		}
		c.JSON(status, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "session shutdown initiated",
		"session_id": sessionID,
	})
}

func (s *Server) handleExtendSession(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("id")

	var req ExtendSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Bug #9: Sanitize validation errors to use JSON field names
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     sanitizeValidationError(err),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if err := s.lifecycle.ExtendSession(ctx, sessionID, req.AdditionalHours); err != nil {
		// Bug #16/#18 fix: Return proper HTTP status codes based on error type
		// Use typed error checking instead of string matching for reliability
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		} else {
			// Check for terminal/stopping session error (409 Conflict)
			var terminalErr *lifecycle.SessionTerminalError
			if errors.As(err, &terminalErr) {
				status = http.StatusConflict
			}
			// Check for hard max exceeded (400 Bad Request)
			var hardMaxErr *lifecycle.HardMaxExceededError
			if errors.As(err, &hardMaxErr) {
				status = http.StatusBadRequest
			}
		}
		c.JSON(status, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Get updated session
	session, err := s.provisioner.GetSession(ctx, sessionID)
	if err != nil || session == nil {
		// Extension succeeded but couldn't retrieve updated session
		// Return success without the new expiry time
		c.JSON(http.StatusOK, gin.H{
			"message":    "session extended",
			"session_id": sessionID,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "session extended",
		"session_id":     sessionID,
		"new_expires_at": session.ExpiresAt,
	})
}

func (s *Server) handleDeleteSession(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("id")

	if err := s.provisioner.DestroySession(ctx, sessionID); err != nil {
		// Check for not-found errors and return 404
		var sessionNotFound *provisioner.SessionNotFoundError
		if errors.As(err, &sessionNotFound) || errors.Is(err, storage.ErrNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:     fmt.Sprintf("session not found: %s", sessionID),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "session destroyed",
		"session_id": sessionID,
	})
}

func (s *Server) handleGetCosts(c *gin.Context) {
	ctx := c.Request.Context()

	var params CostQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// If session ID provided, get session cost
	if params.SessionID != "" {
		// Bug #49/#75 fix: Check if session exists before returning cost
		// Return 404 if session_id provided but not found
		_, err := s.provisioner.GetSession(ctx, params.SessionID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				c.JSON(http.StatusNotFound, ErrorResponse{
					Error:     fmt.Sprintf("session not found: %s", params.SessionID),
					RequestID: c.GetString("request_id"),
				})
				return
			}
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:     err.Error(),
				RequestID: c.GetString("request_id"),
			})
			return
		}

		cost, err := s.costTracker.GetSessionCost(ctx, params.SessionID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:     err.Error(),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"session_id": params.SessionID,
			"total_cost": cost,
			"currency":   "USD",
		})
		return
	}

	// Handle period-based queries
	var summary *models.CostSummary
	var err error

	switch params.Period {
	case "daily":
		summary, err = s.costTracker.GetDailySummary(ctx, params.ConsumerID)
	case "monthly":
		summary, err = s.costTracker.GetMonthlySummary(ctx, params.ConsumerID)
	default:
		// Parse custom date range
		var startTime, endTime time.Time
		if params.StartDate != "" {
			var parseErr error
			startTime, parseErr = time.Parse("2006-01-02", params.StartDate)
			if parseErr != nil {
				c.JSON(http.StatusBadRequest, ErrorResponse{
					Error:     fmt.Sprintf("invalid start_date format, expected YYYY-MM-DD: %s", params.StartDate),
					RequestID: c.GetString("request_id"),
				})
				return
			}
		}
		if params.EndDate != "" {
			var parseErr error
			endTime, parseErr = time.Parse("2006-01-02", params.EndDate)
			if parseErr != nil {
				c.JSON(http.StatusBadRequest, ErrorResponse{
					Error:     fmt.Sprintf("invalid end_date format, expected YYYY-MM-DD: %s", params.EndDate),
					RequestID: c.GetString("request_id"),
				})
				return
			}
		}

		// Bug #10: If no dates specified, default to current month to avoid zero dates
		if params.StartDate == "" && params.EndDate == "" {
			now := time.Now()
			startTime = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			endTime = startTime.AddDate(0, 1, 0)
		} else if params.StartDate == "" {
			// If only end date specified, default start to beginning of that month
			startTime = time.Date(endTime.Year(), endTime.Month(), 1, 0, 0, 0, 0, endTime.Location())
		} else if params.EndDate == "" {
			// If only start date specified, default end to end of that month
			endTime = time.Date(startTime.Year(), startTime.Month()+1, 1, 0, 0, 0, 0, startTime.Location())
		}

		summary, err = s.costTracker.GetSummary(ctx, models.CostQuery{
			ConsumerID: params.ConsumerID,
			StartTime:  startTime,
			EndTime:    endTime,
		})
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, summary)
}

func (s *Server) handleGetCostSummary(c *gin.Context) {
	ctx := c.Request.Context()
	consumerID := c.Query("consumer_id")

	summary, err := s.costTracker.GetMonthlySummary(ctx, consumerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, summary)
}

func (s *Server) handleGetSessionDiagnostics(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("id")

	session, err := s.provisioner.GetSession(ctx, sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Check if session is running
	if session.Status != models.StatusRunning {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     "session is not running; diagnostics only available for running sessions",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	now := time.Now()
	uptime := now.Sub(session.CreatedAt)
	timeToExpiry := session.ExpiresAt.Sub(now)

	// Check if SSH connection info is available
	sshAvailable := session.SSHHost != "" && session.SSHPort > 0

	// Build diagnostics response
	response := SessionDiagnosticsResponse{
		SessionID:    session.ID,
		Status:       session.Status,
		Provider:     session.Provider,
		GPUType:      session.GPUType,
		GPUCount:     session.GPUCount,
		SSHHost:      session.SSHHost,
		SSHPort:      session.SSHPort,
		SSHUser:      session.SSHUser,
		LaunchMode:   string(session.LaunchMode),
		APIEndpoint:  session.APIEndpoint,
		CreatedAt:    session.CreatedAt,
		ExpiresAt:    session.ExpiresAt,
		Uptime:       formatDuration(uptime),
		TimeToExpiry: formatDuration(timeToExpiry),
		SSHAvailable: sshAvailable,
		Note:         "Full SSH diagnostics (GPU status, health checks) require client-side SSH access. The private key is not stored server-side for security.",
	}

	c.JSON(http.StatusOK, response)
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
	}
	return strconv.Itoa(minutes) + "m"
}

// Bug #9: sanitizeValidationError converts internal field names to JSON field names
// in validation error messages to avoid leaking internal implementation details.
func sanitizeValidationError(err error) string {
	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		return err.Error()
	}

	var messages []string
	for _, fe := range validationErrs {
		// Convert field name to JSON tag name (snake_case)
		jsonFieldName := toSnakeCase(fe.Field())
		switch fe.Tag() {
		case "required":
			messages = append(messages, fmt.Sprintf("%s is required", jsonFieldName))
		case "min":
			messages = append(messages, fmt.Sprintf("%s must be at least %s", jsonFieldName, fe.Param()))
		case "max":
			messages = append(messages, fmt.Sprintf("%s must be at most %s", jsonFieldName, fe.Param()))
		default:
			messages = append(messages, fmt.Sprintf("%s failed validation (%s)", jsonFieldName, fe.Tag()))
		}
	}
	return strings.Join(messages, "; ")
}

// toSnakeCase converts a PascalCase or camelCase string to snake_case
func toSnakeCase(s string) string {
	// Handle common field name mappings
	fieldMappings := map[string]string{
		"ConsumerID":      "consumer_id",
		"OfferID":         "offer_id",
		"WorkloadType":    "workload_type",
		"ReservationHrs":  "reservation_hours",
		"IdleThreshold":   "idle_threshold_minutes",
		"StoragePolicy":   "storage_policy",
		"LaunchMode":      "launch_mode",
		"DockerImage":     "docker_image",
		"ModelID":         "model_id",
		"ExposedPorts":    "exposed_ports",
		"Quantization":    "quantization",
		"AdditionalHours": "additional_hours",
	}
	if mapped, ok := fieldMappings[s]; ok {
		return mapped
	}
	// Fallback: convert PascalCase to snake_case using regex
	re := regexp.MustCompile("([a-z0-9])([A-Z])")
	return strings.ToLower(re.ReplaceAllString(s, "${1}_${2}"))
}

// Template handlers

func (s *Server) handleListTemplates(c *gin.Context) {
	ctx := c.Request.Context()

	var query ListTemplatesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Build filter from query parameters
	filter := models.TemplateFilter{
		Recommended: query.Recommended,
		UseSSH:      query.UseSSH,
		Name:        query.Name,
		Image:       query.Image,
	}

	// Get the Vast.ai provider (templates are only available from Vast.ai)
	templateProvider, err := s.inventory.GetTemplateProvider("vastai")
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "template provider not available: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	templates, err := templateProvider.ListTemplates(ctx, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to list templates: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"templates": templates,
		"count":     len(templates),
	})
}

func (s *Server) handleGetTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	hashID := c.Param("hash_id")

	// Get the Vast.ai provider (templates are only available from Vast.ai)
	templateProvider, err := s.inventory.GetTemplateProvider("vastai")
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "template provider not available: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	template, err := templateProvider.GetTemplate(ctx, hashID)
	if err != nil {
		// Check if template not found using typed error
		if errors.Is(err, provider.ErrTemplateNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:     err.Error(),
				RequestID: c.GetString("request_id"),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to get template: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, template)
}

func (s *Server) handleGetCompatibleTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	offerID := c.Param("id")

	// Get the offer first
	offer, err := s.inventory.GetOffer(ctx, offerID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "offer not found: " + offerID,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Templates are only available for Vast.ai
	if offer.Provider != "vastai" {
		c.JSON(http.StatusOK, gin.H{
			"compatible_templates": []models.CompatibleTemplate{},
			"count":                0,
			"offer_id":             offerID,
			"provider":             offer.Provider,
			"note":                 "templates are only available for Vast.ai offers",
		})
		return
	}

	// Get the Vast.ai provider
	templateProvider, err := s.inventory.GetTemplateProvider("vastai")
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "template provider not available: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Get compatible templates using filter matching
	compatibleTemplates, err := templateProvider.GetCompatibleTemplates(ctx, offerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     "failed to get compatible templates: " + err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"compatible_templates": compatibleTemplates,
		"count":                len(compatibleTemplates),
		"offer_id":             offerID,
		"gpu_type":             offer.GPUType,
		"vram_gb":              offer.VRAM,
	})
}
