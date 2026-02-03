---
type: reference
title: Troubleshooting Guide
created: 2026-02-02
tags:
  - troubleshooting
  - debugging
  - support
related:
  - "[[API]]"
  - "[[CONFIGURATION]]"
  - "[[PROVIDERS]]"
---

# Troubleshooting Guide

This guide covers common issues and their solutions when using Cloud GPU Shopper.

---

## Connection Issues

### Server Won't Start

**Symptom**: Server fails to start or exits immediately.

**Check Port Availability**:
```bash
# Check if port 8080 is in use
lsof -i :8080

# Kill the process if needed
kill <PID>
```

**Solution**: Change the port via environment variable:
```bash
SERVER_PORT=8081 go run cmd/server/main.go
```

**Check Database Path**:
```bash
# Ensure directory exists and is writable
mkdir -p ./data
chmod 755 ./data
```

**Common Errors**:
- `address already in use` → Port conflict, change SERVER_PORT
- `failed to open database` → Check DATABASE_PATH and permissions
- `at least one provider must be enabled` → Set API credentials

### Can't Connect to API

**Symptom**: `curl` or client can't reach the API.

**Check Server is Running**:
```bash
curl http://localhost:8080/health
```

**Check Firewall**:
```bash
# macOS
sudo pfctl -s rules

# Linux
sudo iptables -L
```

**Check Binding Address**:
- If `SERVER_HOST=127.0.0.1`, API only accessible locally
- Use `SERVER_HOST=0.0.0.0` for external access

**Verify URL**:
- Ensure using correct port (default: 8080)
- Check for typos in endpoint paths
- API endpoints start with `/api/v1/`

---

## Provider Issues

### API Key Authentication Failures

**Vast.ai: "Provider authentication failed"**

1. **Verify Key Format**: Should be a 64-character hexadecimal string
2. **Check Environment Variable**:
   ```bash
   echo $VASTAI_API_KEY | wc -c  # Should be 65 (64 + newline)
   ```
3. **Test Key Directly**:
   ```bash
   curl -H "Authorization: Bearer $VASTAI_API_KEY" \
     "https://console.vast.ai/api/v0/bundles/?q=%7B%7D"
   ```
4. **Regenerate Key**: If key was compromised or is old, generate a new one

**TensorDock: "Provider authentication failed"**

1. **Verify Both Credentials**:
   ```bash
   echo "Auth ID: $TENSORDOCK_AUTH_ID"
   echo "Token: $TENSORDOCK_API_TOKEN"
   ```
2. **Test Credentials Directly**:
   ```bash
   curl "https://dashboard.tensordock.com/api/v2/locations?api_key=$TENSORDOCK_AUTH_ID&api_token=$TENSORDOCK_API_TOKEN"
   ```
3. **Check Credential Permissions**: Ensure API access is enabled in dashboard

### "No Offers Available" Troubleshooting

**Check Provider Status**:
- Vast.ai: [status.vast.ai](https://status.vast.ai)
- TensorDock: Contact support

**Relax Filters**:
```bash
# Try without filters first
curl http://localhost:8080/api/v1/inventory

# Then add filters incrementally
curl "http://localhost:8080/api/v1/inventory?min_vram=16"
```

**Check Specific GPU Availability**:
- Some GPU types (H100, A100 80GB) are frequently sold out
- RTX 4090 is usually more available
- Try different providers

**Verify Provider is Enabled**:
```bash
# Check logs for provider initialization
LOG_LEVEL=debug go run cmd/server/main.go
```

### SSH Verification Timeouts

**Symptom**: Session stuck in "provisioning" status.

**Causes and Solutions**:

1. **Normal Delay**: SSH verification takes 1-5 minutes. Wait and poll status.

2. **Image Pull**: Large Docker images take longer:
   - Check provider dashboard for image pull progress
   - Use smaller base images when possible

3. **Provider Issue**: Instance may have failed to start:
   - Check provider dashboard directly
   - Look for error messages in session details

4. **SSH Key Issue** (Vast.ai specific):
   - Keys take 10-15 seconds to propagate
   - Cloud GPU Shopper retries automatically

**Debugging**:
```bash
# Get detailed session status
curl http://localhost:8080/api/v1/sessions/<session_id>

# Check server logs
LOG_LEVEL=debug go run cmd/server/main.go 2>&1 | grep -i ssh
```

---

## Session Issues

### Session Stuck in "Provisioning" State

**Wait for Completion**: Provisioning typically takes 1-5 minutes.

**Check Session Details**:
```bash
curl http://localhost:8080/api/v1/sessions/<session_id>
```

**If Stuck > 10 Minutes**:

1. **Check Provider Dashboard**: Instance may have failed
2. **Force Destroy and Retry**:
   ```bash
   curl -X DELETE http://localhost:8080/api/v1/sessions/<session_id>
   ```
3. **Try Different Offer**: The specific host may have issues

### Session Terminated Unexpectedly

**Check Session Status**:
```bash
curl http://localhost:8080/api/v1/sessions/<session_id>
```

**Common Causes**:

1. **Expiration**: Session reached `expires_at` time
   - Solution: Use `/extend` endpoint before expiration

2. **Hard Maximum**: 12-hour limit reached
   - Solution: Plan workloads within 12 hours or checkpoint

3. **Orphan Detection**: Session flagged as orphan
   - Usually means session wasn't properly tracked
   - Check `DEPLOYMENT_ID` consistency

4. **Provider Termination**:
   - Vast.ai interruptible instances can be preempted
   - Host went offline
   - Account billing issue

5. **Idle Shutdown**: `idle_threshold_minutes` triggered
   - Increase threshold or disable (set to 0)

### SSH Connection Refused After Provisioning

**Verify Session Status is "running"**:
```bash
curl http://localhost:8080/api/v1/sessions/<session_id> | jq '.status'
```
SSH is only ready when status is `running`, not `provisioning`.

**Check SSH Details**:
```bash
curl http://localhost:8080/api/v1/sessions/<session_id> | jq '.ssh_host, .ssh_port, .ssh_user'
```

**Test Connection**:
```bash
ssh -v -i /path/to/key -p <port> <user>@<host>
```

**Common Issues**:

1. **Wrong Port**: Use the port from session details, not 22
2. **Wrong User**: Usually `root` for Vast.ai, varies for TensorDock
3. **Key Permissions**: `chmod 600 /path/to/key`
4. **Key Not Saved**: SSH key only returned once at creation

**Vast.ai Specific**:
- Port is randomized (e.g., 20544, not 22)
- Host is often `ssh.vast.ai` (proxy)
- Allow 10-15 seconds for key propagation

---

## Cost Tracking Issues

### Missing Cost Data

**Check Session Exists**:
```bash
curl http://localhost:8080/api/v1/sessions/<session_id>
```

**Verify Consumer ID**:
```bash
curl "http://localhost:8080/api/v1/costs?consumer_id=<your_consumer_id>"
```

**Check Date Range**:
```bash
curl "http://localhost:8080/api/v1/costs?start_date=2026-01-01&end_date=2026-12-31"
```

**Reasons for Missing Data**:
- Session is still running (costs finalized on completion)
- Consumer ID typo
- Session was from a different deployment

### Incorrect Cost Calculations

**Verify Session Details**:
```bash
curl http://localhost:8080/api/v1/sessions/<session_id>
```

Check:
- `price_per_hour`: Actual price charged
- `created_at` and completion time: Duration basis

**Note**: Costs are calculated based on:
- Actual provider price (may differ from offer price)
- Actual runtime (not reservation time)

---

## Debugging Tips

### Enable Verbose Logging

```bash
LOG_LEVEL=debug LOG_FORMAT=text go run cmd/server/main.go
```

Log levels:
- `debug`: All messages, very verbose
- `info`: Normal operation (default)
- `warn`: Warnings and errors
- `error`: Errors only

### Check Health Endpoint

```bash
curl http://localhost:8080/health | jq
```

Response indicates:
- Overall status
- Lifecycle manager status
- Inventory service status

### Check Metrics Endpoint

```bash
curl http://localhost:8080/metrics
```

Key metrics:
- `gpu_sessions_active`: Current active sessions
- `gpu_orphans_detected_total`: Orphan instances found
- `gpu_destroy_failures_total`: Failed destroy operations
- `gpu_ssh_verify_failures_total`: SSH verification failures
- `gpu_provider_api_errors_total`: Provider API errors

### Inspect Database

```bash
sqlite3 ./data/gpu-shopper.db

# List tables
.tables

# Check sessions
SELECT * FROM sessions ORDER BY created_at DESC LIMIT 10;

# Check recent errors
SELECT * FROM sessions WHERE status = 'failed' ORDER BY created_at DESC;
```

### Test Provider Directly

**Vast.ai**:
```bash
# List offers
curl -H "Authorization: Bearer $VASTAI_API_KEY" \
  "https://console.vast.ai/api/v0/bundles/?q=%7B%22rentable%22%3Atrue%7D"

# List your instances
curl -H "Authorization: Bearer $VASTAI_API_KEY" \
  "https://console.vast.ai/api/v0/instances/"
```

**TensorDock**:
```bash
# List locations/offers
curl "https://dashboard.tensordock.com/api/v2/locations?api_key=$TENSORDOCK_AUTH_ID&api_token=$TENSORDOCK_API_TOKEN"
```

---

## Getting Help

### Information to Gather

When reporting issues, include:

1. **Error Message**: Exact error text
2. **Session ID**: If applicable
3. **Provider**: Which provider (vastai/tensordock)
4. **Logs**: Relevant log output (sanitize API keys)
5. **Steps to Reproduce**: What you did before the error

### Log Sanitization

Before sharing logs, remove sensitive data:
```bash
# Remove API keys from logs
sed 's/VASTAI_API_KEY=[^ ]*/VASTAI_API_KEY=REDACTED/g' logs.txt
sed 's/Authorization: Bearer [^ ]*/Authorization: Bearer REDACTED/g' logs.txt
```

### Resources

- [[CONFIGURATION]] - Configuration reference
- [[WORKFLOWS]] - Usage patterns
- [[PROVIDERS]] - Provider-specific information
- [[API]] - API documentation
