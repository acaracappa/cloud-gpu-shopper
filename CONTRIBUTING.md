# Contributing to Cloud GPU Shopper

Thank you for your interest in contributing to Cloud GPU Shopper! This guide will help you get started with development and understand our workflow.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Development Setup](#development-setup)
- [IDE Recommendations](#ide-recommendations)
- [Useful Development Commands](#useful-development-commands)
- [Code Style and Formatting](#code-style-and-formatting)

## Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.22+** - [Download Go](https://go.dev/dl/)
- **Git** - [Download Git](https://git-scm.com/downloads)
- **Docker** (optional) - Required for containerized deployment and monitoring stack

Verify your Go installation:

```bash
go version
# Should output: go version go1.22.x (or higher)
```

## Development Setup

### 1. Clone the Repository

```bash
git clone https://github.com/cloud-gpu-shopper/cloud-gpu-shopper.git
cd cloud-gpu-shopper
```

### 2. Configure Environment Variables

Copy the example environment file and configure your API keys:

```bash
cp .env.example .env
```

Edit `.env` with your credentials:

```bash
# TensorDock API Credentials
# Get from: https://dashboard.tensordock.com/api
TENSORDOCK_AUTH_ID=your-auth-id-here
TENSORDOCK_API_TOKEN=your-api-token-here

# Vast.ai API Credentials
# Get from: https://cloud.vast.ai/api/
VASTAI_API_KEY=your-api-key-here

# Database
DATABASE_PATH=./data/gpu-shopper.db

# Server
SERVER_HOST=0.0.0.0
SERVER_PORT=8080
```

**Note:** At least one provider (Vast.ai or TensorDock) must be configured for the service to work.

### 3. Build the Project

Build all binaries:

```bash
go build ./cmd/...
```

Or build specific components:

```bash
# Build the API server
go build -o bin/server ./cmd/server

# Build the CLI tool
go build -o bin/gpu-shopper ./cmd/cli
```

### 4. Run the Server Locally

```bash
# Run directly with Go
go run ./cmd/server

# Or use the built binary
./bin/server
```

The server starts on `http://localhost:8080` by default. You can verify it's running:

```bash
curl http://localhost:8080/health
```

### 5. Run the CLI

```bash
# Run directly with Go
go run ./cmd/cli inventory

# Or use the built binary
./bin/gpu-shopper inventory
```

### 6. Create the Data Directory

The server requires a data directory for SQLite:

```bash
mkdir -p data
```

## IDE Recommendations

### VS Code (Recommended for most users)

Install the official Go extension:
1. Open VS Code
2. Go to Extensions (Ctrl+Shift+X / Cmd+Shift+X)
3. Search for "Go" by the Go Team at Google
4. Install and reload

Recommended settings for `.vscode/settings.json`:

```json
{
  "go.useLanguageServer": true,
  "go.lintOnSave": "package",
  "go.formatTool": "gofmt",
  "editor.formatOnSave": true,
  "[go]": {
    "editor.defaultFormatter": "golang.go"
  }
}
```

### GoLand

JetBrains GoLand provides excellent Go support out of the box:
- Intelligent code completion
- Built-in debugging
- Integrated testing
- Database tools for SQLite inspection

## Useful Development Commands

### Building

```bash
# Build all binaries
go build ./cmd/...

# Build with output directory
go build -o bin/server ./cmd/server
go build -o bin/gpu-shopper ./cmd/cli
```

### Running

```bash
# Run server
go run ./cmd/server

# Run CLI commands
go run ./cmd/cli inventory
go run ./cmd/cli sessions list
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with race detection
go test -race ./...

# Run tests with coverage
go test -cover ./...
```

### Formatting

```bash
# Format all Go files
go fmt ./...
```

### Dependency Management

```bash
# Download dependencies
go mod download

# Tidy dependencies
go mod tidy
```

### Docker (optional)

```bash
# Start server only
cd deploy && docker-compose up -d server

# Start with monitoring stack
cd deploy && docker-compose --profile monitoring up -d

# View logs
cd deploy && docker-compose logs -f server
```

## Testing

This project maintains high test quality standards. All tests should be race-free, deterministic, and properly isolated.

### Running Tests

```bash
# Run the full test suite
go test ./...

# Run tests with race detection (recommended for development)
go test -race ./...

# Run tests with coverage report
go test -cover ./...

# Run tests with detailed coverage output
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out  # View in browser
```

### Running E2E Tests

End-to-end tests use a mock provider and test the full API flow:

```bash
# Run E2E tests (requires the e2e build tag)
go test -tags=e2e ./test/e2e/...

# Run E2E tests with verbose output
go test -tags=e2e -v ./test/e2e/...
```

The E2E tests automatically set up:
- An in-process API server
- A mock provider that simulates Vast.ai behavior
- A temporary SQLite database

### Live Provider Tests

Live tests run against real GPU providers and **incur actual costs**. Use sparingly and only when necessary:

```bash
# Run live tests (requires API keys and the live build tag)
go test -tags=live ./test/live/...
```

**Requirements for live tests:**
- `VASTAI_API_KEY` environment variable for Vast.ai tests
- `TENSORDOCK_AUTH_ID` and `TENSORDOCK_API_TOKEN` for TensorDock tests
- Budget allocation (tests have spending limits built in)

**Warning:** Live tests provision real GPU instances. Always verify cleanup completed successfully.

### Test Quality Standards

All tests in this project should follow these standards:

#### 1. Race-Free Tests

Tests must pass with race detection enabled:

```bash
go test -race ./...
```

- Use proper synchronization (mutexes, channels) for shared state
- Avoid data races in test setup and assertions
- All CI runs include race detection

#### 2. Deterministic Tests with `require.Eventually()`

Never use `time.Sleep()` to wait for asynchronous operations. Instead, use `require.Eventually()` from testify:

```go
// BAD - Non-deterministic, may fail or be slow
time.Sleep(100 * time.Millisecond)
assert.Equal(t, expected, getValue())

// GOOD - Deterministic, polls until condition is met
require.Eventually(t, func() bool {
    return getValue() == expected
}, 5*time.Second, 10*time.Millisecond, "value should equal expected")
```

This approach:
- Makes tests faster (no fixed waits)
- Makes tests more reliable (no flaky timeouts)
- Provides clear failure messages

#### 3. Proper Cleanup with `t.Cleanup()`

Always clean up resources using `t.Cleanup()` to ensure cleanup runs even if tests fail:

```go
func TestSomething(t *testing.T) {
    db, err := openTestDatabase()
    require.NoError(t, err)
    t.Cleanup(func() {
        db.Close()
    })

    server := startTestServer()
    t.Cleanup(func() {
        server.Stop()
    })

    // Test code...
}
```

#### 4. Time-Injectable Services

Services that depend on time should accept a time function for deterministic testing:

```go
// Production code
type Manager struct {
    timeFunc func() time.Time
}

func WithTimeFunc(fn func() time.Time) Option {
    return func(m *Manager) {
        m.timeFunc = fn
    }
}

// Test code - control time precisely
baseTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
currentTime := baseTime

m := NewManager(
    WithTimeFunc(func() time.Time { return currentTime }),
)

// Simulate time progression without waiting
currentTime = baseTime.Add(2 * time.Hour)
m.checkExpiry() // Now sees 2 hours elapsed
```

This pattern is used throughout the codebase (see `internal/service/lifecycle/manager.go` for examples).

### Test Organization

```
├── internal/
│   └── */
│       └── *_test.go      # Unit tests alongside source
├── test/
│   ├── e2e/               # End-to-end API tests
│   ├── live/              # Live provider tests (real instances)
│   └── mockprovider/      # Mock provider for testing
```

## Code Style and Formatting

This section covers coding conventions and style guidelines to maintain consistency across the codebase.

### Formatting

All Go code must be formatted with `gofmt`:

```bash
# Format all files
go fmt ./...

# Check formatting without modifying files
gofmt -l .
```

Configure your IDE to format on save (see [IDE Recommendations](#ide-recommendations)).

### Linting

While no strict linting configuration is enforced, we recommend running basic Go checks:

```bash
# Run the built-in vet tool for common issues
go vet ./...

# Check for shadowed variables (optional but recommended)
go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@latest
go vet -vettool=$(which shadow) ./...
```

If you use `golangci-lint`, a sensible default configuration works well with this project.

### Naming Conventions

Follow existing patterns in the codebase:

#### Packages
- Use short, lowercase, single-word names (e.g., `provider`, `storage`, `lifecycle`)
- Avoid generic names like `util` or `common` - be specific (e.g., `filetransfer` instead of `utils`)

#### Types and Interfaces
- Use PascalCase for exported types: `GPUOffer`, `SessionStatus`, `ProviderFeature`
- Interface names typically describe behavior: `Provider`, not `IProvider`
- Error types end with `Error`: `SessionNotFoundError`, `StaleInventoryError`

#### Functions and Methods
- Use descriptive names: `CreateInstance`, `GetInstanceStatus`, `IsRetryable`
- Boolean functions often start with `Is`, `Has`, `Can`, `Should`: `IsExpired()`, `IsOurs()`, `ShouldRetryWithDifferentOffer()`
- Constructors use `New` prefix: `NewProviderError`, `NewManager`

#### Constants
- Exported constants use PascalCase: `FeatureIdleDetection`, `LaunchModeSSH`
- Group related constants together with a type when appropriate

#### Variables
- Use camelCase for local variables: `sessionID`, `pricePerHour`
- Exported variables use PascalCase: `ErrNotFound`, `DefaultTimeout`

### Error Handling

This project uses structured error handling. Follow these patterns:

#### Sentinel Errors

Define package-level sentinel errors for common cases:

```go
// Package-level errors in errors.go or at the top of the main file
var (
    ErrNotFound      = errors.New("record not found")
    ErrAlreadyExists = errors.New("record already exists")
)
```

#### Custom Error Types

For errors that need context, create structured error types:

```go
// SessionNotFoundError indicates the requested session doesn't exist
type SessionNotFoundError struct {
    ID string
}

func (e *SessionNotFoundError) Error() string {
    return fmt.Sprintf("session not found: %s", e.ID)
}
```

#### Error Wrapping

Use `fmt.Errorf` with `%w` to wrap errors, preserving the chain:

```go
if err := db.Save(session); err != nil {
    return fmt.Errorf("saving session %s: %w", session.ID, err)
}
```

#### Error Checking

Use `errors.Is()` and `errors.As()` for error inspection:

```go
// Check for sentinel errors
if errors.Is(err, ErrNotFound) {
    return nil, http.StatusNotFound
}

// Check for error types
var staleErr *StaleInventoryError
if errors.As(err, &staleErr) {
    // Handle stale inventory specifically
}
```

#### Error Helper Functions

Create `Is*` helper functions for common error checks:

```go
// IsRetryable checks if the error is retryable
func IsRetryable(err error) bool {
    if IsRateLimitError(err) {
        return true
    }
    var pe *ProviderError
    if errors.As(err, &pe) {
        return pe.StatusCode >= 500 && pe.StatusCode < 600
    }
    return false
}
```

### Logging

Use the `internal/logging` package for consistent structured logging:

```go
import "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"

// Use context-aware logging functions
logging.Info(ctx, "session created", "session_id", sessionID, "consumer_id", consumerID)
logging.Error(ctx, "failed to provision", "error", err, "offer_id", offerID)
logging.Debug(ctx, "cache hit", "key", cacheKey)
logging.Warn(ctx, "rate limit approaching", "remaining", remaining)
```

#### Logging Guidelines

1. **Use structured logging**: Pass key-value pairs, not formatted strings
   ```go
   // GOOD
   logging.Info(ctx, "session started", "session_id", s.ID, "duration", duration)

   // AVOID
   logging.Info(ctx, fmt.Sprintf("session %s started for %v", s.ID, duration))
   ```

2. **Include context**: Always pass `ctx` to enable request ID correlation

3. **Log at appropriate levels**:
   - `Debug`: Development-time details, verbose diagnostics
   - `Info`: Normal operations, state changes, key events
   - `Warn`: Degraded but functioning (rate limits, retries)
   - `Error`: Operation failures, unexpected errors

4. **Use audit logging** for security-relevant events:
   ```go
   logging.Audit(ctx, "instance_destroyed", "session_id", sessionID, "provider", provider)
   ```

### Comment Guidelines

#### When to Comment

- **Exported items**: All exported types, functions, constants, and variables need doc comments
- **Complex logic**: Explain non-obvious algorithms or business rules
- **TODOs**: Use `// TODO:` for planned improvements
- **Bug references**: Use `// Bug #123 fix:` when fixing specific issues

#### Comment Style

```go
// GPUOffer represents an available GPU instance from a provider.
// Offers are refreshed periodically and may become unavailable.
type GPUOffer struct {
    ID          string
    Provider    string
    PricePerHour float64
}

// CreateSession provisions a new GPU session for the given consumer.
// It returns the session details and SSH private key for access.
// Returns StaleInventoryError if the offer is no longer available.
func (s *Service) CreateSession(ctx context.Context, req CreateRequest) (*Session, error) {
    // Validate consumer has budget remaining
    if err := s.validateBudget(ctx, req.ConsumerID); err != nil {
        return nil, err
    }

    // TODO: Add support for multiple simultaneous sessions per consumer
    ...
}
```

#### Avoid Unnecessary Comments

```go
// AVOID - comment restates the obvious
// Increment counter by 1
counter++

// GOOD - no comment needed for self-explanatory code
counter++
```

### Code Organization

#### File Structure

- Keep files focused on a single responsibility
- Place interfaces in their own file or at the top of the primary implementation file
- Group related types together
- Put error definitions in `errors.go` files

#### Import Ordering

Organize imports in three groups, separated by blank lines:

```go
import (
    // Standard library
    "context"
    "errors"
    "fmt"

    // Third-party packages
    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/require"

    // Project packages
    "github.com/cloud-gpu-shopper/cloud-gpu-shopper/internal/logging"
    "github.com/cloud-gpu-shopper/cloud-gpu-shopper/pkg/models"
)
```
