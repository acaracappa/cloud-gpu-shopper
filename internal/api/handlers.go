package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
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
}

// CreateSessionResponse is the response after creating a session
type CreateSessionResponse struct {
	Session       models.SessionResponse `json:"session"`
	SSHPrivateKey string                 `json:"ssh_private_key,omitempty"`
}

// ExtendSessionRequest is the request to extend a session
type ExtendSessionRequest struct {
	AdditionalHours int `json:"additional_hours" binding:"required,min=1,max=12"`
}

// ListSessionsQuery defines query parameters for listing sessions
type ListSessionsQuery struct {
	ConsumerID string `form:"consumer_id"`
	Status     string `form:"status"`
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
	SessionID    string                 `json:"session_id"`
	Status       models.SessionStatus   `json:"status"`
	Provider     string                 `json:"provider"`
	GPUType      string                 `json:"gpu_type"`
	GPUCount     int                    `json:"gpu_count"`
	SSHHost      string                 `json:"ssh_host,omitempty"`
	SSHPort      int                    `json:"ssh_port,omitempty"`
	SSHUser      string                 `json:"ssh_user,omitempty"`
	LaunchMode   string                 `json:"launch_mode,omitempty"`
	APIEndpoint  string                 `json:"api_endpoint,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	ExpiresAt    time.Time              `json:"expires_at"`
	Uptime       string                 `json:"uptime"`
	TimeToExpiry string                 `json:"time_to_expiry"`
	SSHAvailable bool                   `json:"ssh_available"`
	Note         string                 `json:"note"`
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

	if minVRAM := c.Query("min_vram"); minVRAM != "" {
		if v, err := strconv.Atoi(minVRAM); err == nil {
			filter.MinVRAM = v
		}
	}

	if maxPrice := c.Query("max_price"); maxPrice != "" {
		if v, err := strconv.ParseFloat(maxPrice, 64); err == nil {
			filter.MaxPrice = v
		}
	}

	if minGPUCount := c.Query("min_gpu_count"); minGPUCount != "" {
		if v, err := strconv.Atoi(minGPUCount); err == nil {
			filter.MinGPUCount = v
		}
	}

	if minConfidence := c.Query("min_availability_confidence"); minConfidence != "" {
		if v, err := strconv.ParseFloat(minConfidence, 64); err == nil {
			filter.MinAvailabilityConfidence = v
		}
	}

	offers, err := s.inventory.ListOffers(ctx, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"offers": offers,
		"count":  len(offers),
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
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
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
		Session:       session.ToResponse(),
		SSHPrivateKey: session.SSHPrivateKey,
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
	filter := models.SessionListFilter{
		ConsumerID: query.ConsumerID,
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
		status := http.StatusInternalServerError
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
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	if err := s.lifecycle.ExtendSession(ctx, sessionID, req.AdditionalHours); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
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

