# Blue Lobster Provider Adapter Design

**Date**: 2026-02-23
**Branch**: `feat/provider-bluelobster`
**Status**: Approved

## Overview

Add Blue Lobster Cloud as a third GPU provider in cloud-gpu-shopper. Blue Lobster offers fixed-price dedicated GPU instances via a REST API with async task-based provisioning. The adapter implements the existing `provider.Provider` interface following the same patterns as Vast.ai and TensorDock.

## Blue Lobster API Summary

- **Base URL**: `https://api.bluelobster.ai/api/v1`
- **Auth**: `X-API-Key` header
- **Regions**: `igl` (Wilmington, DE — production), `phl` (Philadelphia, PA — development)
- **Instance types**: 19 types (2 CPU-only, 17 GPU). GPUs: RTX 2080 Ti, A4000, A5000, A6000, Quadro RTX 8000, RTX 5090
- **Templates**: 10 OS templates. GPU default: `UBUNTU-22-04-NV` (Ubuntu 22.04 + NVIDIA drivers + Docker)
- **Async model**: Launch/delete return `task_id`. Poll `GET /tasks/{id}` for status (PENDING → PROCESSING → COMPLETED | FAILED)
- **Pricing**: Fixed, in cents/hr in the availability response. No marketplace bidding.

## Architecture

### File Structure

```
internal/provider/bluelobster/
├── client.go          # Client struct, NewClient(), Provider interface methods
├── client_test.go     # Unit tests with mock HTTP server
├── types.go           # Blue Lobster API request/response types
```

### Changes to Existing Files

- `internal/config/config.go` — add `BlueLobster` config struct + env var mapping (`BLUELOBSTER_API_KEY`)
- `cmd/server/main.go` — conditional initialization of Blue Lobster client
- `.env` — `BLUELOBSTER_API_KEY` (already added)

## Client Structure

```go
type Client struct {
    apiKey          string
    baseURL         string           // https://api.bluelobster.ai/api/v1
    httpClient      *http.Client
    limiter         *rate.Limiter    // Token bucket (2 req/s, burst 3)
    circuitBreaker  *circuitBreaker
    logger          *slog.Logger
    defaultTemplate string           // UBUNTU-22-04-NV
}
```

**Constructor**: `NewClient(apiKey string, opts ...ClientOption) *Client`

**Functional options**: `WithBaseURL`, `WithHTTPClient`, `WithRateLimit`, `WithCircuitBreaker`, `WithLogger`, `WithDefaultTemplate`

**Provider name**: `"bluelobster"`

## Provider Interface Mapping

| Method | Blue Lobster API | Notes |
|---|---|---|
| `Name()` | — | Returns `"bluelobster"` |
| `ListOffers()` | `GET /instances/available` | Convert each instance type + region combo to `GPUOffer`. Skip CPU-only (`gpus: 0`). Normalize `gpu_model` (string or `[]string` → first value). |
| `CreateInstance()` | `POST /instances/launch-instance` → poll `GET /tasks/{id}` → `GET /instances/{id}` | Ephemeral SSH key, `UBUNTU-22-04-NV` template, `username: "ubuntu"`. Poll task up to 3 min, then return partial info. |
| `DestroyInstance()` | `DELETE /instances/{id}` | Synchronous return. |
| `GetInstanceStatus()` | `GET /instances/{id}` | Map `power_status` to our status model. SSH on port 22, direct IP. |
| `ListAllInstances()` | `GET /instances` | Filter by metadata tags. |
| `SupportsFeature()` | — | Supports `FeatureInstanceTags` only. |

## Key Design Decisions

### Offer ID Format

`bluelobster:{instance_type}:{region}` (e.g., `bluelobster:v1_gpu_1x_a5000:igl`). Parsed back during `CreateInstance` to extract the instance type and region for the launch request.

### CreateInstance Timeout

Poll the task API every 3 seconds for up to 3 minutes. If task completes (COMPLETED), fetch instance details and return full `InstanceInfo`. If still PROCESSING after 3 min, return partial `InstanceInfo` with status `"provisioning"` and any assigned IP. The provisioner's SSH verification loop handles the rest. If task FAILED, return wrapped error.

### SSH Access

- Ephemeral keypair generated per session (consistent with existing security model)
- Public key passed at launch via `ssh_key` field
- Private key returned in session response (shown once only, same as Vast.ai)
- Direct IP access on port 22. No port forwarding needed.
- Username: `"ubuntu"`

### Workload Setup

SSH post-provision, then pull Ollama Docker container since the NV template has Docker + NVIDIA runtime pre-installed. Matches existing benchmark runner pattern of uploading scripts via SCP.

### Tagging

Use Blue Lobster's `metadata` field to store:
- `shopper_deployment_id` — identifies our deployment
- `shopper_session_id` — links to our session
- `shopper_expires_at` — expiration timestamp

Same tags as other providers, used for orphan detection and reconciliation.

### Availability Confidence

Set to 1.0 for all offers. Blue Lobster uses dedicated hardware with no oversubscription and reports real-time capacity.

## Error Handling

| HTTP Status | Maps to |
|---|---|
| 401, 403 | `ErrProviderAuth` |
| 404 | `ErrInstanceNotFound` |
| 409 | `ErrOfferUnavailable` |
| 429 | `ErrProviderRateLimit` |
| 500+ | `ErrProviderError` (retryable) |

Blue Lobster errors return `{"detail": {"error": "...", "message": "..."}}` or `{"error": "...", "message": "..."}` — handle both shapes.

**Circuit breaker**: 5 failures → open, 30s reset, exponential backoff up to 2 min.

**Rate limiter**: Token bucket at 2 req/s, burst 3.

## Testing Strategy

Unit tests with `httptest.NewServer` mock:

- `ListOffers` — GPU-only filtering, offer ID format, price conversion (cents → dollars)
- `CreateInstance` — happy path, timeout path (partial info), task FAILED path
- `DestroyInstance` — success and 404 handling
- `GetInstanceStatus` — all power states
- `ListAllInstances` — metadata tag filtering
- Error handling — auth errors, rate limits, circuit breaker
- `gpu_model` polymorphism — string vs `[]string`

No integration/live tests in initial PR.
