package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// contextKey is a type for context keys
type contextKey string

const (
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request_id"
	// SessionIDKey is the context key for session ID
	SessionIDKey contextKey = "session_id"
	// ConsumerIDKey is the context key for consumer ID
	ConsumerIDKey contextKey = "consumer_id"
)

// Config holds logging configuration
type Config struct {
	Level  string // "debug", "info", "warn", "error"
	Format string // "json" or "text"
	Output io.Writer
}

// Setup configures the global logger
func Setup(cfg Config) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	output := cfg.Output
	if output == nil {
		output = os.Stdout
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug,
	}

	if strings.ToLower(cfg.Format) == "text" {
		handler = slog.NewTextHandler(output, opts)
	} else {
		handler = slog.NewJSONHandler(output, opts)
	}

	// Wrap with context handler
	handler = &ContextHandler{Handler: handler}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger
}

// ContextHandler adds context values to log records
type ContextHandler struct {
	slog.Handler
}

// Handle adds context values to the record before passing to the wrapped handler
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	// Add request ID if present
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok && requestID != "" {
		r.AddAttrs(slog.String("request_id", requestID))
	}

	// Add session ID if present
	if sessionID, ok := ctx.Value(SessionIDKey).(string); ok && sessionID != "" {
		r.AddAttrs(slog.String("session_id", sessionID))
	}

	// Add consumer ID if present
	if consumerID, ok := ctx.Value(ConsumerIDKey).(string); ok && consumerID != "" {
		r.AddAttrs(slog.String("consumer_id", consumerID))
	}

	return h.Handler.Handle(ctx, r)
}

// WithRequestID adds a request ID to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// WithSessionID adds a session ID to the context
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, SessionIDKey, sessionID)
}

// WithConsumerID adds a consumer ID to the context
func WithConsumerID(ctx context.Context, consumerID string) context.Context {
	return context.WithValue(ctx, ConsumerIDKey, consumerID)
}

// Logger returns a logger with additional context
func Logger(ctx context.Context) *slog.Logger {
	logger := slog.Default()

	// Add context values as attributes
	var attrs []any
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok && requestID != "" {
		attrs = append(attrs, "request_id", requestID)
	}
	if sessionID, ok := ctx.Value(SessionIDKey).(string); ok && sessionID != "" {
		attrs = append(attrs, "session_id", sessionID)
	}
	if consumerID, ok := ctx.Value(ConsumerIDKey).(string); ok && consumerID != "" {
		attrs = append(attrs, "consumer_id", consumerID)
	}

	if len(attrs) > 0 {
		return logger.With(attrs...)
	}
	return logger
}

// Audit logs an audit event (always logged regardless of level)
func Audit(ctx context.Context, operation string, attrs ...any) {
	logger := slog.Default()

	// Build base attributes
	baseAttrs := []any{
		"audit", true,
		"operation", operation,
	}

	// Add context values
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok && requestID != "" {
		baseAttrs = append(baseAttrs, "request_id", requestID)
	}
	if sessionID, ok := ctx.Value(SessionIDKey).(string); ok && sessionID != "" {
		baseAttrs = append(baseAttrs, "session_id", sessionID)
	}
	if consumerID, ok := ctx.Value(ConsumerIDKey).(string); ok && consumerID != "" {
		baseAttrs = append(baseAttrs, "consumer_id", consumerID)
	}

	// Append additional attributes
	baseAttrs = append(baseAttrs, attrs...)

	logger.Info("AUDIT", baseAttrs...)
}

// Common log operations with context

// Debug logs a debug message
func Debug(ctx context.Context, msg string, args ...any) {
	Logger(ctx).Debug(msg, args...)
}

// Info logs an info message
func Info(ctx context.Context, msg string, args ...any) {
	Logger(ctx).Info(msg, args...)
}

// Warn logs a warning message
func Warn(ctx context.Context, msg string, args ...any) {
	Logger(ctx).Warn(msg, args...)
}

// Error logs an error message
func Error(ctx context.Context, msg string, args ...any) {
	Logger(ctx).Error(msg, args...)
}
