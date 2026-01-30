// Package main implements the GPU Shopper node agent.
// The agent runs on provisioned GPU nodes and provides:
// - Heartbeat sending to the shopper service
// - Self-destruct timer for safety
// - Shopper-unreachable failsafe
// - Health/status endpoints
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/agent/api"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/agent/gpumon"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/agent/heartbeat"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/agent/idle"
)

const (
	// DefaultSelfDestructGrace is the grace period after expiry before self-destruct
	DefaultSelfDestructGrace = 30 * time.Minute

	// DefaultAgentPort is the default port for the agent API
	DefaultAgentPort = 8081

	// ShutdownTimeout is the timeout for graceful shutdown
	ShutdownTimeout = 10 * time.Second
)

// AgentStatus provides status information to the API and heartbeat sender
type AgentStatus struct {
	sessionID       string
	startedAt       time.Time
	heartbeatSender *heartbeat.Sender
	gpuMonitor      *gpumon.Monitor
	idleDetector    *idle.Detector

	mu        sync.RWMutex
	lastStats gpumon.GPUStats
}

func (a *AgentStatus) GetSessionID() string { return a.sessionID }
func (a *AgentStatus) GetStatus() string    { return "running" }
func (a *AgentStatus) GetIdleSeconds() int  { return a.idleDetector.IdleSeconds() }
func (a *AgentStatus) GetGPUUtilization() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastStats.UtilizationPct
}
func (a *AgentStatus) GetMemoryUsedMB() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastStats.MemoryUsedMB
}
func (a *AgentStatus) GetUptime() time.Duration  { return time.Since(a.startedAt) }
func (a *AgentStatus) GetHeartbeatFailures() int { return a.heartbeatSender.GetFailureCount() }
func (a *AgentStatus) IsShopperReachable() bool  { return a.heartbeatSender.GetFailureCount() < 3 }

// updateStats updates the cached GPU stats
func (a *AgentStatus) updateStats(stats gpumon.GPUStats) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastStats = stats
}

// For heartbeat status provider interface
func (a *AgentStatus) GetStatus4() (string, int, float64, int) {
	return a.GetStatus(), a.GetIdleSeconds(), a.GetGPUUtilization(), a.GetMemoryUsedMB()
}

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("GPU Shopper Agent starting")

	// Load configuration from environment
	config, err := loadConfig()
	if err != nil {
		logger.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("configuration loaded",
		slog.String("session_id", config.SessionID),
		slog.Time("expires_at", config.ExpiresAt),
		slog.Time("self_destruct_at", config.SelfDestructAt),
		slog.Int("agent_port", config.AgentPort))

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create GPU monitor and idle detector
	gpuMonitor := gpumon.NewMonitor(logger.With(slog.String("component", "gpumon")))
	idleDetector := idle.NewDetector(5.0) // 5% threshold

	// Create agent status tracker
	status := &AgentStatus{
		sessionID:    config.SessionID,
		startedAt:    time.Now(),
		gpuMonitor:   gpuMonitor,
		idleDetector: idleDetector,
	}

	// Create heartbeat sender
	hbSender := heartbeat.New(
		config.ShopperURL,
		config.SessionID,
		config.AgentToken,
		heartbeat.WithLogger(logger.With(slog.String("component", "heartbeat"))),
		heartbeat.WithFailsafeHandler(func() {
			triggerSelfDestruct(logger, "shopper unreachable")
		}),
	)
	status.heartbeatSender = hbSender

	// Create API server
	apiServer := api.New(
		config.SessionID,
		api.WithLogger(logger.With(slog.String("component", "api"))),
		api.WithPort(config.AgentPort),
		api.WithStatusProvider(status),
	)

	// Start self-destruct timer
	go startSelfDestructTimer(ctx, logger, config.SelfDestructAt)

	// Start GPU monitoring goroutine
	go startGPUMonitoring(ctx, logger, status)

	// Start heartbeat sender
	hbSender.Start(ctx)

	// Start API server in background
	go func() {
		if err := apiServer.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error("API server error", slog.String("error", err.Error()))
		}
	}()

	logger.Info("agent started successfully",
		slog.String("session_id", config.SessionID),
		slog.Duration("time_until_expiry", time.Until(config.ExpiresAt)),
		slog.Duration("time_until_self_destruct", time.Until(config.SelfDestructAt)))

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal", slog.String("signal", sig.String()))
	case <-ctx.Done():
		logger.Info("context cancelled")
	}

	// Graceful shutdown
	logger.Info("initiating graceful shutdown")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer shutdownCancel()

	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("API server shutdown error", slog.String("error", err.Error()))
	}

	logger.Info("agent shutdown complete")
}

// Config holds the agent configuration
type Config struct {
	SessionID      string
	DeploymentID   string
	ExpiresAt      time.Time
	SelfDestructAt time.Time
	ConsumerID     string
	AgentToken     string
	AgentPort      int
	ShopperURL     string
}

// loadConfig loads configuration from environment variables
func loadConfig() (*Config, error) {
	config := &Config{}

	// Required variables
	config.SessionID = os.Getenv("SHOPPER_SESSION_ID")
	if config.SessionID == "" {
		return nil, fmt.Errorf("SHOPPER_SESSION_ID is required")
	}

	config.AgentToken = os.Getenv("SHOPPER_AGENT_TOKEN")
	if config.AgentToken == "" {
		return nil, fmt.Errorf("SHOPPER_AGENT_TOKEN is required")
	}

	expiresAtStr := os.Getenv("SHOPPER_EXPIRES_AT")
	if expiresAtStr == "" {
		return nil, fmt.Errorf("SHOPPER_EXPIRES_AT is required")
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SHOPPER_EXPIRES_AT: %w", err)
	}
	config.ExpiresAt = expiresAt

	// Optional variables with defaults
	config.DeploymentID = os.Getenv("SHOPPER_DEPLOYMENT_ID")
	config.ConsumerID = os.Getenv("SHOPPER_CONSUMER_ID")

	config.ShopperURL = os.Getenv("SHOPPER_URL")
	if config.ShopperURL == "" {
		config.ShopperURL = "http://localhost:8080"
	}

	portStr := os.Getenv("SHOPPER_AGENT_PORT")
	if portStr == "" {
		config.AgentPort = DefaultAgentPort
	} else {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SHOPPER_AGENT_PORT: %w", err)
		}
		config.AgentPort = port
	}

	// Calculate self-destruct time
	graceStr := os.Getenv("SHOPPER_SELF_DESTRUCT_GRACE")
	gracePeriod := DefaultSelfDestructGrace
	if graceStr != "" {
		grace, err := time.ParseDuration(graceStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SHOPPER_SELF_DESTRUCT_GRACE: %w", err)
		}
		gracePeriod = grace
	}
	config.SelfDestructAt = config.ExpiresAt.Add(gracePeriod)

	return config, nil
}

// startSelfDestructTimer starts the self-destruct timer
func startSelfDestructTimer(ctx context.Context, logger *slog.Logger, selfDestructAt time.Time) {
	timeUntil := time.Until(selfDestructAt)
	if timeUntil <= 0 {
		logger.Error("SELF-DESTRUCT: expiration time already passed")
		triggerSelfDestruct(logger, "expiration time passed")
		return
	}

	logger.Info("self-destruct timer started",
		slog.Time("self_destruct_at", selfDestructAt),
		slog.Duration("time_until", timeUntil))

	timer := time.NewTimer(timeUntil)
	defer timer.Stop()

	select {
	case <-timer.C:
		logger.Error("SELF-DESTRUCT: expiration time reached")
		triggerSelfDestruct(logger, "expiration time reached")
	case <-ctx.Done():
		logger.Info("self-destruct timer cancelled")
	}
}

// triggerSelfDestruct initiates system shutdown
func triggerSelfDestruct(logger *slog.Logger, reason string) {
	logger.Error("SELF-DESTRUCT TRIGGERED",
		slog.String("reason", reason),
		slog.Time("timestamp", time.Now()))

	// Try to shutdown gracefully first
	cmd := exec.Command("shutdown", "-h", "now")
	if err := cmd.Run(); err != nil {
		logger.Error("shutdown command failed, trying poweroff",
			slog.String("error", err.Error()))

		// Try poweroff as fallback
		cmd = exec.Command("poweroff")
		if err := cmd.Run(); err != nil {
			logger.Error("poweroff command failed, forcing exit",
				slog.String("error", err.Error()))
			os.Exit(1)
		}
	}
}

// startGPUMonitoring runs a goroutine that polls GPU stats every 5 seconds
func startGPUMonitoring(ctx context.Context, logger *slog.Logger, status *AgentStatus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	logger.Info("GPU monitoring started")

	for {
		select {
		case <-ticker.C:
			stats, err := status.gpuMonitor.GetStats(ctx)
			if err != nil {
				logger.Warn("failed to get GPU stats", slog.String("error", err.Error()))
				continue
			}

			status.updateStats(stats)
			status.idleDetector.RecordSample(stats.UtilizationPct)

		case <-ctx.Done():
			logger.Info("GPU monitoring stopped")
			return
		}
	}
}
