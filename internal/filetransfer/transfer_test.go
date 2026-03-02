package filetransfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentials_Validate(t *testing.T) {
	tests := []struct {
		name    string
		creds   Credentials
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid credentials",
			creds: Credentials{
				Host:       "example.com",
				Port:       22,
				User:       "testuser",
				PrivateKey: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----"),
			},
			wantErr: false,
		},
		{
			name: "empty host",
			creds: Credentials{
				Host:       "",
				Port:       22,
				User:       "testuser",
				PrivateKey: []byte("key"),
			},
			wantErr: true,
			errMsg:  "host cannot be empty",
		},
		{
			name: "zero port",
			creds: Credentials{
				Host:       "example.com",
				Port:       0,
				User:       "testuser",
				PrivateKey: []byte("key"),
			},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name: "negative port",
			creds: Credentials{
				Host:       "example.com",
				Port:       -1,
				User:       "testuser",
				PrivateKey: []byte("key"),
			},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name: "port too high",
			creds: Credentials{
				Host:       "example.com",
				Port:       70000,
				User:       "testuser",
				PrivateKey: []byte("key"),
			},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name: "empty user",
			creds: Credentials{
				Host:       "example.com",
				Port:       22,
				User:       "",
				PrivateKey: []byte("key"),
			},
			wantErr: true,
			errMsg:  "user cannot be empty",
		},
		{
			name: "empty private key",
			creds: Credentials{
				Host:       "example.com",
				Port:       22,
				User:       "testuser",
				PrivateKey: nil,
			},
			wantErr: true,
			errMsg:  "private key cannot be empty",
		},
		{
			name: "zero-length private key",
			creds: Credentials{
				Host:       "example.com",
				Port:       22,
				User:       "testuser",
				PrivateKey: []byte{},
			},
			wantErr: true,
			errMsg:  "private key cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.creds.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
					return
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestNew(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("key"),
	}

	t.Run("default options", func(t *testing.T) {
		transfer := New(creds)
		if transfer == nil {
			t.Error("New() returned nil")
			return
		}
		if transfer.connectTimeout != DefaultConnectTimeout {
			t.Errorf("New() connectTimeout = %v, want %v", transfer.connectTimeout, DefaultConnectTimeout)
		}
		if transfer.creds.Host != creds.Host {
			t.Errorf("New() creds.Host = %q, want %q", transfer.creds.Host, creds.Host)
		}
	})

	t.Run("with custom timeout", func(t *testing.T) {
		customTimeout := 60 * time.Second
		transfer := New(creds, WithConnectTimeout(customTimeout))
		if transfer == nil {
			t.Error("New() returned nil")
			return
		}
		if transfer.connectTimeout != customTimeout {
			t.Errorf("New() connectTimeout = %v, want %v", transfer.connectTimeout, customTimeout)
		}
	})
}

func TestTransfer_Upload_InvalidLocalPath(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("key"),
	}
	transfer := New(creds)
	ctx := context.Background()

	t.Run("empty local path", func(t *testing.T) {
		err := transfer.Upload(ctx, "", "/remote/path")
		if err == nil {
			t.Error("Upload() expected error for empty local path")
			return
		}
		if err.Error() != "local path cannot be empty" {
			t.Errorf("Upload() error = %q, want %q", err.Error(), "local path cannot be empty")
		}
	})

	t.Run("empty remote path", func(t *testing.T) {
		err := transfer.Upload(ctx, "/local/path", "")
		if err == nil {
			t.Error("Upload() expected error for empty remote path")
			return
		}
		if err.Error() != "remote path cannot be empty" {
			t.Errorf("Upload() error = %q, want %q", err.Error(), "remote path cannot be empty")
		}
	})

	t.Run("nonexistent local file", func(t *testing.T) {
		err := transfer.Upload(ctx, "/nonexistent/file/path", "/remote/path")
		if err == nil {
			t.Error("Upload() expected error for nonexistent local file")
			return
		}
		// Error should mention stat failure
		if !contains(err.Error(), "failed to stat local file") {
			t.Errorf("Upload() error = %q, want error containing 'failed to stat local file'", err.Error())
		}
	})

	t.Run("local path is directory", func(t *testing.T) {
		tempDir := t.TempDir()
		err := transfer.Upload(ctx, tempDir, "/remote/path")
		if err == nil {
			t.Error("Upload() expected error when local path is directory")
			return
		}
		if err.Error() != "local path is a directory, not a file" {
			t.Errorf("Upload() error = %q, want %q", err.Error(), "local path is a directory, not a file")
		}
	})
}

func TestTransfer_Download_InvalidPaths(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("key"),
	}
	transfer := New(creds)
	ctx := context.Background()

	t.Run("empty remote path", func(t *testing.T) {
		err := transfer.Download(ctx, "", "/local/path")
		if err == nil {
			t.Error("Download() expected error for empty remote path")
			return
		}
		if err.Error() != "remote path cannot be empty" {
			t.Errorf("Download() error = %q, want %q", err.Error(), "remote path cannot be empty")
		}
	})

	t.Run("empty local path", func(t *testing.T) {
		err := transfer.Download(ctx, "/remote/path", "")
		if err == nil {
			t.Error("Download() expected error for empty local path")
			return
		}
		if err.Error() != "local path cannot be empty" {
			t.Errorf("Download() error = %q, want %q", err.Error(), "local path cannot be empty")
		}
	})
}

func TestTransfer_Connect_InvalidPrivateKey(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("not a valid key"),
	}
	transfer := New(creds)
	ctx := context.Background()

	// Create a temp file to satisfy the upload file check
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(tempFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	t.Run("upload with invalid key", func(t *testing.T) {
		err := transfer.Upload(ctx, tempFile, "/remote/path")
		if err == nil {
			t.Error("Upload() expected error for invalid private key")
			return
		}
		// Should fail during connect with key parsing error
		if !contains(err.Error(), "failed to parse private key") {
			t.Errorf("Upload() error = %q, want error containing 'failed to parse private key'", err.Error())
		}
	})

	t.Run("download with invalid key", func(t *testing.T) {
		err := transfer.Download(ctx, "/remote/path", tempFile)
		if err == nil {
			t.Error("Download() expected error for invalid private key")
			return
		}
		// Should fail during connect with key parsing error
		if !contains(err.Error(), "failed to parse private key") {
			t.Errorf("Download() error = %q, want error containing 'failed to parse private key'", err.Error())
		}
	})
}

func TestTransfer_ListRemoteDir_EmptyPath(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("key"),
	}
	transfer := New(creds)
	ctx := context.Background()

	_, err := transfer.ListRemoteDir(ctx, "")
	if err == nil {
		t.Error("ListRemoteDir() expected error for empty path")
		return
	}
	if err.Error() != "remote path cannot be empty" {
		t.Errorf("ListRemoteDir() error = %q, want %q", err.Error(), "remote path cannot be empty")
	}
}

func TestTransfer_RemoteFileExists_EmptyPath(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("key"),
	}
	transfer := New(creds)
	ctx := context.Background()

	_, err := transfer.RemoteFileExists(ctx, "")
	if err == nil {
		t.Error("RemoteFileExists() expected error for empty path")
		return
	}
	if err.Error() != "remote path cannot be empty" {
		t.Errorf("RemoteFileExists() error = %q, want %q", err.Error(), "remote path cannot be empty")
	}
}

func TestTransfer_ContextCancellation(t *testing.T) {
	creds := Credentials{
		Host:       "example.com",
		Port:       22,
		User:       "testuser",
		PrivateKey: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----"),
	}
	transfer := New(creds, WithConnectTimeout(1*time.Second))

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Create a temp file
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(tempFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	t.Run("upload with cancelled context", func(t *testing.T) {
		err := transfer.Upload(ctx, tempFile, "/remote/path")
		if err == nil {
			t.Error("Upload() expected error with cancelled context")
			return
		}
		// Should fail with context cancellation or connection error
		// (we can't control which happens first)
	})

	t.Run("download with cancelled context", func(t *testing.T) {
		err := transfer.Download(ctx, "/remote/path", filepath.Join(tempDir, "output.txt"))
		if err == nil {
			t.Error("Download() expected error with cancelled context")
		}
	})
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Integration test - skipped in CI, requires real SSH host
func TestTransfer_Integration(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=1 to run")
	}

	host := os.Getenv("SSH_HOST")
	user := os.Getenv("SSH_USER")
	keyFile := os.Getenv("SSH_KEY_FILE")

	if host == "" || user == "" || keyFile == "" {
		t.Skip("Skipping integration test. Set SSH_HOST, SSH_USER, and SSH_KEY_FILE")
	}

	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("Failed to read key file: %v", err)
	}

	creds := Credentials{
		Host:       host,
		Port:       22,
		User:       user,
		PrivateKey: keyData,
	}
	transfer := New(creds)
	ctx := context.Background()

	// Create a temp file for upload
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "test_upload.txt")
	testContent := []byte("Hello, GPU Shopper!")
	if err := os.WriteFile(localFile, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	remotePath := "/tmp/gpu_shopper_test.txt"

	t.Run("upload and download roundtrip", func(t *testing.T) {
		// Upload
		if err := transfer.Upload(ctx, localFile, remotePath); err != nil {
			t.Fatalf("Upload failed: %v", err)
		}

		// Verify remote file exists
		exists, err := transfer.RemoteFileExists(ctx, remotePath)
		if err != nil {
			t.Fatalf("RemoteFileExists failed: %v", err)
		}
		if !exists {
			t.Error("Remote file should exist after upload")
		}

		// Download
		downloadPath := filepath.Join(tempDir, "test_download.txt")
		if err := transfer.Download(ctx, remotePath, downloadPath); err != nil {
			t.Fatalf("Download failed: %v", err)
		}

		// Verify content
		downloadedContent, err := os.ReadFile(downloadPath)
		if err != nil {
			t.Fatalf("Failed to read downloaded file: %v", err)
		}
		if string(downloadedContent) != string(testContent) {
			t.Errorf("Downloaded content = %q, want %q", string(downloadedContent), string(testContent))
		}
	})
}
