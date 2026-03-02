package provisioner

import (
	"context"
	"sync"
	"time"
)

// MockSSHVerifier is a mock SSH verifier for testing
type MockSSHVerifier struct {
	mu sync.Mutex

	// Configuration
	shouldSucceed bool
	delay         time.Duration
	failureError  error

	// Tracking
	verifyCalls []MockVerifyCall
}

// MockVerifyCall records a call to VerifyOnce
type MockVerifyCall struct {
	Host       string
	Port       int
	User       string
	PrivateKey string
	Timestamp  time.Time
}

// NewMockSSHVerifier creates a new mock SSH verifier
func NewMockSSHVerifier() *MockSSHVerifier {
	return &MockSSHVerifier{
		shouldSucceed: true,
	}
}

// SetSucceed configures the mock to succeed or fail
func (m *MockSSHVerifier) SetSucceed(succeed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldSucceed = succeed
}

// SetDelay configures a delay before returning
func (m *MockSSHVerifier) SetDelay(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = d
}

// SetFailureError configures the error to return on failure
func (m *MockSSHVerifier) SetFailureError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureError = err
}

// VerifyOnce implements SSHVerifier
func (m *MockSSHVerifier) VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error {
	m.mu.Lock()
	call := MockVerifyCall{
		Host:       host,
		Port:       port,
		User:       user,
		PrivateKey: privateKey,
		Timestamp:  time.Now(),
	}
	m.verifyCalls = append(m.verifyCalls, call)
	shouldSucceed := m.shouldSucceed
	delay := m.delay
	failureError := m.failureError
	m.mu.Unlock()

	// Apply delay if configured
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if !shouldSucceed {
		if failureError != nil {
			return failureError
		}
		return &MockSSHError{Message: "mock SSH verification failed"}
	}

	return nil
}

// GetVerifyCalls returns all recorded verify calls
func (m *MockSSHVerifier) GetVerifyCalls() []MockVerifyCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := make([]MockVerifyCall, len(m.verifyCalls))
	copy(calls, m.verifyCalls)
	return calls
}

// Reset clears all recorded calls and resets configuration
func (m *MockSSHVerifier) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifyCalls = nil
	m.shouldSucceed = true
	m.delay = 0
	m.failureError = nil
}

// MockSSHError is the error returned by the mock verifier
type MockSSHError struct {
	Message string
}

func (e *MockSSHError) Error() string {
	return e.Message
}

// AlwaysSucceedSSHVerifier is a simple verifier that always succeeds immediately
type AlwaysSucceedSSHVerifier struct{}

// VerifyOnce always returns nil (success)
func (v *AlwaysSucceedSSHVerifier) VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error {
	return nil
}

// AlwaysFailSSHVerifier is a simple verifier that always fails
type AlwaysFailSSHVerifier struct {
	Error error
}

// VerifyOnce always returns an error
func (v *AlwaysFailSSHVerifier) VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error {
	if v.Error != nil {
		return v.Error
	}
	return &MockSSHError{Message: "SSH verification always fails"}
}
