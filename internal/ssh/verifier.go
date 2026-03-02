package ssh

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// DefaultVerifyTimeout is how long to wait for SSH verification
	DefaultVerifyTimeout = 5 * time.Minute

	// DefaultCheckInterval is how often to retry SSH connection
	DefaultCheckInterval = 15 * time.Second

	// DefaultConnectTimeout is the timeout for each SSH connection attempt
	DefaultConnectTimeout = 30 * time.Second

	// VerifyCommand is the command run to verify SSH access
	VerifyCommand = "echo ok"
)

// VerifyResult contains the result of SSH verification
type VerifyResult struct {
	Success   bool
	Duration  time.Duration
	Attempts  int
	LastError string
}

// Verifier handles SSH verification of GPU instances
type Verifier struct {
	verifyTimeout  time.Duration
	checkInterval  time.Duration
	connectTimeout time.Duration
}

// Option configures the Verifier
type Option func(*Verifier)

// WithVerifyTimeout sets the total verification timeout
func WithVerifyTimeout(d time.Duration) Option {
	return func(v *Verifier) {
		v.verifyTimeout = d
	}
}

// WithCheckInterval sets the interval between connection attempts
func WithCheckInterval(d time.Duration) Option {
	return func(v *Verifier) {
		v.checkInterval = d
	}
}

// WithConnectTimeout sets the timeout for each connection attempt
func WithConnectTimeout(d time.Duration) Option {
	return func(v *Verifier) {
		v.connectTimeout = d
	}
}

// NewVerifier creates a new SSH verifier
func NewVerifier(opts ...Option) *Verifier {
	v := &Verifier{
		verifyTimeout:  DefaultVerifyTimeout,
		checkInterval:  DefaultCheckInterval,
		connectTimeout: DefaultConnectTimeout,
	}

	for _, opt := range opts {
		opt(v)
	}

	return v
}

// Verify attempts to verify SSH connectivity by connecting and running "echo ok".
// It retries at checkInterval until verifyTimeout is reached.
func (v *Verifier) Verify(ctx context.Context, host string, port int, user, privateKey string) (*VerifyResult, error) {
	if host == "" {
		return nil, fmt.Errorf("host cannot be empty")
	}
	if port <= 0 {
		return nil, fmt.Errorf("port must be positive")
	}
	if user == "" {
		return nil, fmt.Errorf("user cannot be empty")
	}
	if privateKey == "" {
		return nil, fmt.Errorf("private key cannot be empty")
	}

	// Parse the private key once, outside the retry loop
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return &VerifyResult{
			Success:   false,
			Duration:  0,
			Attempts:  0,
			LastError: fmt.Sprintf("failed to parse private key: %v", err),
		}, fmt.Errorf("failed to parse private key: %w", err)
	}

	start := time.Now()
	deadline := start.Add(v.verifyTimeout)
	attempts := 0
	var lastError string

	for {
		attempts++

		// Check if we've exceeded the deadline
		if time.Now().After(deadline) {
			return &VerifyResult{
				Success:   false,
				Duration:  time.Since(start),
				Attempts:  attempts,
				LastError: lastError,
			}, fmt.Errorf("SSH verification timeout after %d attempts: %s", attempts, lastError)
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return &VerifyResult{
				Success:   false,
				Duration:  time.Since(start),
				Attempts:  attempts,
				LastError: ctx.Err().Error(),
			}, ctx.Err()
		default:
		}

		// Attempt SSH connection
		err := v.tryConnect(ctx, host, port, user, signer)
		if err == nil {
			return &VerifyResult{
				Success:  true,
				Duration: time.Since(start),
				Attempts: attempts,
			}, nil
		}

		lastError = err.Error()

		// Calculate sleep duration, respecting deadline
		sleepDuration := v.checkInterval
		timeUntilDeadline := time.Until(deadline)
		if timeUntilDeadline <= 0 {
			// Deadline already passed, will be caught at top of loop
			continue
		}
		if sleepDuration > timeUntilDeadline {
			sleepDuration = timeUntilDeadline
		}

		// Wait before next attempt
		select {
		case <-ctx.Done():
			return &VerifyResult{
				Success:   false,
				Duration:  time.Since(start),
				Attempts:  attempts,
				LastError: ctx.Err().Error(),
			}, ctx.Err()
		case <-time.After(sleepDuration):
			// Continue to next attempt
		}
	}
}

// tryConnect attempts a single SSH connection and runs "echo ok"
func (v *Verifier) tryConnect(ctx context.Context, host string, port int, user string, signer ssh.Signer) error {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // GPU instances have dynamic host keys
		Timeout:         v.connectTimeout,
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	// Create a connection with context support
	dialer := net.Dialer{Timeout: v.connectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	// Wrap the connection with SSH
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SSH handshake failed: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	// Create a session and run the verify command
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Use a goroutine to run the command with context cancellation
	done := make(chan error, 1)
	go func() {
		done <- session.Run(VerifyCommand)
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("verify command failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
		}
		output := strings.TrimSpace(stdout.String())
		if output != "ok" {
			return fmt.Errorf("unexpected verify output: %q", output)
		}
		return nil
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return ctx.Err()
	}
}

// VerifyOnce attempts a single SSH connection verification (no retries)
func (v *Verifier) VerifyOnce(ctx context.Context, host string, port int, user, privateKey string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if port <= 0 {
		return fmt.Errorf("port must be positive")
	}
	if user == "" {
		return fmt.Errorf("user cannot be empty")
	}
	if privateKey == "" {
		return fmt.Errorf("private key cannot be empty")
	}

	// Parse the private key
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	return v.tryConnect(ctx, host, port, user, signer)
}
