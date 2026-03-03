package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	sshverify "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/ssh"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)

// Compile-time check that sshverify.Verifier satisfies SSHVerifier interface
var _ SSHVerifier = (*sshverify.Verifier)(nil)

const (
	// DefaultSSHVerifyTimeout is how long to wait for SSH verification
	DefaultSSHVerifyTimeout = 8 * time.Minute

	// DefaultSSHCheckInterval is the initial interval for SSH polling
	DefaultSSHCheckInterval = 15 * time.Second

	// DefaultSSHMaxInterval is the maximum interval between SSH poll attempts
	DefaultSSHMaxInterval = 60 * time.Second

	// DefaultSSHBackoffMultiplier is the multiplier for progressive backoff
	DefaultSSHBackoffMultiplier = 1.5

	// DefaultAPIVerifyTimeout is how long to wait for API verification (entrypoint mode)
	DefaultAPIVerifyTimeout = 10 * time.Minute

	// DefaultAPICheckInterval is how often to retry API health check
	DefaultAPICheckInterval = 15 * time.Second

	// DefaultDestroyTimeout is the max time to wait for destroy verification
	DefaultDestroyTimeout = 5 * time.Minute

	// DefaultDestroyRetries is the max number of destroy attempts
	DefaultDestroyRetries = 10

	// DefaultSSHKeyBits is the RSA key size
	DefaultSSHKeyBits = 4096

	// TensorDockCloudInitDelay is the time to wait for TensorDock cloud-init before SSH polling.
	TensorDockCloudInitDelay = 90 * time.Second

	// BlueLobsterBootDelay waits for post-boot dist-upgrade SSH instability.
	BlueLobsterBootDelay = 60 * time.Second

	// DefaultLowBalanceThreshold is the balance below which a warning is logged
	DefaultLowBalanceThreshold = 5.00
)

// SessionStore defines the interface for session persistence
type SessionStore interface {
	Create(ctx context.Context, session *models.Session) error
	Get(ctx context.Context, id string) (*models.Session, error)
	Update(ctx context.Context, session *models.Session) error
	GetActiveSessionByConsumerAndOffer(ctx context.Context, consumerID, offerID string) (*models.Session, error)
	List(ctx context.Context, filter models.SessionListFilter) ([]*models.Session, error)
}

// ProviderRegistry provides access to provider clients
type ProviderRegistry interface {
	Get(name string) (provider.Provider, error)
}

// InventoryFinder provides access to comparable offer lookup for auto-retry
// and global offer failure tracking
type InventoryFinder interface {
	FindComparableOffers(ctx context.Context, original *models.GPUOffer, scope string, excludeIDs []string, excludeMachineIDs []string) ([]models.GPUOffer, error)
	GetOffer(ctx context.Context, offerID string) (*models.GPUOffer, error)
	RecordOfferFailure(offerID, provider, gpuType, machineID, failureType, reason string)
	EvictOffer(offerID string)
}

// CostRecorder records final costs for terminated sessions.
type CostRecorder interface {
	RecordFinalCost(ctx context.Context, session *models.Session) error
}

// SSHVerifier defines the interface for SSH verification
type SSHVerifier interface {
	VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error
}

// HTTPVerifier defines the interface for HTTP endpoint verification
type HTTPVerifier interface {
	CheckHealth(ctx context.Context, url string) error
}

// Service handles GPU session provisioning and destruction
type Service struct {
	store        SessionStore
	providers    ProviderRegistry
	inventory    InventoryFinder // Optional: needed for auto-retry
	costRecorder CostRecorder    // Optional: records final cost on session termination
	logger       *slog.Logger
	deploymentID string

	// SSH verification
	sshVerifier          SSHVerifier
	sshVerifyTimeout     time.Duration
	sshCheckInterval     time.Duration
	sshMaxInterval       time.Duration
	sshBackoffMultiplier float64

	// API verification (for entrypoint mode)
	httpVerifier     HTTPVerifier
	apiVerifyTimeout time.Duration
	apiCheckInterval time.Duration

	// Configuration
	destroyTimeout time.Duration
	destroyRetries int
	sshKeyBits     int

	// Balance warning
	lowBalanceThreshold float64

	// For time mocking in tests
	now func() time.Time

	// Verification goroutine tracking (for testing)
	verifyWg sync.WaitGroup

	// Per-session destroy locks to prevent concurrent destroy operations
	destroyLocks   map[string]*sync.Mutex
	destroyLocksMu sync.Mutex
}

// Option configures the provisioner service
type Option func(*Service)

func WithLogger(logger *slog.Logger) Option {
	return func(s *Service) { s.logger = logger }
}

func WithDeploymentID(id string) Option {
	return func(s *Service) { s.deploymentID = id }
}

func WithSSHVerifyTimeout(d time.Duration) Option {
	return func(s *Service) { s.sshVerifyTimeout = d }
}

func WithSSHCheckInterval(d time.Duration) Option {
	return func(s *Service) { s.sshCheckInterval = d }
}

func WithSSHMaxInterval(d time.Duration) Option {
	return func(s *Service) { s.sshMaxInterval = d }
}

func WithSSHBackoffMultiplier(m float64) Option {
	return func(s *Service) { s.sshBackoffMultiplier = m }
}

func WithDestroyRetries(n int) Option {
	return func(s *Service) { s.destroyRetries = n }
}

func WithSSHVerifier(v SSHVerifier) Option {
	return func(s *Service) { s.sshVerifier = v }
}

func WithHTTPVerifier(v HTTPVerifier) Option {
	return func(s *Service) { s.httpVerifier = v }
}

func WithAPIVerifyTimeout(d time.Duration) Option {
	return func(s *Service) { s.apiVerifyTimeout = d }
}

func WithAPICheckInterval(d time.Duration) Option {
	return func(s *Service) { s.apiCheckInterval = d }
}

func WithTimeFunc(fn func() time.Time) Option {
	return func(s *Service) { s.now = fn }
}

func WithInventory(inv InventoryFinder) Option {
	return func(s *Service) { s.inventory = inv }
}

func WithCostRecorder(cr CostRecorder) Option {
	return func(s *Service) { s.costRecorder = cr }
}

// New creates a new provisioner service
func New(store SessionStore, providers ProviderRegistry, opts ...Option) *Service {
	s := &Service{
		store:                store,
		providers:            providers,
		logger:               slog.Default(),
		deploymentID:         uuid.New().String(),
		sshVerifyTimeout:     DefaultSSHVerifyTimeout,
		sshCheckInterval:     DefaultSSHCheckInterval,
		sshMaxInterval:       DefaultSSHMaxInterval,
		sshBackoffMultiplier: DefaultSSHBackoffMultiplier,
		apiVerifyTimeout:     DefaultAPIVerifyTimeout,
		apiCheckInterval:     DefaultAPICheckInterval,
		destroyTimeout:       DefaultDestroyTimeout,
		destroyRetries:       DefaultDestroyRetries,
		sshKeyBits:           DefaultSSHKeyBits,
		lowBalanceThreshold:  DefaultLowBalanceThreshold,
		now:                  time.Now,
		destroyLocks:         make(map[string]*sync.Mutex),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.sshVerifier == nil {
		s.sshVerifier = sshverify.NewVerifier(
			sshverify.WithVerifyTimeout(s.sshVerifyTimeout),
			sshverify.WithCheckInterval(s.sshCheckInterval),
		)
	}

	if s.httpVerifier == nil {
		s.httpVerifier = NewDefaultHTTPVerifier()
	}

	return s
}

// CreateSession provisions a new GPU session using two-phase provisioning.
// If auto_retry is enabled, it will automatically try comparable offers on failure.
func (s *Service) CreateSession(ctx context.Context, req models.CreateSessionRequest, offer *models.GPUOffer) (*models.Session, error) {
	if req.AutoRetry && req.MaxRetries <= 0 {
		req.MaxRetries = 3
	}
	if req.MaxRetries > 5 {
		req.MaxRetries = 5
	}
	if req.RetryScope == "" {
		req.RetryScope = "same_gpu"
	}

	return s.createSessionWithRetry(ctx, req, offer, nil, nil, 0, "")
}

// GetSession retrieves a session by ID
func (s *Service) GetSession(ctx context.Context, sessionID string) (*models.Session, error) {
	return s.store.Get(ctx, sessionID)
}

// ListSessions returns sessions matching the filter criteria
func (s *Service) ListSessions(ctx context.Context, filter models.SessionListFilter) ([]*models.Session, error) {
	return s.store.List(ctx, filter)
}

// GetDeploymentID returns the deployment identifier
func (s *Service) GetDeploymentID() string {
	return s.deploymentID
}

// WaitForVerificationComplete waits for all pending verification goroutines to complete.
// Primarily for testing to ensure no goroutine leaks.
func (s *Service) WaitForVerificationComplete(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.verifyWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// buildWorkloadConfig builds a provider WorkloadConfig from session request
func (s *Service) buildWorkloadConfig(req models.CreateSessionRequest) *provider.WorkloadConfig {
	config := &provider.WorkloadConfig{
		ModelID:      req.ModelID,
		Quantization: req.Quantization,
	}

	switch req.WorkloadType {
	case models.WorkloadLLMVLLM:
		config.Type = provider.WorkloadTypeVLLM
	case models.WorkloadLLMTGI:
		config.Type = provider.WorkloadTypeTGI
	default:
		config.Type = provider.WorkloadTypeCustom
	}

	return config
}

// buildBenchmarkOnStart generates an on-start command that deploys and runs
// the GPU benchmark script on the instance.
func buildBenchmarkOnStart(sessionID string, offer *models.GPUOffer) string {
	return fmt.Sprintf(
		"echo 'benchmark_pending' > /tmp/benchmark_status && echo %s %s %.4f %s %s > /tmp/benchmark_args",
		shellQuote(sessionID),
		shellQuote("${MODEL:-unknown}"),
		offer.PricePerHour,
		shellQuote(offer.Provider),
		shellQuote(offer.Location),
	)
}

// buildInstanceTags creates provider instance tags for session tracking and orphan detection
func (s *Service) buildInstanceTags(sessionID, consumerID string, expiresAt time.Time) models.InstanceTags {
	return models.InstanceTags{
		ShopperSessionID:    sessionID,
		ShopperDeploymentID: s.deploymentID,
		ShopperExpiresAt:    expiresAt,
		ShopperConsumerID:   consumerID,
	}
}

// shellQuote wraps a string in single quotes with proper escaping for safe
// shell interpolation, preventing injection via untrusted values like location names.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
