package filetransfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	// DefaultConnectTimeout is the default timeout for establishing SSH connections
	DefaultConnectTimeout = 30 * time.Second
)

// Credentials holds SSH connection details for file transfer
type Credentials struct {
	Host       string
	Port       int
	User       string
	PrivateKey []byte // PEM-encoded private key
}

// Validate checks that the credentials have all required fields
func (c *Credentials) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if c.User == "" {
		return fmt.Errorf("user cannot be empty")
	}
	if len(c.PrivateKey) == 0 {
		return fmt.Errorf("private key cannot be empty")
	}
	return nil
}

// Transfer handles file transfers over SSH/SFTP
type Transfer struct {
	creds          Credentials
	connectTimeout time.Duration
}

// Option configures a Transfer instance
type Option func(*Transfer)

// WithConnectTimeout sets the connection timeout
func WithConnectTimeout(d time.Duration) Option {
	return func(t *Transfer) {
		t.connectTimeout = d
	}
}

// New creates a new Transfer instance with the given credentials
func New(creds Credentials, opts ...Option) *Transfer {
	t := &Transfer{
		creds:          creds,
		connectTimeout: DefaultConnectTimeout,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// Upload copies a local file to the remote session
func (t *Transfer) Upload(ctx context.Context, localPath, remotePath string) error {
	if localPath == "" {
		return fmt.Errorf("local path cannot be empty")
	}
	if remotePath == "" {
		return fmt.Errorf("remote path cannot be empty")
	}

	// Verify local file exists and is readable
	localInfo, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("failed to stat local file: %w", err)
	}
	if localInfo.IsDir() {
		return fmt.Errorf("local path is a directory, not a file")
	}

	client, err := t.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %w", err)
	}
	defer sftpClient.Close()

	// Open local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer localFile.Close()

	// Create remote file (creating parent directories if needed via MkdirAll)
	remoteDir := filepath.Dir(remotePath)
	if remoteDir != "" && remoteDir != "." && remoteDir != "/" {
		// Try to create parent directories, ignore errors (they might already exist)
		_ = sftpClient.MkdirAll(remoteDir)
	}

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()

	// Copy with context cancellation support
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(remoteFile, localFile)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to copy file: %w", err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("upload cancelled: %w", ctx.Err())
	}
}

// Download copies a remote file to the local filesystem
func (t *Transfer) Download(ctx context.Context, remotePath, localPath string) error {
	if remotePath == "" {
		return fmt.Errorf("remote path cannot be empty")
	}
	if localPath == "" {
		return fmt.Errorf("local path cannot be empty")
	}

	client, err := t.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("failed to create sftp client: %w", err)
	}
	defer sftpClient.Close()

	// Open remote file
	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	// Create local directory structure if needed
	localDir := filepath.Dir(localPath)
	if localDir != "" && localDir != "." {
		if err := os.MkdirAll(localDir, 0755); err != nil {
			return fmt.Errorf("failed to create local directory: %w", err)
		}
	}

	// Create local file
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	// Copy with context cancellation support
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(localFile, remoteFile)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			// Clean up partial file on error
			localFile.Close()
			os.Remove(localPath)
			return fmt.Errorf("failed to copy file: %w", err)
		}
		return nil
	case <-ctx.Done():
		// Clean up partial file on cancellation
		localFile.Close()
		os.Remove(localPath)
		return fmt.Errorf("download cancelled: %w", ctx.Err())
	}
}

// connect establishes an SSH connection to the remote host
func (t *Transfer) connect(ctx context.Context) (*ssh.Client, error) {
	if err := t.creds.Validate(); err != nil {
		return nil, fmt.Errorf("invalid credentials: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(t.creds.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: t.creds.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Commodity GPUs have unknown/dynamic host keys
		Timeout:         t.connectTimeout,
	}

	addr := fmt.Sprintf("%s:%d", t.creds.Host, t.creds.Port)

	// Use a dialer that respects context cancellation
	dialer := &ssh.Client{}
	_ = dialer // Avoid unused variable

	// Check context before attempting connection
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", addr, err)
	}

	return client, nil
}

// ListRemoteDir lists files in a remote directory
func (t *Transfer) ListRemoteDir(ctx context.Context, remotePath string) ([]os.FileInfo, error) {
	if remotePath == "" {
		return nil, fmt.Errorf("remote path cannot be empty")
	}

	client, err := t.connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create sftp client: %w", err)
	}
	defer sftpClient.Close()

	files, err := sftpClient.ReadDir(remotePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote directory: %w", err)
	}

	return files, nil
}

// RemoteFileExists checks if a file exists on the remote host
func (t *Transfer) RemoteFileExists(ctx context.Context, remotePath string) (bool, error) {
	if remotePath == "" {
		return false, fmt.Errorf("remote path cannot be empty")
	}

	client, err := t.connect(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return false, fmt.Errorf("failed to create sftp client: %w", err)
	}
	defer sftpClient.Close()

	_, err = sftpClient.Stat(remotePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat remote file: %w", err)
	}

	return true, nil
}
