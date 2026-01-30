// Package api provides the HTTP API for the node agent.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// StatusProvider provides agent status information
type StatusProvider interface {
	GetSessionID() string
	GetStatus() string
	GetIdleSeconds() int
	GetGPUUtilization() float64
	GetMemoryUsedMB() int
	GetUptime() time.Duration
	GetHeartbeatFailures() int
	IsShopperReachable() bool
}

// HealthResponse is the health check response
type HealthResponse struct {
	Status    string    `json:"status"`
	SessionID string    `json:"session_id"`
	Uptime    string    `json:"uptime"`
	Timestamp time.Time `json:"timestamp"`
}

// StatusResponse is the detailed status response
type StatusResponse struct {
	SessionID         string    `json:"session_id"`
	Status            string    `json:"status"`
	IdleSeconds       int       `json:"idle_seconds"`
	GPUUtilization    float64   `json:"gpu_utilization_pct"`
	MemoryUsedMB      int       `json:"memory_used_mb"`
	Uptime            string    `json:"uptime"`
	ShopperReachable  bool      `json:"shopper_reachable"`
	HeartbeatFailures int       `json:"heartbeat_failures"`
	Timestamp         time.Time `json:"timestamp"`
}

// Server is the agent HTTP API server
type Server struct {
	server    *http.Server
	logger    *slog.Logger
	status    StatusProvider
	port      int
	startedAt time.Time

	mu sync.RWMutex
}

// Option configures the server
type Option func(*Server)

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithPort sets the server port
func WithPort(port int) Option {
	return func(s *Server) {
		s.port = port
	}
}

// WithStatusProvider sets the status provider
func WithStatusProvider(sp StatusProvider) Option {
	return func(s *Server) {
		s.status = sp
	}
}

// defaultStatusProvider is a fallback status provider
type defaultStatusProvider struct {
	sessionID string
	startedAt time.Time
}

func (d *defaultStatusProvider) GetSessionID() string       { return d.sessionID }
func (d *defaultStatusProvider) GetStatus() string          { return "running" }
func (d *defaultStatusProvider) GetIdleSeconds() int        { return 0 }
func (d *defaultStatusProvider) GetGPUUtilization() float64 { return 0.0 }
func (d *defaultStatusProvider) GetMemoryUsedMB() int       { return 0 }
func (d *defaultStatusProvider) GetUptime() time.Duration   { return time.Since(d.startedAt) }
func (d *defaultStatusProvider) GetHeartbeatFailures() int  { return 0 }
func (d *defaultStatusProvider) IsShopperReachable() bool   { return true }

// New creates a new agent API server
func New(sessionID string, opts ...Option) *Server {
	s := &Server{
		logger:    slog.Default(),
		port:      8081,
		startedAt: time.Now(),
		status:    &defaultStatusProvider{sessionID: sessionID, startedAt: time.Now()},
	}

	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/", s.handleNotFound)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.loggingMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	return s
}

// Start starts the API server
func (s *Server) Start() error {
	s.logger.Info("starting agent API server", slog.Int("port", s.port))
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down agent API server")
	return s.server.Shutdown(ctx)
}

// loggingMiddleware logs all requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Debug("request handled",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Duration("latency", time.Since(start)))
	})
}

// handleHealth returns the health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := HealthResponse{
		Status:    "ok",
		SessionID: s.status.GetSessionID(),
		Uptime:    s.status.GetUptime().String(),
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleStatus returns detailed status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := StatusResponse{
		SessionID:         s.status.GetSessionID(),
		Status:            s.status.GetStatus(),
		IdleSeconds:       s.status.GetIdleSeconds(),
		GPUUtilization:    s.status.GetGPUUtilization(),
		MemoryUsedMB:      s.status.GetMemoryUsedMB(),
		Uptime:            s.status.GetUptime().String(),
		ShopperReachable:  s.status.IsShopperReachable(),
		HeartbeatFailures: s.status.GetHeartbeatFailures(),
		Timestamp:         time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleNotFound handles unknown paths
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not found", http.StatusNotFound)
}
