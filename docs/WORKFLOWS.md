---
type: reference
title: Common Workflows
created: 2026-02-02
tags:
  - workflows
  - usage
  - guide
related:
  - "[[API]]"
  - "[[CONFIGURATION]]"
  - "[[PROVIDERS]]"
---

# Common Workflows

This guide covers typical usage patterns for Cloud GPU Shopper, from browsing inventory to managing sessions and tracking costs.

---

## Workflow 1: Browsing Available GPU Inventory

Before provisioning a GPU, you'll want to browse what's available across providers.

### List All Available Offers

```bash
# CLI
gpu-shopper inventory

# API
curl http://localhost:8080/api/v1/inventory
```

### Filter by GPU Type

Find specific GPU models:

```bash
# CLI
gpu-shopper inventory --gpu-type "RTX 4090"

# API
curl "http://localhost:8080/api/v1/inventory?gpu_type=RTX%204090"
```

### Filter by VRAM Requirements

For large models, filter by minimum VRAM:

```bash
# CLI
gpu-shopper inventory --min-vram 24

# API
curl "http://localhost:8080/api/v1/inventory?min_vram=24"
```

### Filter by Price

Set a maximum hourly price:

```bash
# CLI
gpu-shopper inventory --max-price 0.50

# API
curl "http://localhost:8080/api/v1/inventory?max_price=0.50"
```

### Filter by Provider

See offers from a specific provider:

```bash
# CLI
gpu-shopper inventory --provider vastai

# API
curl "http://localhost:8080/api/v1/inventory?provider=vastai"
```

### Filter by GPU Count

For multi-GPU workloads:

```bash
# API
curl "http://localhost:8080/api/v1/inventory?min_gpu_count=2"
```

### Combine Filters

```bash
# Find RTX 4090s under $0.50/hr from Vast.ai
curl "http://localhost:8080/api/v1/inventory?gpu_type=RTX%204090&max_price=0.50&provider=vastai"
```

### Understanding Offer Attributes

Each offer includes:

| Attribute | Description |
|-----------|-------------|
| `id` | Unique offer ID (use this to provision) |
| `provider` | "vastai" or "tensordock" |
| `provider_id` | Provider's internal ID |
| `gpu_type` | Normalized GPU model name |
| `gpu_count` | Number of GPUs |
| `vram_gb` | GPU memory in gigabytes |
| `price_per_hour` | Cost in USD per hour |
| `location` | Geographic location |
| `reliability` | Provider's reliability score (0-1) |
| `available` | Whether offer is currently available |
| `fetched_at` | When offer data was last updated |

### Comparing Providers

When comparing offers:

- **Price**: TensorDock often has competitive pricing; Vast.ai has more variety
- **Reliability**: Check the `reliability` score (higher is better)
- **Location**: Consider latency to your region
- **Availability**: Some GPU types are scarce - provision quickly when available

---

## Workflow 2: Provisioning a GPU Session

Once you've selected an offer, provision it for your workload.

### Step 1: Select an Offer

Note the `id` from the inventory listing, e.g., `vastai-12345`.

### Step 2: Create a Session

```bash
# CLI
gpu-shopper provision \
  --offer-id vastai-12345 \
  --consumer-id my-app \
  --workload-type llm \
  --hours 2

# API
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "consumer_id": "my-app",
    "offer_id": "vastai-12345",
    "workload_type": "llm",
    "reservation_hours": 2
  }'
```

### Understanding Workload Types

| Type | Use Case | Behavior |
|------|----------|----------|
| `llm` | LLM inference, chatbots | Optimized for interactive use |
| `training` | Model training | Optimized for long-running jobs |
| `batch` | Batch processing | Optimized for throughput |

### Choosing Reservation Hours

- **Minimum**: 1 hour
- **Maximum**: 12 hours (hard limit, requires CLI override to extend)
- **Recommendation**: Start with what you need; you can extend later

### Optional Parameters

```json
{
  "consumer_id": "my-app",
  "offer_id": "vastai-12345",
  "workload_type": "llm",
  "reservation_hours": 2,
  "idle_threshold_minutes": 30,
  "storage_policy": "destroy",
  "disk_gb": 100
}
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `idle_threshold_minutes` | 0 (disabled) | Auto-shutdown after idle period |
| `storage_policy` | "destroy" | "preserve" to keep data, "destroy" to clean up |
| `disk_gb` | 50 | Disk space in GB. Cannot be changed after creation. |

### Disk Allocation Guidance

When choosing disk size, consider:

- **Default (50GB)**: Sufficient for most workloads and smaller models
- **100-200GB**: Recommended for medium LLMs (7B-30B parameters)
- **200-500GB**: Required for large LLMs (70B+ parameters) like Llama 2 70B or Mixtral
- **500GB+**: For very large models like DeepSeek-V2.5 (236B, ~132GB weights)

**Important**: Disk size is permanent for Vast.ai instances. Plan your storage needs upfront.

### Step 3: Handle the Response

The response includes SSH credentials:

```json
{
  "session": {
    "id": "sess-abc123",
    "status": "provisioning",
    "ssh_host": "192.168.1.100",
    "ssh_port": 22,
    "ssh_user": "root",
    ...
  },
  "ssh_private_key": "-----BEGIN RSA PRIVATE KEY-----\n..."
}
```

**Important**: The SSH private key is returned **only once** at creation. Save it immediately.

### Step 4: Save the SSH Key

```bash
# Save the key
echo "$SSH_PRIVATE_KEY" > /tmp/session-key
chmod 600 /tmp/session-key
```

### Step 5: Wait for Session to be Ready

Poll the session status until it transitions from "provisioning" to "running":

```bash
# CLI
gpu-shopper sessions --id sess-abc123

# API
curl http://localhost:8080/api/v1/sessions/sess-abc123
```

Session states:
- `pending` → `provisioning` → `running` (success path)
- `failed` (if provisioning fails)

### Step 6: Connect via SSH

Once status is "running":

```bash
ssh -i /tmp/session-key -p 22 root@192.168.1.100
```

### Example: Provisioning for Large Models

For large models that require substantial disk space:

```bash
# API - Provisioning with custom disk allocation for a large model
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "consumer_id": "llm-inference",
    "offer_id": "vastai-30733007",
    "workload_type": "ssh",
    "reservation_hours": 4,
    "template_hash_id": "a8a44c7363cbca20056020397e3bf072",
    "disk_gb": 200
  }'
```

This allocates 200GB of disk space, suitable for models like Llama 2 70B or similar large LLMs.

---

## Workflow 3: Managing Active Sessions

### List All Sessions

```bash
# CLI
gpu-shopper sessions

# API
curl http://localhost:8080/api/v1/sessions
```

### Filter by Consumer

```bash
curl "http://localhost:8080/api/v1/sessions?consumer_id=my-app"
```

### Filter by Status

```bash
curl "http://localhost:8080/api/v1/sessions?status=running"
```

### Get Session Details

```bash
curl http://localhost:8080/api/v1/sessions/sess-abc123
```

### Extending Session Time

If you need more time before the session expires:

```bash
# API
curl -X POST http://localhost:8080/api/v1/sessions/sess-abc123/extend \
  -H "Content-Type: application/json" \
  -d '{"additional_hours": 2}'
```

**Note**: Extensions are limited by the 12-hour hard maximum.

### Signaling Completion

When your workload is done, signal completion to cleanly terminate:

```bash
# CLI
gpu-shopper done sess-abc123

# API
curl -X POST http://localhost:8080/api/v1/sessions/sess-abc123/done
```

This initiates a graceful shutdown:
1. Session status changes to "stopping"
2. Provider instance is destroyed
3. Session status changes to "stopped"
4. Cost tracking is finalized

### Force Destroying a Session

If something goes wrong or you need immediate termination:

```bash
# CLI
gpu-shopper shutdown sess-abc123

# API
curl -X DELETE http://localhost:8080/api/v1/sessions/sess-abc123
```

---

## Workflow 4: Cost Tracking and Budgeting

### View Session Costs

Get costs for a specific session:

```bash
curl "http://localhost:8080/api/v1/costs?session_id=sess-abc123"
```

Response:
```json
{
  "session_id": "sess-abc123",
  "total_cost": 1.35,
  "currency": "USD"
}
```

### View Consumer Costs

Aggregate costs by consumer ID:

```bash
curl "http://localhost:8080/api/v1/costs?consumer_id=my-app"
```

Response:
```json
{
  "consumer_id": "my-app",
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

### Monthly Cost Summary

```bash
# All consumers
curl http://localhost:8080/api/v1/costs/summary

# Specific consumer
curl "http://localhost:8080/api/v1/costs/summary?consumer_id=my-app"
```

### Filter by Date Range

```bash
curl "http://localhost:8080/api/v1/costs?start_date=2026-01-01&end_date=2026-01-31"
```

### Cost Breakdown Analysis

Use the breakdown by provider and GPU type to:
- Identify cost-effective providers for your workloads
- Track GPU type preferences
- Plan budgets based on historical usage

---

## Tips and Best Practices

### Provision Quickly

GPU availability is dynamic. If you see a good offer:
1. Provision immediately
2. The offer may be taken by another user within seconds

### Use Consumer IDs Consistently

Use meaningful consumer IDs to track costs:
- `team-ml-training`
- `project-chatbot-prod`
- `dev-john-experiments`

### Monitor Session Expiration

Sessions have a 12-hour hard maximum. Set up monitoring:
- Check `expires_at` in session details
- Use `idle_threshold_minutes` for automatic cleanup
- Extend sessions before they expire if needed

### Handle SSH Key Security

1. Never log SSH private keys
2. Store keys with restricted permissions (`chmod 600`)
3. Delete keys after sessions end
4. Consider using a secrets manager for production

### Clean Up After Yourself

Always signal completion or destroy sessions when done:
- Prevents orphan charges
- Frees resources for others
- Maintains accurate cost tracking

For troubleshooting help, see [[TROUBLESHOOTING]].
