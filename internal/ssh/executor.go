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
	// DefaultExecutorConnectTimeout is the default timeout for establishing SSH connections
	DefaultExecutorConnectTimeout = 30 * time.Second

	// DefaultExecutorCommandTimeout is the default timeout for command execution
	DefaultExecutorCommandTimeout = 60 * time.Second
)

// Connection represents an established SSH connection to a host
type Connection struct {
	client *ssh.Client
	host   string
	port   int
	user   string
}

// Host returns the connection's host
func (c *Connection) Host() string {
	return c.host
}

// Port returns the connection's port
func (c *Connection) Port() int {
	return c.port
}

// User returns the connection's user
func (c *Connection) User() string {
	return c.user
}

// Close closes the SSH connection
func (c *Connection) Close() error {
	if c.client != nil {
		err := c.client.Close()
		c.client = nil
		return err
	}
	return nil
}

// Executor provides SSH command execution for production use.
// Pattern: Create executor with options, connect to hosts, run commands, close when done.
type Executor struct {
	connectTimeout time.Duration
	commandTimeout time.Duration
}

// ExecutorOption configures the Executor
type ExecutorOption func(*Executor)

// WithExecutorConnectTimeout sets the connection timeout for the executor
func WithExecutorConnectTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.connectTimeout = d
	}
}

// WithExecutorCommandTimeout sets the command execution timeout for the executor
func WithExecutorCommandTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.commandTimeout = d
	}
}

// NewExecutor creates an executor with configurable timeouts
func NewExecutor(opts ...ExecutorOption) *Executor {
	e := &Executor{
		connectTimeout: DefaultExecutorConnectTimeout,
		commandTimeout: DefaultExecutorCommandTimeout,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Connect establishes SSH connection to a host
func (e *Executor) Connect(ctx context.Context, host string, port int, user, privateKey string) (*Connection, error) {
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

	// Parse the private key
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // GPU instances have dynamic host keys
		Timeout:         e.connectTimeout,
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	// Create a connection with context support
	dialer := net.Dialer{Timeout: e.connectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	// Wrap the connection with SSH
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake failed: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	return &Connection{
		client: client,
		host:   host,
		port:   port,
		user:   user,
	}, nil
}

// RunCommand executes a command and returns stdout/stderr
func (e *Executor) RunCommand(ctx context.Context, conn *Connection, cmd string) (stdout, stderr string, err error) {
	if conn == nil || conn.client == nil {
		return "", "", fmt.Errorf("connection is nil or closed")
	}

	session, err := conn.client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	// Create a context with command timeout if not already set
	cmdCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, e.commandTimeout)
		defer cancel()
	}

	// Use a goroutine to run the command with context cancellation
	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case runErr := <-done:
		stdout = strings.TrimSpace(stdoutBuf.String())
		stderr = strings.TrimSpace(stderrBuf.String())
		return stdout, stderr, runErr
	case <-cmdCtx.Done():
		session.Signal(ssh.SIGKILL)
		return "", "", fmt.Errorf("command timed out: %w", cmdCtx.Err())
	}
}

// CheckHealth verifies the connection is responsive by running a simple command
func (e *Executor) CheckHealth(ctx context.Context, conn *Connection) error {
	stdout, stderr, err := e.RunCommand(ctx, conn, "echo ok")
	if err != nil {
		return fmt.Errorf("health check failed: %w (stderr: %s)", err, stderr)
	}
	if stdout != "ok" {
		return fmt.Errorf("health check returned unexpected output: %q", stdout)
	}
	return nil
}

// GetGPUStatus runs nvidia-smi and returns parsed status
func (e *Executor) GetGPUStatus(ctx context.Context, conn *Connection) (*GPUStatus, error) {
	// Use nvidia-smi with CSV format for easy parsing
	cmd := "nvidia-smi --query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu,power.draw --format=csv,noheader,nounits"
	stdout, stderr, err := e.RunCommand(ctx, conn, cmd)
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi failed: %w (stderr: %s)", err, stderr)
	}

	status, err := ParseNvidiaSMI(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to parse nvidia-smi output: %w", err)
	}

	return status, nil
}

// ReadFile retrieves file contents from the remote host
func (e *Executor) ReadFile(ctx context.Context, conn *Connection, path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("path cannot be empty")
	}

	// Use cat to read the file, quoting the path to handle spaces
	stdout, stderr, err := e.RunCommand(ctx, conn, fmt.Sprintf("cat %q", path))
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w (stderr: %s)", path, err, stderr)
	}

	return []byte(stdout), nil
}

// FileExists checks if a file exists on the remote host
func (e *Executor) FileExists(ctx context.Context, conn *Connection, path string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("path cannot be empty")
	}

	_, _, err := e.RunCommand(ctx, conn, fmt.Sprintf("test -f %q", path))
	if err != nil {
		// Command failed, file doesn't exist
		return false, nil
	}
	return true, nil
}

// RunCommandWithCombinedOutput runs a command and returns combined stdout output
// Returns an error that includes stderr if the command fails
func (e *Executor) RunCommandWithCombinedOutput(ctx context.Context, conn *Connection, cmd string) (string, error) {
	stdout, stderr, err := e.RunCommand(ctx, conn, cmd)
	if err != nil {
		if stderr != "" {
			return stdout, fmt.Errorf("%w: %s", err, stderr)
		}
		return stdout, err
	}
	return stdout, nil
}

// GetDiskStatus runs df and returns disk usage for key mount points
func (e *Executor) GetDiskStatus(ctx context.Context, conn *Connection) (*DiskStatus, error) {
	cmd := `df -BG 2>/dev/null | grep -v tmpfs | grep -v "^none"`
	stdout, stderr, err := e.RunCommand(ctx, conn, cmd)
	if err != nil {
		return nil, fmt.Errorf("df failed: %w (stderr: %s)", err, stderr)
	}

	status, err := ParseDiskOutput(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to parse df output: %w", err)
	}

	return status, nil
}

// CheckOOM checks dmesg for OOM killer events
func (e *Executor) CheckOOM(ctx context.Context, conn *Connection) (*OOMStatus, error) {
	cmd := `dmesg -T 2>/dev/null | grep -i "oom\|out of memory\|killed process" | tail -5`
	stdout, _, err := e.RunCommand(ctx, conn, cmd)
	if err != nil {
		// dmesg may require root or may not be available - not fatal
		// grep returns exit code 1 if no matches, which is also fine
		return &OOMStatus{}, nil
	}

	return ParseOOMOutput(stdout), nil
}

// GetCUDAVersion retrieves CUDA version information from the remote host.
// BUG-004: Post-provision CUDA validation to detect version mismatches.
func (e *Executor) GetCUDAVersion(ctx context.Context, conn *Connection) (*CUDAInfo, error) {
	// Use nvidia-smi to get CUDA version
	// The -L flag shows less output but still includes the CUDA version in the header
	stdout, stderr, err := e.RunCommand(ctx, conn, "nvidia-smi")
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi failed: %w (stderr: %s)", err, stderr)
	}

	info, err := ParseCUDAVersion(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CUDA version: %w", err)
	}

	return info, nil
}
