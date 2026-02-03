---
type: reference
title: API Documentation
created: 2026-02-02
tags:
  - api
  - reference
  - endpoints
related:
  - "[[CONFIGURATION]]"
  - "[[WORKFLOWS]]"
  - "[[TROUBLESHOOTING]]"
---

# Cloud GPU Shopper API Documentation

Base URL: `http://localhost:8080`

## Authentication

Currently, the API does not require authentication. Agent endpoints use session-specific tokens passed in the request body.

For configuration details, see [[CONFIGURATION]]. For usage examples, see [[WORKFLOWS]].

---

## Health & Metrics

### GET /health

Health check endpoint. Returns 503 during startup sweep.

**Response** (200 OK)
```json
{
  "status": "ok",
  "timestamp": "2026-01-29T12:00:00Z",
  "services": {
    "lifecycle": "running",
    "inventory": "ok",
    "ready": "true"
  }
}
```

**Response** (503 Service Unavailable - during startup)
```json
{
  "status": "unavailable",
  "timestamp": "2026-01-29T12:00:00Z",
  "services": {
    "lifecycle": "stopped",
    "inventory": "ok",
    "ready": "false"
  }
}
```

### GET /ready

Simple readiness check endpoint.

**Response** (200 OK)
```json
{
  "ready": true,
  "timestamp": "2026-01-29T12:00:00Z"
}
```

**Response** (503 Service Unavailable)
```json
{
  "ready": false,
  "timestamp": "2026-01-29T12:00:00Z"
}
```

### GET /metrics

Prometheus metrics endpoint. Returns metrics in Prometheus text format.

Key metrics:
- `gpu_sessions_active{provider,status}` - Active session count
- `gpu_orphans_detected_total` - Orphaned instances detected
- `gpu_destroy_failures_total` - Failed destruction attempts
- `gpu_ssh_verify_duration_seconds` - SSH verification duration
- `gpu_ssh_verify_failures_total` - SSH verification failures
- `gpu_provider_api_errors_total{provider,operation}` - Provider API errors

---

## Inventory

### GET /api/v1/inventory

List available GPU offers from all providers.

**Query Parameters**
| Parameter | Type | Description |
|-----------|------|-------------|
| provider | string | Filter by provider ("vastai", "tensordock") |
| gpu_type | string | Filter by GPU type (e.g., "RTX 4090", "A100") |
| location | string | Filter by location |
| min_vram | int | Minimum VRAM in GB |
| max_price | float | Maximum price per hour in USD |
| min_gpu_count | int | Minimum number of GPUs |
| gpu_count | int | Alias for min_gpu_count |
| min_reliability | float | Minimum reliability score (0-1) |
| min_availability_confidence | float | Minimum availability confidence (0-1) |
| limit | int | Maximum number of results (must be positive) |
| offset | int | Number of results to skip (for pagination) |

**Response**
```json
{
  "offers": [
    {
      "id": "vastai-12345",
      "provider": "vastai",
      "provider_id": "12345",
      "gpu_type": "RTX 4090",
      "gpu_count": 1,
      "vram_gb": 24,
      "price_per_hour": 0.45,
      "location": "US-West",
      "reliability": 0.98,
      "available": true,
      "max_duration_hours": 0,
      "fetched_at": "2026-01-29T12:00:00Z"
    }
  ],
  "count": 1,
  "total": 150
}
```

### GET /api/v1/inventory/:id

Get a specific offer by ID.

**Response**
```json
{
  "id": "vastai-12345",
  "provider": "vastai",
  "provider_id": "12345",
  "gpu_type": "RTX 4090",
  "gpu_count": 1,
  "vram_gb": 24,
  "price_per_hour": 0.45,
  "location": "US-West",
  "reliability": 0.98,
  "available": true,
  "max_duration_hours": 0,
  "fetched_at": "2026-01-29T12:00:00Z"
}
```

**Errors**
- `404 Not Found` - Offer not found

---

## Sessions

### POST /api/v1/sessions

Create a new GPU session.

**Request Body**
```json
{
  "consumer_id": "my-application",
  "offer_id": "vastai-12345",
  "workload_type": "llm",
  "reservation_hours": 2,
  "idle_threshold_minutes": 30,
  "storage_policy": "destroy"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| consumer_id | string | Yes | Identifier for the consumer/application |
| offer_id | string | Yes | ID of the GPU offer to provision |
| workload_type | string | Yes | "llm", "training", or "batch" |
| reservation_hours | int | Yes | Duration in hours (1-12) |
| idle_threshold_minutes | int | No | Auto-shutdown after idle time (0 = disabled) |
| storage_policy | string | No | "preserve" or "destroy" (default: "destroy") |
| launch_mode | string | No | "ssh" or "entrypoint" (default: "ssh") |
| docker_image | string | No | Custom Docker image (for entrypoint mode) |
| model_id | string | No | HuggingFace model ID (for vLLM/TGI workloads) |
| exposed_ports | array | No | Ports to expose (e.g., [8000]) |
| quantization | string | No | Quantization method (e.g., "awq", "gptq") |

**Response** (201 Created)
```json
{
  "session": {
    "id": "sess-abc123",
    "consumer_id": "my-application",
    "provider": "vastai",
    "gpu_type": "RTX 4090",
    "gpu_count": 1,
    "status": "provisioning",
    "ssh_host": "192.168.1.100",
    "ssh_port": 22,
    "ssh_user": "root",
    "workload_type": "llm",
    "reservation_hours": 2,
    "price_per_hour": 0.45,
    "created_at": "2026-01-29T12:00:00Z",
    "expires_at": "2026-01-29T14:00:00Z"
  },
  "ssh_private_key": "-----BEGIN RSA PRIVATE KEY-----\n..."
}
```

**Note**: `ssh_private_key` is only returned once at creation. Poll the session status until it transitions to "running" (SSH verification complete) before connecting.

### GET /api/v1/sessions

List sessions.

**Query Parameters**
| Parameter | Type | Description |
|-----------|------|-------------|
| consumer_id | string | Filter by consumer |
| status | string | Filter by status |
| provider | string | Filter by provider ("vastai", "tensordock") |
| limit | int | Maximum results |

**Response**
```json
{
  "sessions": [...],
  "count": 5
}
```

### GET /api/v1/sessions/:id

Get session details.

**Response**
```json
{
  "id": "sess-abc123",
  "consumer_id": "my-application",
  "provider": "vastai",
  "gpu_type": "RTX 4090",
  "gpu_count": 1,
  "status": "running",
  "ssh_host": "192.168.1.100",
  "ssh_port": 22,
  "ssh_user": "root",
  "workload_type": "llm",
  "reservation_hours": 2,
  "price_per_hour": 0.45,
  "created_at": "2026-01-29T12:00:00Z",
  "expires_at": "2026-01-29T14:00:00Z"
}
```

**Session Status Values**
| Status | Description |
|--------|-------------|
| pending | Session created, not yet provisioned |
| provisioning | Provider instance being created, awaiting SSH verification |
| running | Instance running and SSH verified |
| stopping | Destruction in progress |
| stopped | Successfully terminated |
| failed | Failed to provision or crashed |

### POST /api/v1/sessions/:id/done

Signal that work is complete and session can be terminated.

**Response**
```json
{
  "message": "session shutdown initiated",
  "session_id": "sess-abc123"
}
```

### POST /api/v1/sessions/:id/extend

Extend a session's reservation time.

**Request Body**
```json
{
  "additional_hours": 2
}
```

**Response**
```json
{
  "message": "session extended",
  "session_id": "sess-abc123",
  "new_expires_at": "2026-01-29T16:00:00Z"
}
```

### DELETE /api/v1/sessions/:id

Force destroy a session immediately.

**Response**
```json
{
  "message": "session destroyed",
  "session_id": "sess-abc123"
}
```

**Errors**
- `404 Not Found` - Session not found

### GET /api/v1/sessions/:id/diagnostics

Get diagnostic information for a running session.

**Response** (200 OK)
```json
{
  "session_id": "sess-abc123",
  "status": "running",
  "provider": "vastai",
  "gpu_type": "RTX 4090",
  "gpu_count": 1,
  "ssh_host": "192.168.1.100",
  "ssh_port": 22,
  "ssh_user": "root",
  "launch_mode": "ssh",
  "api_endpoint": "",
  "created_at": "2026-01-29T12:00:00Z",
  "expires_at": "2026-01-29T14:00:00Z",
  "uptime": "1h 30m",
  "time_to_expiry": "30m",
  "ssh_available": true,
  "note": "Full SSH diagnostics require client-side SSH access."
}
```

**Errors**
- `400 Bad Request` - Session is not running
- `404 Not Found` - Session not found

---

## Costs

### GET /api/v1/costs

Get cost information.

**Query Parameters**
| Parameter | Type | Description |
|-----------|------|-------------|
| consumer_id | string | Filter by consumer |
| session_id | string | Get cost for specific session |
| start_date | string | Start date (YYYY-MM-DD) |
| end_date | string | End date (YYYY-MM-DD) |
| period | string | "daily" or "monthly" |

**Response (session_id provided)**
```json
{
  "session_id": "sess-abc123",
  "total_cost": 1.35,
  "currency": "USD"
}
```

**Response (summary)**
```json
{
  "consumer_id": "my-application",
  "total_cost": 45.67,
  "session_count": 12,
  "hours_used": 98.5,
  "by_provider": {
    "vastai": 30.00,
    "tensordock": 15.67
  },
  "by_gpu_type": {
    "RTX 4090": 25.00,
    "A100": 20.67
  }
}
```

### GET /api/v1/costs/summary

Get monthly cost summary.

**Query Parameters**
| Parameter | Type | Description |
|-----------|------|-------------|
| consumer_id | string | Filter by consumer (optional) |

**Response**
```json
{
  "consumer_id": "",
  "total_cost": 450.00,
  "session_count": 89,
  "hours_used": 1024.5,
  "by_provider": {
    "vastai": 300.00,
    "tensordock": 150.00
  },
  "by_gpu_type": {
    "RTX 4090": 200.00,
    "A100": 250.00
  }
}
```

---

## Error Responses

All errors follow this format:

```json
{
  "error": "error message description",
  "request_id": "uuid-of-request"
}
```

Common HTTP status codes:
- `400 Bad Request` - Invalid request body or parameters
- `401 Unauthorized` - Invalid authentication
- `404 Not Found` - Resource not found
- `409 Conflict` - Operation conflicts with current state (e.g., extending a stopped session)
- `500 Internal Server Error` - Server error
- `503 Service Unavailable` - Server not ready or offer no longer available (stale inventory)

---

## Stale Inventory Errors

When provisioning fails due to stale inventory data (offer no longer available), the API returns a structured error:

**Response** (503 Service Unavailable)
```json
{
  "error": "offer unavailable due to stale inventory",
  "error_type": "stale_inventory",
  "offer_id": "vastai-12345",
  "provider": "vastai",
  "retry_suggested": true,
  "message": "The selected offer is no longer available. Please refresh inventory and select a different offer.",
  "request_id": "uuid-of-request"
}
```

When you receive this error:
1. Refresh the inventory list
2. Select a different offer
3. Retry the provision request

---

## Related Documentation

- [[CONFIGURATION]] - Environment variables and configuration options
- [[WORKFLOWS]] - Common usage patterns and examples
- [[PROVIDERS]] - Provider-specific information
- [[TROUBLESHOOTING]] - Common issues and solutions
