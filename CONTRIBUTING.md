# Contributing to Cloud GPU Shopper

Thank you for your interest in contributing to Cloud GPU Shopper! This guide will help you get started with development and understand our workflow.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Development Setup](#development-setup)
- [IDE Recommendations](#ide-recommendations)
- [Useful Development Commands](#useful-development-commands)

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
