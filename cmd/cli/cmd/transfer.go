package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/filetransfer"
	"github.com/spf13/cobra"
)

var (
	transferKeyFile string
	transferTimeout time.Duration
)

var transferCmd = &cobra.Command{
	Use:   "transfer",
	Short: "Transfer files to/from GPU sessions",
	Long: `Transfer files to and from GPU sessions using SFTP.

Examples:
  # Upload a file to a session
  gpu-shopper transfer upload ./local-file.txt session-123:/remote/path/file.txt -k ~/.ssh/session_key

  # Download a file from a session
  gpu-shopper transfer download session-123:/remote/path/file.txt ./local-file.txt -k ~/.ssh/session_key`,
}

var uploadCmd = &cobra.Command{
	Use:   "upload <local-path> <session-id>:<remote-path>",
	Short: "Upload a file to a session",
	Long: `Upload a local file to a GPU session using SFTP.

The destination must be specified as <session-id>:<remote-path>.

Examples:
  gpu-shopper transfer upload ./model.bin abc123:/home/user/model.bin -k ~/.ssh/key
  gpu-shopper transfer upload ./script.py abc123:/workspace/script.py -k ~/.ssh/key`,
	Args: cobra.ExactArgs(2),
	RunE: runUpload,
}

var downloadCmd = &cobra.Command{
	Use:   "download <session-id>:<remote-path> <local-path>",
	Short: "Download a file from a session",
	Long: `Download a file from a GPU session using SFTP.

The source must be specified as <session-id>:<remote-path>.

Examples:
  gpu-shopper transfer download abc123:/home/user/output.txt ./output.txt -k ~/.ssh/key
  gpu-shopper transfer download abc123:/workspace/model.bin ./model.bin -k ~/.ssh/key`,
	Args: cobra.ExactArgs(2),
	RunE: runDownload,
}

func init() {
	rootCmd.AddCommand(transferCmd)
	transferCmd.AddCommand(uploadCmd)
	transferCmd.AddCommand(downloadCmd)

	transferCmd.PersistentFlags().StringVarP(&transferKeyFile, "key", "k", "", "SSH private key file (required)")
	transferCmd.PersistentFlags().DurationVarP(&transferTimeout, "timeout", "t", 5*time.Minute, "Transfer timeout")
	transferCmd.MarkPersistentFlagRequired("key")
}

// parseSessionPath parses a string like "session-id:/path/to/file" into session ID and path
func parseSessionPath(s string) (sessionID, path string, err error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format, expected <session-id>:<path>, got %q", s)
	}
	sessionID = strings.TrimSpace(parts[0])
	path = strings.TrimSpace(parts[1])
	if sessionID == "" {
		return "", "", fmt.Errorf("session ID cannot be empty")
	}
	if path == "" {
		return "", "", fmt.Errorf("path cannot be empty")
	}
	return sessionID, path, nil
}

// getSessionDetails fetches session information from the API
func getSessionDetails(sessionID string) (*Session, error) {
	reqURL := fmt.Sprintf("%s/api/v1/sessions/%s", serverURL, sessionID)
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &session, nil
}

// readPrivateKey reads the private key from a file
func readPrivateKey(keyFile string) ([]byte, error) {
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %w", err)
	}
	return keyData, nil
}

// createTransfer creates a Transfer instance from session details and key file
func createTransfer(session *Session, keyData []byte) (*filetransfer.Transfer, error) {
	if session.SSHHost == "" {
		return nil, fmt.Errorf("session has no SSH host (status: %s)", session.Status)
	}
	if session.SSHPort == 0 {
		return nil, fmt.Errorf("session has no SSH port")
	}
	if session.SSHUser == "" {
		return nil, fmt.Errorf("session has no SSH user")
	}
	if session.Status != "running" {
		return nil, fmt.Errorf("session is not running (status: %s)", session.Status)
	}

	creds := filetransfer.Credentials{
		Host:       session.SSHHost,
		Port:       session.SSHPort,
		User:       session.SSHUser,
		PrivateKey: keyData,
	}

	return filetransfer.New(creds, filetransfer.WithConnectTimeout(30*time.Second)), nil
}

func runUpload(cmd *cobra.Command, args []string) error {
	localPath := args[0]
	sessionPath := args[1]

	// Parse destination
	sessionID, remotePath, err := parseSessionPath(sessionPath)
	if err != nil {
		return fmt.Errorf("invalid destination: %w", err)
	}

	// Verify local file exists
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return fmt.Errorf("local file does not exist: %s", localPath)
	}

	// Read private key
	keyData, err := readPrivateKey(transferKeyFile)
	if err != nil {
		return err
	}

	// Get session details
	fmt.Printf("Fetching session %s...\n", sessionID)
	session, err := getSessionDetails(sessionID)
	if err != nil {
		return err
	}

	// Create transfer client
	transfer, err := createTransfer(session, keyData)
	if err != nil {
		return err
	}

	// Perform upload
	ctx, cancel := context.WithTimeout(context.Background(), transferTimeout)
	defer cancel()

	fmt.Printf("Uploading %s to %s@%s:%d:%s...\n",
		localPath, session.SSHUser, session.SSHHost, session.SSHPort, remotePath)

	if err := transfer.Upload(ctx, localPath, remotePath); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	fmt.Println("Upload complete.")
	return nil
}

func runDownload(cmd *cobra.Command, args []string) error {
	sessionPath := args[0]
	localPath := args[1]

	// Parse source
	sessionID, remotePath, err := parseSessionPath(sessionPath)
	if err != nil {
		return fmt.Errorf("invalid source: %w", err)
	}

	// Read private key
	keyData, err := readPrivateKey(transferKeyFile)
	if err != nil {
		return err
	}

	// Get session details
	fmt.Printf("Fetching session %s...\n", sessionID)
	session, err := getSessionDetails(sessionID)
	if err != nil {
		return err
	}

	// Create transfer client
	transfer, err := createTransfer(session, keyData)
	if err != nil {
		return err
	}

	// Perform download
	ctx, cancel := context.WithTimeout(context.Background(), transferTimeout)
	defer cancel()

	fmt.Printf("Downloading %s@%s:%d:%s to %s...\n",
		session.SSHUser, session.SSHHost, session.SSHPort, remotePath, localPath)

	if err := transfer.Download(ctx, remotePath, localPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	fmt.Println("Download complete.")
	return nil
}
