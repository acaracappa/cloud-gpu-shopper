package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/benchmark"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/metrics"
	benchsvc "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/benchmark"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/cost"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/inventory"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/lifecycle"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/service/provisioner"
)

// Server is the HTTP API server
type Server struct {
	router     *gin.Engine
	httpServer *http.Server
	logger     *slog.Logger

	// Services
	inventory          *inventory.Service
	provisioner        *provisioner.Service
	lifecycle          *lifecycle.Manager
	costTracker        *cost.Tracker
	benchmarkStore     *benchmark.Store
	benchmarkRunner    *benchsvc.Runner
	benchmarkScheduler *benchsvc.Scheduler

	// Configuration
	host string
	port int

	// Readiness state (atomic for thread-safe access)
	ready atomic.Bool
}

// Option configures the server
type Option func(*Server)

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithHost sets the server host
func WithHost(host string) Option {
	return func(s *Server) {
		s.host = host
	}
}

// WithPort sets the server port
func WithPort(port int) Option {
	return func(s *Server) {
		s.port = port
	}
}

// WithBenchmarkStore sets the benchmark store
func WithBenchmarkStore(store *benchmark.Store) Option {
	return func(s *Server) {
		s.benchmarkStore = store
	}
}

// WithBenchmarkRunner sets the benchmark runner
func WithBenchmarkRunner(runner *benchsvc.Runner) Option {
	return func(s *Server) {
		s.benchmarkRunner = runner
	}
}

// WithBenchmarkScheduler sets the benchmark scheduler
func WithBenchmarkScheduler(scheduler *benchsvc.Scheduler) Option {
	return func(s *Server) {
		s.benchmarkScheduler = scheduler
	}
}

// New creates a new API server
func New(
	inv *inventory.Service,
	prov *provisioner.Service,
	lm *lifecycle.Manager,
	ct *cost.Tracker,
	opts ...Option,
) *Server {
	s := &Server{
		logger:      slog.Default(),
		inventory:   inv,
		provisioner: prov,
		lifecycle:   lm,
		costTracker: ct,
		host:        "0.0.0.0",
		port:        8080,
	}

	for _, opt := range opts {
		opt(s)
	}

	s.setupRouter()
	return s
}

// SetReady sets the server readiness state
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
	s.logger.Info("server readiness changed", slog.Bool("ready", ready))
}

// IsReady returns whether the server is ready to accept traffic
func (s *Server) IsReady() bool {
	return s.ready.Load()
}

// setupRouter configures the Gin router
func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Add middleware
	router.Use(s.requestIDMiddleware())
	router.Use(s.metricsMiddleware())
	router.Use(s.bodySizeLimitMiddleware(1 << 20)) // 1MB limit
	router.Use(s.loggingMiddleware())
	router.Use(s.recoveryMiddleware())

	// Health and readiness endpoints
	router.GET("/health", s.handleHealth)
	router.GET("/ready", s.handleReady)

	// Prometheus metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Inventory
		v1.GET("/inventory", s.handleListInventory)
		v1.GET("/inventory/:id", s.handleGetOffer)
		v1.GET("/inventory/:id/compatible-templates", s.handleGetCompatibleTemplates)

		// Templates (Vast.ai only)
		v1.GET("/templates", s.handleListTemplates)
		v1.GET("/templates/:hash_id", s.handleGetTemplate)

		// Sessions
		v1.POST("/sessions", s.handleCreateSession)
		v1.GET("/sessions", s.handleListSessions)
		v1.GET("/sessions/:id", s.handleGetSession)
		v1.GET("/sessions/:id/diagnostics", s.handleGetSessionDiagnostics)
		v1.POST("/sessions/:id/done", s.handleSessionDone)
		v1.POST("/sessions/:id/extend", s.handleExtendSession)
		v1.DELETE("/sessions/:id", s.handleDeleteSession)

		// Costs
		v1.GET("/costs", s.handleGetCosts)
		v1.GET("/costs/summary", s.handleGetCostSummary)

		// Offer health (global failure tracking)
		v1.GET("/offer-health", s.handleOfferHealth)

		// Benchmarks
		v1.GET("/benchmarks", s.handleListBenchmarks)
		v1.GET("/benchmarks/:id", s.handleGetBenchmark)
		v1.POST("/benchmarks", s.handleCreateBenchmark)
		v1.GET("/benchmarks/best", s.handleGetBestBenchmark)
		v1.GET("/benchmarks/cheapest", s.handleGetCheapestBenchmark)
		v1.GET("/benchmarks/compare", s.handleCompareBenchmarks)
		v1.GET("/benchmarks/recommendations", s.handleGetHardwareRecommendations)

		// Benchmark Runs (automated orchestration)
		v1.POST("/benchmark-runs", s.handleStartBenchmarkRun)
		v1.GET("/benchmark-runs/:id", s.handleGetBenchmarkRun)
		v1.DELETE("/benchmark-runs/:id", s.handleCancelBenchmarkRun)

		// Benchmark Schedules (recurring automation)
		v1.POST("/benchmark-schedules", s.handleCreateBenchmarkSchedule)
		v1.GET("/benchmark-schedules", s.handleListBenchmarkSchedules)
		v1.PUT("/benchmark-schedules/:id", s.handleUpdateBenchmarkSchedule)
		v1.DELETE("/benchmark-schedules/:id", s.handleDeleteBenchmarkSchedule)
	}

	s.router = router
}

// Start starts the HTTP server
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	// Bug #25 fix: Increase timeouts and connection limits to handle burst traffic
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadTimeout:       60 * time.Second,  // Bug #25: Increased from 30s
		WriteTimeout:      60 * time.Second,  // Bug #25: Increased from 30s
		IdleTimeout:       120 * time.Second, // Bug #25: Increased from 60s
		ReadHeaderTimeout: 10 * time.Second,  // Bug #25: Add header read timeout
		MaxHeaderBytes:    1 << 20,           // Bug #25: 1MB max header size
	}

	s.logger.Info("starting API server", slog.String("addr", addr))
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down API server")
	return s.httpServer.Shutdown(ctx)
}

// Router returns the Gin router (for testing)
func (s *Server) Router() *gin.Engine {
	return s.router
}

// Middleware

// validRequestIDRegex allows alphanumeric, dots, underscores, and hyphens up to 128 chars.
var validRequestIDRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

func isValidRequestID(id string) bool {
	return id != "" && validRequestIDRegex.MatchString(id)
}

func (s *Server) requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if !isValidRequestID(requestID) {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

func (s *Server) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		// Use the matched route pattern for consistent path labels
		// This prevents high cardinality from path parameters like /sessions/:id
		path := c.FullPath()
		if path == "" {
			// Fallback for unmatched routes (404s)
			path = "unmatched"
		}

		duration := time.Since(start)
		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method

		metrics.RecordHTTPRequest(method, path, status, duration)
	}
}

func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		s.logger.Info("request completed",
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Duration("latency", latency),
			slog.String("request_id", c.GetString("request_id")),
			slog.String("client_ip", c.ClientIP()))
	}
}

func (s *Server) recoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := string(debug.Stack())
				s.logger.Error("panic recovered",
					slog.Any("error", err),
					slog.String("stack", stack),
					slog.String("request_id", c.GetString("request_id")))

				c.JSON(http.StatusInternalServerError, ErrorResponse{
					Error:     "internal server error",
					RequestID: c.GetString("request_id"),
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}

func (s *Server) bodySizeLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
