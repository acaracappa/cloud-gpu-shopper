//go:build live
// +build live

package live

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHHelper provides SSH connectivity to GPU nodes
type SSHHelper struct {
	host       string
	port       int
	user       string
	privateKey string
	client     *ssh.Client
}

// NewSSHHelper creates a new SSH helper
func NewSSHHelper(host string, port int, user, privateKey string) *SSHHelper {
	return &SSHHelper{
		host:       host,
		port:       port,
		user:       user,
		privateKey: privateKey,
	}
}

// Connect establishes the SSH connection with a timeout
func (s *SSHHelper) Connect(ctx context.Context) error {
	// Parse the private key
	signer, err := ssh.ParsePrivateKey([]byte(s.privateKey))
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: s.user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For testing only
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	// Create a connection with context support
	dialer := net.Dialer{Timeout: 30 * time.Second}
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

	s.client = ssh.NewClient(sshConn, chans, reqs)
	return nil
}

// RunCommand executes a command on the remote host
func (s *SSHHelper) RunCommand(ctx context.Context, cmd string) (stdout, stderr string, err error) {
	if s.client == nil {
		return "", "", fmt.Errorf("not connected")
	}

	session, err := s.client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	// Use a goroutine to run the command with context cancellation
	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	select {
	case err = <-done:
		stdout = strings.TrimSpace(stdoutBuf.String())
		stderr = strings.TrimSpace(stderrBuf.String())
		return stdout, stderr, err
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return "", "", ctx.Err()
	}
}

// RunCommandWithOutput runs a command and returns combined output
func (s *SSHHelper) RunCommandWithOutput(ctx context.Context, cmd string) (string, error) {
	stdout, stderr, err := s.RunCommand(ctx, cmd)
	if err != nil {
		if stderr != "" {
			return stdout, fmt.Errorf("%w: %s", err, stderr)
		}
		return stdout, err
	}
	return stdout, nil
}

// CopyOutput reads output from a command without waiting for completion
func (s *SSHHelper) CopyOutput(ctx context.Context, cmd string, w io.Writer) error {
	if s.client == nil {
		return fmt.Errorf("not connected")
	}

	session, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(w, stdout)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return ctx.Err()
	}
}

// FileExists checks if a file exists on the remote host
func (s *SSHHelper) FileExists(ctx context.Context, path string) (bool, error) {
	_, _, err := s.RunCommand(ctx, fmt.Sprintf("test -f %q && echo exists", path))
	if err != nil {
		// Command failed, file doesn't exist
		return false, nil
	}
	return true, nil
}

// ReadFile reads a file from the remote host
func (s *SSHHelper) ReadFile(ctx context.Context, path string) (string, error) {
	return s.RunCommandWithOutput(ctx, fmt.Sprintf("cat %q", path))
}

// TailFile reads the last N lines of a file
func (s *SSHHelper) TailFile(ctx context.Context, path string, lines int) (string, error) {
	return s.RunCommandWithOutput(ctx, fmt.Sprintf("tail -n %d %q 2>/dev/null || echo 'File not found'", lines, path))
}

// ProcessRunning checks if a process is running by name
func (s *SSHHelper) ProcessRunning(ctx context.Context, processName string) (bool, error) {
	stdout, _, err := s.RunCommand(ctx, fmt.Sprintf("pgrep -f %q", processName))
	if err != nil {
		// pgrep returns error if no process found
		return false, nil
	}
	return stdout != "", nil
}

// GetProcessList returns the process list
func (s *SSHHelper) GetProcessList(ctx context.Context) (string, error) {
	return s.RunCommandWithOutput(ctx, "ps aux")
}

// GetEnvironment returns environment variables (filtered for secrets)
func (s *SSHHelper) GetEnvironment(ctx context.Context) (string, error) {
	// Filter out potentially sensitive values
	return s.RunCommandWithOutput(ctx, "env | grep -E '^SHOPPER_' | sed 's/TOKEN=.*/TOKEN=[REDACTED]/'")
}

// GetNetworkStatus returns network listening ports
func (s *SSHHelper) GetNetworkStatus(ctx context.Context) (string, error) {
	return s.RunCommandWithOutput(ctx, "ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || echo 'Network tools unavailable'")
}

// GetNvidiaSMI runs nvidia-smi and returns output
func (s *SSHHelper) GetNvidiaSMI(ctx context.Context) (string, error) {
	return s.RunCommandWithOutput(ctx, "nvidia-smi 2>/dev/null || echo 'nvidia-smi not available'")
}

// CurlEndpoint makes an HTTP request from the remote host
func (s *SSHHelper) CurlEndpoint(ctx context.Context, url string) (string, error) {
	return s.RunCommandWithOutput(ctx, fmt.Sprintf("curl -s %q", url))
}

// BlockHost adds an iptables rule to block traffic to a host (requires root)
func (s *SSHHelper) BlockHost(ctx context.Context, host string) error {
	_, _, err := s.RunCommand(ctx, fmt.Sprintf("sudo iptables -A OUTPUT -d %s -j DROP", host))
	return err
}

// UnblockHost removes the iptables rule blocking traffic to a host
func (s *SSHHelper) UnblockHost(ctx context.Context, host string) error {
	_, _, err := s.RunCommand(ctx, fmt.Sprintf("sudo iptables -D OUTPUT -d %s -j DROP", host))
	return err
}

// IsConnected returns whether the SSH connection is established
func (s *SSHHelper) IsConnected() bool {
	return s.client != nil
}

// Close closes the SSH connection
func (s *SSHHelper) Close() error {
	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}

// SSHConnectionInfo holds SSH connection details from a session
type SSHConnectionInfo struct {
	Host       string
	Port       int
	User       string
	PrivateKey string
}

// NewSSHHelperFromSession creates an SSH helper from session response
func NewSSHHelperFromSession(session *Session, privateKey string) *SSHHelper {
	return NewSSHHelper(session.SSHHost, session.SSHPort, session.SSHUser, privateKey)
}
