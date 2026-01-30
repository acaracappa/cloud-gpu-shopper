// Package heartbeat provides heartbeat sender functionality for the node agent.
package heartbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	// DefaultInterval is the default heartbeat interval
	DefaultInterval = 30 * time.Second

	// DefaultTimeout is the HTTP request timeout
	DefaultTimeout = 10 * time.Second

	// DefaultUnreachableThreshold is consecutive failures before triggering failsafe
	DefaultUnreachableThreshold = 60 // 30 minutes at 30s interval
)

// Request is the heartbeat request body
type Request struct {
	SessionID    string  `json:"session_id"`
	AgentToken   string  `json:"agent_token"`
	Status       string  `json:"status"`
	IdleSeconds  int     `json:"idle_seconds,omitempty"`
	GPUUtilPct   float64 `json:"gpu_util_pct,omitempty"`
	MemoryUsedMB int     `json:"memory_used_mb,omitempty"`
}

// StatusProvider provides current agent status
type StatusProvider interface {
	GetStatus() (status string, idleSeconds int, gpuUtilPct float64, memoryUsedMB int)
}

// FailsafeHandler is called when shopper is unreachable for too long
type FailsafeHandler func()

// Sender sends periodic heartbeats to the shopper service
type Sender struct {
	shopperURL           string
	sessionID            string
	agentToken           string
	interval             time.Duration
	httpClient           *http.Client
	logger               *slog.Logger
	status               StatusProvider
	failsafe             FailsafeHandler
	failureCount         atomic.Int32
	running              atomic.Bool
	unreachableThreshold int32
}

// Option configures the heartbeat sender
type Option func(*Sender)

// WithInterval sets the heartbeat interval
func WithInterval(d time.Duration) Option {
	return func(s *Sender) {
		s.interval = d
	}
}

// WithLogger sets a custom logger
func WithLogger(logger *slog.Logger) Option {
	return func(s *Sender) {
		s.logger = logger
	}
}

// WithStatusProvider sets the status provider
func WithStatusProvider(sp StatusProvider) Option {
	return func(s *Sender) {
		s.status = sp
	}
}

// WithFailsafeHandler sets the failsafe handler for shopper-unreachable
func WithFailsafeHandler(h FailsafeHandler) Option {
	return func(s *Sender) {
		s.failsafe = h
	}
}

// WithUnreachableThreshold sets the consecutive failure threshold for failsafe
func WithUnreachableThreshold(threshold int) Option {
	return func(s *Sender) {
		if threshold > 0 {
			s.unreachableThreshold = int32(threshold)
		}
	}
}

// defaultStatusProvider returns static status
type defaultStatusProvider struct{}

func (d defaultStatusProvider) GetStatus() (string, int, float64, int) {
	return "running", 0, 0.0, 0
}

// New creates a new heartbeat sender
func New(shopperURL, sessionID, agentToken string, opts ...Option) *Sender {
	s := &Sender{
		shopperURL:           shopperURL,
		sessionID:            sessionID,
		agentToken:           agentToken,
		interval:             DefaultInterval,
		unreachableThreshold: DefaultUnreachableThreshold,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		logger:   slog.Default(),
		status:   defaultStatusProvider{},
		failsafe: func() {},
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// GetUnreachableThreshold returns the current failsafe threshold
func (s *Sender) GetUnreachableThreshold() int {
	return int(s.unreachableThreshold)
}

// Start begins sending heartbeats in the background
func (s *Sender) Start(ctx context.Context) {
	if s.running.Swap(true) {
		return // Already running
	}

	go s.run(ctx)
}

// run is the main heartbeat loop
func (s *Sender) run(ctx context.Context) {
	defer s.running.Store(false)

	s.logger.Info("heartbeat sender starting",
		slog.String("shopper_url", s.shopperURL),
		slog.String("session_id", s.sessionID),
		slog.Duration("interval", s.interval))

	// Send initial heartbeat immediately
	s.sendHeartbeat()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.sendHeartbeat()
		case <-ctx.Done():
			s.logger.Info("heartbeat sender stopping")
			return
		}
	}
}

// sendHeartbeat sends a single heartbeat to the shopper
func (s *Sender) sendHeartbeat() {
	status, idleSeconds, gpuUtil, memUsed := s.status.GetStatus()

	req := Request{
		SessionID:    s.sessionID,
		AgentToken:   s.agentToken,
		Status:       status,
		IdleSeconds:  idleSeconds,
		GPUUtilPct:   gpuUtil,
		MemoryUsedMB: memUsed,
	}

	body, err := json.Marshal(req)
	if err != nil {
		s.logger.Error("failed to marshal heartbeat", slog.String("error", err.Error()))
		return
	}

	url := fmt.Sprintf("%s/api/v1/sessions/%s/heartbeat", s.shopperURL, s.sessionID)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		s.logger.Error("failed to create heartbeat request", slog.String("error", err.Error()))
		s.handleFailure()
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		s.logger.Warn("heartbeat failed",
			slog.String("error", err.Error()),
			slog.Int("consecutive_failures", int(s.failureCount.Load())+1))
		s.handleFailure()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Warn("heartbeat returned non-OK status",
			slog.Int("status", resp.StatusCode),
			slog.Int("consecutive_failures", int(s.failureCount.Load())+1))
		s.handleFailure()
		return
	}

	// Success - reset failure count
	failures := s.failureCount.Swap(0)
	if failures > 0 {
		s.logger.Info("heartbeat succeeded after failures",
			slog.Int("previous_failures", int(failures)))
	}
}

// handleFailure increments failure count and triggers failsafe if threshold reached
func (s *Sender) handleFailure() {
	count := s.failureCount.Add(1)
	if count >= s.unreachableThreshold {
		s.logger.Error("FAILSAFE: shopper unreachable for too long, triggering shutdown",
			slog.Int("consecutive_failures", int(count)),
			slog.Int("threshold", int(s.unreachableThreshold)),
			slog.Duration("unreachable_time", time.Duration(count)*s.interval))
		s.failsafe()
	}
}

// GetFailureCount returns the current consecutive failure count
func (s *Sender) GetFailureCount() int {
	return int(s.failureCount.Load())
}

// IsRunning returns whether the heartbeat sender is running
func (s *Sender) IsRunning() bool {
	return s.running.Load()
}
