package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
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
}

// CreateSessionResponse is the response after creating a session
type CreateSessionResponse struct {
	Session       models.SessionResponse `json:"session"`
	SSHPrivateKey string                 `json:"ssh_private_key,omitempty"`
	AgentToken    string                 `json:"agent_token,omitempty"`
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

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleListInventory(c *gin.Context) {
	ctx := c.Request.Context()

	filter := models.OfferFilter{
		Provider:       c.Query("provider"),
		GPUType:        c.Query("gpu_type"),
		Location:       c.Query("location"),
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

	// Create session
	createReq := models.CreateSessionRequest{
		ConsumerID:     req.ConsumerID,
		OfferID:        req.OfferID,
		WorkloadType:   models.WorkloadType(req.WorkloadType),
		ReservationHrs: req.ReservationHrs,
		IdleThreshold:  req.IdleThreshold,
		StoragePolicy:  storagePolicy,
	}

	session, err := s.provisioner.CreateSession(ctx, createReq, offer)
	if err != nil {
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
		AgentToken:    session.AgentToken,
	})
}

func (s *Server) handleListSessions(c *gin.Context) {
	var query ListSessionsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Note: In a full implementation, we'd query the session store
	// with filters. This is a placeholder that returns empty list.
	c.JSON(http.StatusOK, gin.H{
		"sessions": []models.SessionResponse{},
		"count":    0,
	})
}

func (s *Server) handleGetSession(c *gin.Context) {
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
	session, _ := s.provisioner.GetSession(ctx, sessionID)

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
			startTime, _ = time.Parse("2006-01-02", params.StartDate)
		}
		if params.EndDate != "" {
			endTime, _ = time.Parse("2006-01-02", params.EndDate)
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

// HeartbeatRequest is the request from an agent heartbeat
type HeartbeatRequest struct {
	SessionID    string  `json:"session_id"`
	AgentToken   string  `json:"agent_token"`
	Status       string  `json:"status,omitempty"`
	IdleSeconds  int     `json:"idle_seconds,omitempty"`
	GPUUtilPct   float64 `json:"gpu_util_pct,omitempty"`
	MemoryUsedMB int     `json:"memory_used_mb,omitempty"`
}

func (s *Server) handleHeartbeat(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("id")

	var req HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Verify session exists and token matches
	session, err := s.provisioner.GetSession(ctx, sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:     "session not found",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Validate agent token
	if session.AgentToken != req.AgentToken {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:     "invalid agent token",
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// Record heartbeat with idle tracking
	if err := s.provisioner.RecordHeartbeat(ctx, sessionID, req.IdleSeconds); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:     err.Error(),
			RequestID: c.GetString("request_id"),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"session_id": sessionID,
	})
}
