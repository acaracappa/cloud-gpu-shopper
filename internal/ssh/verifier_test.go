package ssh

import (
	"context"
	"testing"
	"time"
)

func TestNewVerifier(t *testing.T) {
	v := NewVerifier()

	if v.verifyTimeout != DefaultVerifyTimeout {
		t.Errorf("expected default verify timeout %v, got %v", DefaultVerifyTimeout, v.verifyTimeout)
	}
	if v.checkInterval != DefaultCheckInterval {
		t.Errorf("expected default check interval %v, got %v", DefaultCheckInterval, v.checkInterval)
	}
	if v.connectTimeout != DefaultConnectTimeout {
		t.Errorf("expected default connect timeout %v, got %v", DefaultConnectTimeout, v.connectTimeout)
	}
}

func TestNewVerifierWithOptions(t *testing.T) {
	v := NewVerifier(
		WithVerifyTimeout(1*time.Minute),
		WithCheckInterval(5*time.Second),
		WithConnectTimeout(10*time.Second),
	)

	if v.verifyTimeout != 1*time.Minute {
		t.Errorf("expected verify timeout 1m, got %v", v.verifyTimeout)
	}
	if v.checkInterval != 5*time.Second {
		t.Errorf("expected check interval 5s, got %v", v.checkInterval)
	}
	if v.connectTimeout != 10*time.Second {
		t.Errorf("expected connect timeout 10s, got %v", v.connectTimeout)
	}
}

func TestVerify_ValidationErrors(t *testing.T) {
	v := NewVerifier()
	ctx := context.Background()

	tests := []struct {
		name       string
		host       string
		port       int
		user       string
		privateKey string
		wantErr    string
	}{
		{
			name:       "empty host",
			host:       "",
			port:       22,
			user:       "root",
			privateKey: "key",
			wantErr:    "host cannot be empty",
		},
		{
			name:       "invalid port",
			host:       "localhost",
			port:       0,
			user:       "root",
			privateKey: "key",
			wantErr:    "port must be positive",
		},
		{
			name:       "empty user",
			host:       "localhost",
			port:       22,
			user:       "",
			privateKey: "key",
			wantErr:    "user cannot be empty",
		},
		{
			name:       "empty private key",
			host:       "localhost",
			port:       22,
			user:       "root",
			privateKey: "",
			wantErr:    "private key cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.Verify(ctx, tt.host, tt.port, tt.user, tt.privateKey)
			if err == nil {
				t.Error("expected error, got nil")
			}
			if err.Error() != tt.wantErr {
				t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestVerifyOnce_ValidationErrors(t *testing.T) {
	v := NewVerifier()
	ctx := context.Background()

	err := v.VerifyOnce(ctx, "", 22, "root", "key")
	if err == nil {
		t.Error("expected error for empty host")
	}

	err = v.VerifyOnce(ctx, "localhost", 0, "root", "key")
	if err == nil {
		t.Error("expected error for invalid port")
	}

	err = v.VerifyOnce(ctx, "localhost", 22, "", "key")
	if err == nil {
		t.Error("expected error for empty user")
	}

	err = v.VerifyOnce(ctx, "localhost", 22, "root", "")
	if err == nil {
		t.Error("expected error for empty private key")
	}
}

func TestVerify_ContextCancellation(t *testing.T) {
	v := NewVerifier(
		WithVerifyTimeout(10*time.Second),
		WithCheckInterval(100*time.Millisecond),
		WithConnectTimeout(100*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	result, err := v.Verify(ctx, "localhost", 22, "root", "invalid-key")
	if err == nil {
		t.Error("expected error on cancelled context")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}
	if result.Success {
		t.Error("expected Success to be false")
	}
}

func TestVerify_InvalidPrivateKey(t *testing.T) {
	v := NewVerifier(
		WithVerifyTimeout(1*time.Second),
		WithCheckInterval(100*time.Millisecond),
		WithConnectTimeout(100*time.Millisecond),
	)

	ctx := context.Background()

	result, err := v.Verify(ctx, "localhost", 22, "root", "not-a-valid-key")
	if err == nil {
		t.Error("expected error for invalid key")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}
	if result.Success {
		t.Error("expected Success to be false")
	}
	// With early key parsing, invalid keys fail before any connection attempts
	// so Attempts == 0 is expected
}

func TestVerifyOnce_InvalidPrivateKey(t *testing.T) {
	v := NewVerifier(
		WithConnectTimeout(100*time.Millisecond),
	)

	ctx := context.Background()

	err := v.VerifyOnce(ctx, "localhost", 22, "root", "not-a-valid-key")
	if err == nil {
		t.Error("expected error for invalid key")
	}
}
