---
type: reference
title: Configuration Reference
created: 2026-02-02
tags:
  - configuration
  - deployment
  - environment-variables
related:
  - "[[API]]"
  - "[[WORKFLOWS]]"
  - "[[PROVIDERS]]"
---

# Configuration Reference

This document provides a complete reference for all configuration options in Cloud GPU Shopper.

## Overview

Cloud GPU Shopper can be configured through:
1. **Environment variables** - Recommended for production deployments
2. **Configuration file** - YAML format, useful for complex setups
3. **`.env` file** - Convenient for local development

Environment variables take precedence over configuration file values.

---

## Environment Variables

### Provider Credentials

| Variable | Required | Description |
|----------|----------|-------------|
| `VASTAI_API_KEY` | Yes* | Vast.ai API key for authentication |
| `TENSORDOCK_AUTH_ID` | Yes* | TensorDock authorization ID |
| `TENSORDOCK_API_TOKEN` | Yes* | TensorDock API token |

*At least one provider must be configured with valid credentials.

#### Obtaining API Keys

**Vast.ai:**
1. Create an account at [vast.ai](https://vast.ai/)
2. Navigate to **Account** → **API Keys**
3. Generate a new API key
4. Copy the hexadecimal key string

**TensorDock:**
1. Create an account at [tensordock.com](https://tensordock.com/)
2. Go to **Dashboard** → **API** → **Credentials**
3. Create new API credentials
4. Note both the **Authorization ID** (`TENSORDOCK_AUTH_ID`) and **API Token** (`TENSORDOCK_API_TOKEN`)

### Server Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_HOST` | `0.0.0.0` | Host address to bind to |
| `SERVER_PORT` | `8080` | Port for the HTTP API server |

### Database Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_PATH` | `./data/gpu-shopper.db` | Path to SQLite database file |

The directory will be created automatically if it doesn't exist.

### Logging Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | Log format: `json` or `text` |

### Lifecycle Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `DEPLOYMENT_ID` | (auto-generated) | Unique identifier for this deployment, used for instance tagging and orphan detection |

### Provider-Specific Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TENSORDOCK_DEFAULT_IMAGE` | `ubuntu2404` | Default OS image for TensorDock instances |

---

## Example Configuration

### `.env` File (Local Development)

```bash
# Provider Credentials
# At least one provider must be configured
VASTAI_API_KEY=your_vastai_api_key_here
TENSORDOCK_AUTH_ID=your_tensordock_auth_id_here
TENSORDOCK_API_TOKEN=your_tensordock_api_token_here

# Server Configuration
SERVER_HOST=127.0.0.1
SERVER_PORT=8080

# Database
DATABASE_PATH=./data/gpu-shopper.db

# Logging (optional - defaults shown)
LOG_LEVEL=info
LOG_FORMAT=text

# Deployment ID (optional - auto-generated if not set)
# DEPLOYMENT_ID=my-deployment-001
```

### Docker Deployment

```bash
docker run -d \
  -p 8080:8080 \
  -e VASTAI_API_KEY="your_key" \
  -e TENSORDOCK_AUTH_ID="your_auth_id" \
  -e TENSORDOCK_API_TOKEN="your_token" \
  -e DATABASE_PATH="/data/gpu-shopper.db" \
  -e LOG_LEVEL="info" \
  -v /path/to/data:/data \
  cloud-gpu-shopper:latest
```

### Docker Compose

```yaml
version: '3.8'
services:
  gpu-shopper:
    image: cloud-gpu-shopper:latest
    ports:
      - "8080:8080"
    environment:
      - VASTAI_API_KEY=${VASTAI_API_KEY}
      - TENSORDOCK_AUTH_ID=${TENSORDOCK_AUTH_ID}
      - TENSORDOCK_API_TOKEN=${TENSORDOCK_API_TOKEN}
      - DATABASE_PATH=/data/gpu-shopper.db
      - LOG_LEVEL=info
      - LOG_FORMAT=json
    volumes:
      - gpu-shopper-data:/data
    restart: unless-stopped

volumes:
  gpu-shopper-data:
```

---

## Security Best Practices

### API Key Management

1. **Never commit API keys to version control**
   - Use `.env` files (add to `.gitignore`)
   - Use environment variables in CI/CD
   - Use secrets management (Vault, AWS Secrets Manager, etc.)

2. **Use least-privilege keys where possible**
   - Vast.ai supports creating restricted API keys with limited permissions

3. **Rotate keys periodically**
   - Regenerate API keys every 90 days or after personnel changes

4. **Secure the database**
   - The SQLite database may contain session metadata
   - Restrict file permissions: `chmod 600 gpu-shopper.db`
   - Consider encrypted storage for sensitive deployments

### Network Security

1. **Bind to localhost for local development**
   ```bash
   SERVER_HOST=127.0.0.1
   ```

2. **Use a reverse proxy in production**
   - Nginx, Caddy, or cloud load balancers
   - Enable TLS/HTTPS

3. **Consider firewall rules**
   - Restrict access to the API port (8080)
   - Allow only trusted IP ranges

---

## Configuration File (Alternative)

For complex deployments, you can use a YAML configuration file:

```yaml
# config.yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  path: "./data/gpu-shopper.db"

providers:
  vastai:
    api_key: ""  # Set via VASTAI_API_KEY env var
    enabled: true
  tensordock:
    auth_id: ""  # Set via TENSORDOCK_AUTH_ID env var
    api_token: ""  # Set via TENSORDOCK_API_TOKEN env var
    enabled: true
    default_image: "ubuntu2404"

inventory:
  default_cache_ttl: "1m"
  backoff_cache_ttl: "5m"

lifecycle:
  check_interval: "1m"
  hard_max_hours: 12
  orphan_grace_period: "15m"
  reconciliation_interval: "5m"
  startup_sweep_enabled: true
  startup_sweep_timeout: "2m"
  shutdown_timeout: "60s"
  deployment_id: ""

ssh:
  verify_timeout: "5m"
  check_interval: "15s"

logging:
  level: "info"
  format: "json"
```

Run with config file:
```bash
go run cmd/server/main.go --config config.yaml
```

---

## Default Values Reference

| Setting | Default | Description |
|---------|---------|-------------|
| `server.host` | `0.0.0.0` | Bind to all interfaces |
| `server.port` | `8080` | HTTP API port |
| `database.path` | `./data/gpu-shopper.db` | SQLite file location |
| `providers.vastai.enabled` | `true` | Enable Vast.ai provider |
| `providers.tensordock.enabled` | `true` | Enable TensorDock provider |
| `providers.tensordock.default_image` | `ubuntu2404` | Default TensorDock OS image |
| `inventory.default_cache_ttl` | `1m` | Normal inventory cache duration |
| `inventory.backoff_cache_ttl` | `5m` | Cache duration during rate limiting |
| `lifecycle.check_interval` | `1m` | Session lifecycle check frequency |
| `lifecycle.hard_max_hours` | `12` | Maximum session duration (hours) |
| `lifecycle.orphan_grace_period` | `15m` | Grace period before orphan cleanup |
| `lifecycle.reconciliation_interval` | `5m` | Provider reconciliation frequency |
| `lifecycle.startup_sweep_enabled` | `true` | Clean orphans on startup |
| `lifecycle.startup_sweep_timeout` | `2m` | Timeout for startup sweep |
| `lifecycle.shutdown_timeout` | `60s` | Graceful shutdown timeout |
| `ssh.verify_timeout` | `5m` | SSH verification timeout |
| `ssh.check_interval` | `15s` | SSH verification poll interval |
| `logging.level` | `info` | Log verbosity |
| `logging.format` | `json` | Log output format |

---

## Troubleshooting Configuration

### Common Issues

**"at least one provider must be enabled"**
- Ensure you have set API credentials for at least one provider
- Check that the environment variables are exported correctly

**"VASTAI_API_KEY is required when Vast.ai is enabled"**
- Either set `VASTAI_API_KEY` or disable Vast.ai in the config

**"TENSORDOCK_AUTH_ID is required when TensorDock is enabled"**
- TensorDock requires both `TENSORDOCK_AUTH_ID` and `TENSORDOCK_API_TOKEN`

**Database permission errors**
- Ensure the database directory exists and is writable
- Check file permissions on existing database files

For more troubleshooting help, see [[TROUBLESHOOTING]].
