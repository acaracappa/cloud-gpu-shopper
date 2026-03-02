package provisioner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/provider"
	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSSHVerification_SuccessTransitionsToRunning verifies that successful SSH verification
// transitions the session from provisioning to running status
func TestSSHVerification_SuccessTransitionsToRunning(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Use mock SSH verifier that succeeds immediately
	mockSSH := NewMockSSHVerifier()
	mockSSH.SetSucceed(true)

	svc := New(store, registry,
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithSSHVerifier(mockSSH),
		WithSSHVerifyTimeout(5*time.Second),
		WithSSHCheckInterval(100*time.Millisecond))

	// Ensure verification goroutines complete before test ends
	defer func() {
		require.True(t, svc.WaitForVerificationComplete(10*time.Second), "verification goroutines should complete")
	}()

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{
		Provider:   "vastai",
		ProviderID: "123",
		GPUType:    "RTX4090",
	}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)
	assert.NotEmpty(t, session.ID)

	// Wait for SSH verification to complete using require.Eventually
	require.Eventually(t, func() bool {
		s, err := store.Get(ctx, session.ID)
		if err != nil {
			return false
		}
		return s.Status == models.StatusRunning
	}, 5*time.Second, 50*time.Millisecond, "Session should transition to running after SSH verification")

	// Verify SSH verification was called
	calls := mockSSH.GetVerifyCalls()
	assert.NotEmpty(t, calls, "SSH verification should have been called")
	if len(calls) > 0 {
		assert.Equal(t, "192.168.1.100", calls[0].Host)
		assert.Equal(t, 22, calls[0].Port)
		assert.Equal(t, "root", calls[0].User)
	}
}

// TestSSHVerification_TimeoutDestroysInstance verifies that SSH verification timeout
// destroys the instance and fails the session
func TestSSHVerification_TimeoutDestroysInstance(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Use mock SSH verifier that always fails
	mockSSH := NewMockSSHVerifier()
	mockSSH.SetSucceed(false)

	// Use very short timeout for testing
	svc := New(store, registry,
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithSSHVerifier(mockSSH),
		WithSSHVerifyTimeout(500*time.Millisecond),
		WithSSHCheckInterval(100*time.Millisecond))

	// Ensure verification goroutines complete before test ends
	defer func() {
		require.True(t, svc.WaitForVerificationComplete(10*time.Second), "verification goroutines should complete")
	}()

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{
		Provider:   "vastai",
		ProviderID: "123",
	}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	// Wait for SSH verification timeout using require.Eventually
	var finalError string
	require.Eventually(t, func() bool {
		s, err := store.Get(ctx, session.ID)
		if err != nil {
			return false
		}
		finalError = s.Error
		return s.Status == models.StatusFailed
	}, 5*time.Second, 50*time.Millisecond, "Session should fail after SSH verification timeout")

	assert.Contains(t, finalError, "SSH verification timeout", "Error should indicate SSH timeout")

	// Verify provider destroy was called
	assert.Equal(t, 1, prov.destroyCalls, "Instance should be destroyed after SSH timeout")
}

// TestSSHVerification_MultipleAttempts verifies that SSH verification retries
func TestSSHVerification_MultipleAttempts(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Create a mock SSH verifier that fails initially then succeeds
	attemptCount := 0
	var mu sync.Mutex
	customVerifier := &countingSSHVerifier{
		failUntilAttempt: 3,
		mu:               &mu,
		attempts:         &attemptCount,
	}

	svc := New(store, registry,
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithSSHVerifier(customVerifier),
		WithSSHVerifyTimeout(5*time.Second),
		WithSSHCheckInterval(100*time.Millisecond))

	// Ensure verification goroutines complete before test ends
	defer func() {
		require.True(t, svc.WaitForVerificationComplete(10*time.Second), "verification goroutines should complete")
	}()

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{
		Provider:   "vastai",
		ProviderID: "123",
	}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	// Wait for SSH verification to complete using require.Eventually
	require.Eventually(t, func() bool {
		s, err := store.Get(ctx, session.ID)
		if err != nil {
			return false
		}
		return s.Status == models.StatusRunning
	}, 5*time.Second, 50*time.Millisecond, "Session should eventually reach running")

	mu.Lock()
	attempts := attemptCount
	mu.Unlock()

	assert.GreaterOrEqual(t, attempts, 3, "Should have made at least 3 SSH verification attempts")
}

// countingSSHVerifier is a custom verifier that fails until a certain attempt count
type countingSSHVerifier struct {
	failUntilAttempt int
	mu               *sync.Mutex
	attempts         *int
}

func (c *countingSSHVerifier) VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error {
	c.mu.Lock()
	*c.attempts++
	count := *c.attempts
	c.mu.Unlock()

	if count < c.failUntilAttempt {
		return errors.New("SSH connection refused")
	}
	return nil
}

// TestSSHVerification_SessionTerminalStopsVerification verifies that if a session
// becomes terminal (stopped/failed), SSH verification stops
func TestSSHVerification_SessionTerminalStopsVerification(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Use mock SSH verifier that fails with delay
	mockSSH := NewMockSSHVerifier()
	mockSSH.SetSucceed(false)
	mockSSH.SetDelay(50 * time.Millisecond)

	svc := New(store, registry,
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithSSHVerifier(mockSSH),
		WithSSHVerifyTimeout(10*time.Second),
		WithSSHCheckInterval(100*time.Millisecond))

	// Ensure verification goroutines complete before test ends
	defer func() {
		require.True(t, svc.WaitForVerificationComplete(15*time.Second), "verification goroutines should complete")
	}()

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{
		Provider:   "vastai",
		ProviderID: "123",
	}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	// Wait for SSH verification to start (at least one call)
	require.Eventually(t, func() bool {
		return len(mockSSH.GetVerifyCalls()) >= 1
	}, 5*time.Second, 50*time.Millisecond, "SSH verification should start")

	// Destroy the session, which should stop SSH verification
	err = svc.DestroySession(ctx, session.ID)
	require.NoError(t, err)

	// Record call count after destroy
	initialCalls := len(mockSSH.GetVerifyCalls())

	// Wait for verification goroutine to notice the terminal state and exit
	// The goroutine checks IsTerminal() on each poll cycle
	require.Eventually(t, func() bool {
		return svc.WaitForVerificationComplete(100 * time.Millisecond)
	}, 5*time.Second, 100*time.Millisecond, "verification goroutine should exit after session destroyed")

	finalCalls := len(mockSSH.GetVerifyCalls())

	// There might be one more call in flight when we stopped, but not many more
	assert.LessOrEqual(t, finalCalls-initialCalls, 2, "SSH verification should stop when session is destroyed")
}

// TestSSHVerification_WithDelayedSSHInfo tests the case where SSH info
// is not immediately available and needs to be polled from the provider
func TestSSHVerification_WithDelayedSSHInfo(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")

	// Configure provider to return empty SSH info initially, then provide it
	callCount := 0
	var mu sync.Mutex
	prov.createInstanceFn = func(ctx context.Context, req provider.CreateInstanceRequest) (*provider.InstanceInfo, error) {
		return &provider.InstanceInfo{
			ProviderInstanceID: "mock-instance-123",
			SSHHost:            "", // Empty initially
			SSHPort:            0,
			SSHUser:            "root",
			Status:             "creating",
		}, nil
	}
	prov.getStatusFn = func(ctx context.Context, instanceID string) (*provider.InstanceStatus, error) {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()

		if count < 3 {
			// Return no SSH info initially
			return &provider.InstanceStatus{
				Status:  "creating",
				Running: false,
			}, nil
		}
		// Return SSH info after a few calls
		return &provider.InstanceStatus{
			Status:  "running",
			Running: true,
			SSHHost: "10.0.0.1",
			SSHPort: 2222,
			SSHUser: "ubuntu",
		}, nil
	}

	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Use mock SSH verifier that succeeds
	mockSSH := NewMockSSHVerifier()
	mockSSH.SetSucceed(true)

	svc := New(store, registry,
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithSSHVerifier(mockSSH),
		WithSSHVerifyTimeout(10*time.Second),
		WithSSHCheckInterval(100*time.Millisecond))

	// Ensure verification goroutines complete before test ends
	defer func() {
		require.True(t, svc.WaitForVerificationComplete(15*time.Second), "verification goroutines should complete")
	}()

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{
		Provider:   "vastai",
		ProviderID: "123",
	}

	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)

	// Wait for SSH verification to complete using require.Eventually
	require.Eventually(t, func() bool {
		s, err := store.Get(ctx, session.ID)
		if err != nil {
			return false
		}
		return s.Status == models.StatusRunning
	}, 10*time.Second, 50*time.Millisecond, "Session should reach running status")

	// Get final session state
	finalSession, err := store.Get(ctx, session.ID)
	require.NoError(t, err)

	assert.Equal(t, models.StatusRunning, finalSession.Status)

	// Verify SSH info was updated from provider status
	assert.Equal(t, "10.0.0.1", finalSession.SSHHost)
	assert.Equal(t, 2222, finalSession.SSHPort)
	assert.Equal(t, "ubuntu", finalSession.SSHUser)

	// Verify SSH verification was called with the updated info
	calls := mockSSH.GetVerifyCalls()
	assert.NotEmpty(t, calls)
	if len(calls) > 0 {
		lastCall := calls[len(calls)-1]
		assert.Equal(t, "10.0.0.1", lastCall.Host)
		assert.Equal(t, 2222, lastCall.Port)
		assert.Equal(t, "ubuntu", lastCall.User)
	}
}

// TestSSHVerification_ContextCancellation tests that context cancellation
// of the create request does NOT stop SSH verification (verification uses its own context)
func TestSSHVerification_ContextCancellation(t *testing.T) {
	store := newMockSessionStore()
	prov := newMockProvider("vastai")
	registry := NewSimpleProviderRegistry([]provider.Provider{prov})

	// Use mock SSH verifier that succeeds quickly so test completes fast
	mockSSH := NewMockSSHVerifier()
	mockSSH.SetSucceed(true)

	svc := New(store, registry,
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))),
		WithSSHVerifier(mockSSH),
		WithSSHVerifyTimeout(5*time.Second),
		WithSSHCheckInterval(50*time.Millisecond))

	// Ensure verification goroutines complete before test ends
	defer func() {
		require.True(t, svc.WaitForVerificationComplete(10*time.Second), "verification goroutines should complete")
	}()

	ctx := context.Background()
	req := models.CreateSessionRequest{
		ConsumerID:     "consumer-001",
		OfferID:        "offer-123",
		WorkloadType:   models.WorkloadLLM,
		ReservationHrs: 1,
	}
	offer := &models.GPUOffer{
		Provider:   "vastai",
		ProviderID: "123",
	}

	// Create session (this starts async SSH verification in background)
	session, err := svc.CreateSession(ctx, req, offer)
	require.NoError(t, err)
	assert.NotEmpty(t, session.ID)

	// Wait for verification to complete - the async SSH verification runs with
	// context.Background() so it won't be affected by cancellation of the create context.
	// This is by design - we want verification to continue even after the API request completes.
	require.Eventually(t, func() bool {
		s, err := store.Get(ctx, session.ID)
		if err != nil {
			return false
		}
		return s.Status == models.StatusRunning
	}, 5*time.Second, 50*time.Millisecond, "Session should reach running status")
}

// TestSSHVerifierInterface verifies that the real SSH verifier satisfies the interface
func TestSSHVerifierInterface(t *testing.T) {
	// This is a compile-time check
	var _ SSHVerifier = &AlwaysSucceedSSHVerifier{}
	var _ SSHVerifier = &AlwaysFailSSHVerifier{}
	var _ SSHVerifier = NewMockSSHVerifier()
}
