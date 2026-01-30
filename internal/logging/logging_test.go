package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetup_JSONFormat(t *testing.T) {
	var buf bytes.Buffer

	logger := Setup(Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	logger.Info("test message", "key", "value")

	// Parse JSON output
	var logEntry map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "test message", logEntry["msg"])
	assert.Equal(t, "value", logEntry["key"])
	assert.Equal(t, "INFO", logEntry["level"])
}

func TestSetup_TextFormat(t *testing.T) {
	var buf bytes.Buffer

	logger := Setup(Config{
		Level:  "info",
		Format: "text",
		Output: &buf,
	})

	logger.Info("test message", "key", "value")

	output := buf.String()
	assert.Contains(t, output, "test message")
	assert.Contains(t, output, "key=value")
}

func TestSetup_LogLevels(t *testing.T) {
	tests := []struct {
		level     string
		shouldLog bool
		logFunc   func(ctx context.Context, msg string, args ...any)
	}{
		{"debug", true, Debug},
		{"info", true, Info},
		{"warn", true, Warn},
		{"error", true, Error},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			var buf bytes.Buffer
			Setup(Config{
				Level:  tt.level,
				Format: "json",
				Output: &buf,
			})

			tt.logFunc(context.Background(), "test")

			if tt.shouldLog {
				assert.NotEmpty(t, buf.String())
			}
		})
	}
}

func TestWithRequestID(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")

	requestID, ok := ctx.Value(RequestIDKey).(string)
	assert.True(t, ok)
	assert.Equal(t, "req-123", requestID)
}

func TestWithSessionID(t *testing.T) {
	ctx := context.Background()
	ctx = WithSessionID(ctx, "sess-456")

	sessionID, ok := ctx.Value(SessionIDKey).(string)
	assert.True(t, ok)
	assert.Equal(t, "sess-456", sessionID)
}

func TestWithConsumerID(t *testing.T) {
	ctx := context.Background()
	ctx = WithConsumerID(ctx, "consumer-789")

	consumerID, ok := ctx.Value(ConsumerIDKey).(string)
	assert.True(t, ok)
	assert.Equal(t, "consumer-789", consumerID)
}

func TestLogger_WithContext(t *testing.T) {
	var buf bytes.Buffer
	Setup(Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")
	ctx = WithSessionID(ctx, "sess-456")

	logger := Logger(ctx)
	logger.Info("test with context")

	output := buf.String()
	assert.Contains(t, output, "req-123")
	assert.Contains(t, output, "sess-456")
}

func TestAudit(t *testing.T) {
	var buf bytes.Buffer
	Setup(Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	ctx := context.Background()
	ctx = WithSessionID(ctx, "sess-123")

	Audit(ctx, "provision_started", "provider", "vastai", "offer_id", "12345")

	output := buf.String()
	assert.Contains(t, output, "AUDIT")
	assert.Contains(t, output, "provision_started")
	assert.Contains(t, output, "vastai")
	assert.Contains(t, output, "sess-123")
}

func TestContextHandler_AddsContextValues(t *testing.T) {
	var buf bytes.Buffer
	Setup(Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	ctx := context.Background()
	ctx = WithRequestID(ctx, "test-request-id")

	Info(ctx, "test message")

	// The context values should be in the output
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 1)

	var logEntry map[string]interface{}
	err := json.Unmarshal([]byte(lines[0]), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "test message", logEntry["msg"])
	assert.Equal(t, "test-request-id", logEntry["request_id"])
}
