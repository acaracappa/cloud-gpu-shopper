---
type: reference
title: Provider Reference
created: 2026-02-02
tags:
  - providers
  - vastai
  - tensordock
  - bluelobster
related:
  - "[[API]]"
  - "[[CONFIGURATION]]"
  - "[[WORKFLOWS]]"
---

# Provider Reference

Cloud GPU Shopper integrates with multiple GPU cloud providers. This document covers provider-specific setup, features, and recommendations.

---

## Vast.ai

### Overview

Vast.ai is a marketplace for GPU compute where independent hosts offer their hardware. It provides a wide variety of GPU types at competitive prices.

### Account Setup

1. **Create Account**: Visit [vast.ai](https://vast.ai/) and sign up
2. **Add Payment Method**: Go to Billing → Payment Methods
3. **Add Funds**: Vast.ai uses prepaid credits (recommended: start with $10-20)
4. **Generate API Key**:
   - Navigate to **Account** → **API Keys**
   - Click "Create API Key"
   - Copy the hexadecimal key string

### API Configuration

```bash
VASTAI_API_KEY=your_64_character_hex_key_here
```

### Pricing Model

Vast.ai offers three pricing tiers:

| Type | Description | Best For |
|------|-------------|----------|
| **On-demand** | Fixed price, guaranteed availability | Production workloads |
| **Reserved** | Up to 50% discount, commitment required | Long-term projects |
| **Interruptible** | Bid-based, cheapest, can be preempted | Fault-tolerant batch work |

Cloud GPU Shopper uses **on-demand** pricing by default.

**Cost Components**:
- **GPU Compute**: Per-second billing while running
- **Storage**: Continuous charge, even when stopped
- **Bandwidth**: Per-byte transfer costs (varies by host)

### Instance Tagging

Cloud GPU Shopper tags all Vast.ai instances with:
- Session ID in the instance label
- Deployment ID for orphan detection
- Expiration timestamp

This enables:
- Automatic orphan detection and cleanup
- Multi-deployment isolation
- Cost attribution

### Disk Allocation

When creating a session, you can specify the disk size using the `disk_gb` parameter:

```json
POST /api/v1/sessions
{
  "offer_id": "vastai-12345",
  "consumer_id": "my-app",
  "reservation_hours": 2,
  "workload_type": "ssh",
  "disk_gb": 200
}
```

**Important considerations**:
- **Default**: 50GB if not specified
- **Immutable**: Disk size cannot be changed after instance creation
- **Large Models**: For large models (e.g., DeepSeek-V2.5 236B at 132GB), allocate sufficient space
- **Templates**: Vast.ai templates include a `recommended_disk_space` field to guide allocation
- **Storage Costs**: Disk storage is charged continuously, even when the instance is stopped

### Known Limitations

1. **SSH Key Propagation Delay**: After provisioning, SSH keys take 10-15 seconds to propagate. Cloud GPU Shopper handles this automatically with verification polling.

2. **Image Pull Times**: Large Docker images (10-50GB) can take 10-60 minutes to pull. Factor this into your workflow.

3. **Shared IP Addresses**: Instances share public IPs. Port mappings are randomized, which Cloud GPU Shopper handles transparently.

4. **Rate Limiting**: Vast.ai has API rate limits. Cloud GPU Shopper implements caching and backoff automatically.

5. **No Instance Resize**: You cannot change GPU count or type after creation. Destroy and recreate instead.

6. **Fixed Disk Size**: Disk allocation is permanent and cannot be modified after instance creation. Plan your storage needs upfront.

### Tips for Vast.ai

- **Check Reliability Scores**: Higher `reliability2` scores indicate more stable hosts
- **Prefer Verified Hosts**: Verified datacenters have better uptime
- **Start Small**: Test with shorter reservations before committing to long jobs
- **Monitor Disk Usage**: Storage costs continue even when stopped

---

## TensorDock

### Overview

TensorDock operates its own infrastructure, providing more consistent performance and availability compared to marketplace models.

### Account Setup

1. **Create Account**: Visit [tensordock.com](https://tensordock.com/) and sign up
2. **Add Payment Method**: Go to Billing and add a credit card
3. **Generate API Credentials**:
   - Navigate to **Dashboard** → **API** → **Credentials**
   - Create new credentials
   - Note both:
     - **Authorization ID** (for `TENSORDOCK_AUTH_ID`)
     - **API Token** (for `TENSORDOCK_API_TOKEN`)

### API Configuration

```bash
TENSORDOCK_AUTH_ID=your_authorization_id_here
TENSORDOCK_API_TOKEN=your_api_token_here
```

### Pricing Model

TensorDock uses straightforward hourly pricing:

| Component | Billing |
|-----------|---------|
| **GPU Compute** | Per-hour while running |
| **Storage** | Included in compute price |
| **Bandwidth** | Included |

No separate storage or bandwidth charges simplifies cost calculation.

### Instance Tagging

Cloud GPU Shopper tags TensorDock instances with:
- Session ID in the VM name
- Deployment ID for reconciliation
- Metadata for lifecycle management

### Provider-Specific Features

1. **Default OS Image**: Configure via `TENSORDOCK_DEFAULT_IMAGE` (default: `ubuntu2404`)

2. **Location-Based Deployment**: TensorDock organizes resources by geographic location

3. **Consistent Pricing**: Unlike marketplace models, prices are fixed by TensorDock

### Known Limitations

1. **Limited GPU Variety**: Fewer GPU types compared to Vast.ai marketplace

2. **Regional Availability**: Not all GPU types available in all regions

3. **API Rate Limits**: TensorDock recommends ~1 request per second. Cloud GPU Shopper handles this automatically.

### Tips for TensorDock

- **Check Location Availability**: GPU types vary by datacenter
- **Predictable Costs**: Easier to budget than marketplace pricing
- **Reliable Performance**: Infrastructure-owned machines have consistent specs

---

## Blue Lobster

### Overview

Blue Lobster Cloud offers dedicated GPU instances with fixed pricing and direct SSH access. Instances run on dedicated hardware with no oversubscription, providing consistent performance. The API uses async task-based provisioning.

### Account Setup

1. **Create Account**: Visit [bluelobster.ai](https://bluelobster.ai/) and sign up
2. **Add Payment Method**: Go to Billing and add a payment method
3. **Generate API Key**:
   - Navigate to **Account** → **API Keys**
   - Create a new API key
   - Copy the key string

### API Configuration

```bash
BLUELOBSTER_API_KEY=your_api_key_here
```

### Pricing Model

Blue Lobster uses fixed hourly pricing:

| Component | Billing |
|-----------|---------|
| **GPU Compute** | Fixed per-hour rate |
| **Storage** | Included |
| **Bandwidth** | Included |

No marketplace bidding or variable pricing — rates are fixed per instance type.

### Available GPUs

| GPU | VRAM | Approx. Price |
|-----|------|---------------|
| RTX 5090 | 32 GB | $0.75/hr |
| Quadro RTX 8000 | 48 GB | $0.50/hr |
| RTX A5000 | 24 GB | $0.40/hr |
| RTX A4000 | 16 GB | $0.30/hr |
| RTX A6000 | 48 GB | $0.60/hr |
| RTX 2080 Ti | 11 GB | $0.15/hr |

### Regions

| Region Code | Location |
|-------------|----------|
| `igl` | Wilmington, DE (production) |
| `phl` | Philadelphia, PA (development) |

### Instance Templates

Blue Lobster provides OS templates for instance creation. The default GPU template is `UBUNTU-22-04-NV` which includes:
- Ubuntu 22.04 LTS
- NVIDIA drivers pre-installed
- Docker + NVIDIA Container Toolkit

### SSH Access

- **Direct IP**: Instances get a public IP with SSH on port 22 (no port forwarding)
- **Username**: `ubuntu`
- **Key**: Ephemeral keypair generated per session (same as other providers)

### Instance Tagging

Cloud GPU Shopper tags Blue Lobster instances using the API's `metadata` field with:
- `shopper_deployment_id` — identifies our deployment
- `shopper_session_id` — links to our session
- `shopper_expires_at` — expiration timestamp

**Note:** Blue Lobster's API does not reliably persist metadata (BL-007), so `FeatureInstanceTags` is disabled. Orphan detection falls back to instance name matching.

### Known Limitations

1. **Post-Boot DKMS Rebuild**: Instances run `apt-get dist-upgrade` on boot, rebuilding NVIDIA kernel modules for 7-19 minutes. SSH becomes available before the system is fully usable. Cloud GPU Shopper handles this with:
   - 60-second boot delay before SSH polling
   - System readiness probe (checks dpkg locks + nvidia-smi stability)
   - 2 consecutive SSH successes required before marking instance ready
   - Extended timeouts for SCP uploads and benchmark execution

2. **Metadata Not Persisted (BL-007)**: The API accepts metadata on instance creation but may not return it on subsequent queries. Instance tagging is disabled.

3. **Power Status Gaps (BL-008)**: The `power_status` field may be null even when an instance is running. Cloud GPU Shopper infers "running" status from the presence of an IP address.

4. **Async Provisioning**: Instance creation is task-based (launch returns a task ID, poll for completion). Task poll timeout is 5 minutes.

5. **NVIDIA Driver/Library Version Mismatch**: During DKMS rebuild, `nvidia-smi` returns driver/library version mismatch errors. This is transient and resolves once the rebuild completes. Ollama uses CUDA directly and can function despite nvidia-smi instability.

### Tips for Blue Lobster

- **Allow Time for Readiness**: First-boot DKMS rebuild means instances need 7-19 minutes after SSH becomes available before GPU workloads can start. Cloud GPU Shopper handles this automatically.
- **Fixed Pricing**: Easier to budget than marketplace providers — no bidding or price fluctuations.
- **Direct SSH**: No port forwarding needed, simplifies connectivity.
- **RTX 5090 Available**: One of the few providers offering the latest consumer GPU.

---

## Provider Comparison

### Feature Comparison

| Feature | Vast.ai | TensorDock | Blue Lobster |
|---------|---------|------------|--------------|
| GPU Variety | Extensive (marketplace) | Limited (owned) | Moderate (dedicated) |
| Pricing Model | Variable (market-based) | Fixed | Fixed |
| Spot/Interruptible | Yes | No | No |
| Storage Charges | Separate | Included | Included |
| Bandwidth Charges | Yes | Included | Included |
| SSH Access | Yes (mapped ports) | Yes (dedicated IP) | Yes (direct IP, port 22) |
| Docker Support | Yes | Yes (VM-based) | Yes (pre-installed) |
| Instance Tagging | Labels | VM names | Metadata (limited) |
| Rate Limits | Moderate | Conservative | 2 req/s |
| Provisioning | Synchronous | Synchronous | Async (task-based) |

### Pricing Comparison (Approximate)

*Prices vary by availability and change frequently*

| GPU Type | Vast.ai Range | TensorDock Range | Blue Lobster |
|----------|---------------|------------------|--------------|
| RTX 5090 | $0.21/hr | N/A | $0.75/hr |
| RTX 4090 | $0.30-0.60/hr | $0.40-0.50/hr | N/A |
| RTX A5000 | N/A | N/A | $0.40/hr |
| RTX A6000 | N/A | $0.40/hr | $0.60/hr |
| Quadro RTX 8000 | N/A | N/A | $0.50/hr |
| A100 40GB | $1.00-2.00/hr | $1.50-2.00/hr | N/A |
| A100 80GB | $1.50-3.00/hr | $2.00-2.50/hr | N/A |
| H100 | $2.50-4.00/hr | N/A | N/A |

### Reliability Comparison

| Aspect | Vast.ai | TensorDock | Blue Lobster |
|--------|---------|------------|--------------|
| Uptime | Varies by host | Consistent | Consistent |
| Performance | Varies | Consistent | Consistent |
| Support | Community | Direct | Direct |
| SLA | None | Service-dependent | Service-dependent |
| Boot Time | Fast (Docker) | Moderate (VM) | Slow (DKMS rebuild) |

### When to Use Each Provider

**Choose Vast.ai when:**
- You need rare or specific GPU types
- Price sensitivity is high
- You can tolerate some variability
- You need interruptible/spot pricing
- Maximum GPU variety is important

**Choose TensorDock when:**
- Predictable pricing is important
- Consistent performance is critical
- Simpler billing is preferred
- You value direct support
- Bandwidth costs are a concern

**Choose Blue Lobster when:**
- You need RTX 5090 or Quadro RTX 8000 GPUs
- Fixed, predictable pricing matters
- Direct SSH access on port 22 is preferred (no port mapping)
- Dedicated hardware with no oversubscription is important
- You can tolerate longer boot times (DKMS rebuild)

---

## Multi-Provider Strategy

Cloud GPU Shopper enables using multiple providers simultaneously:

### Load Balancing Approach

1. **Query Both Providers**: Search inventory across all providers
2. **Compare Offers**: Filter and sort by price, reliability, availability
3. **Choose Best Match**: Provision from the provider with the best offer

### Failover Strategy

1. **Primary Provider First**: Try your preferred provider
2. **Fallback on Failure**: If unavailable, try alternative provider
3. **Cost Limits**: Set `max_price` to avoid expensive fallbacks

### Example: Finding Best Offer

```bash
# Query all providers for RTX 4090
curl "http://localhost:8080/api/v1/inventory?gpu_type=RTX%204090&max_price=0.50"

# Response includes offers from both providers
# Sort by price_per_hour and reliability to find best option
```

---

## Troubleshooting Provider Issues

### Vast.ai

**"Provider authentication failed"**
- Verify `VASTAI_API_KEY` is correct
- Check key hasn't expired
- Ensure account has payment method and funds

**"Offer no longer available"**
- GPU was rented by another user
- Refresh inventory and try another offer

**SSH connection refused**
- Wait for "running" status (not just "provisioning")
- Allow 10-15 seconds for SSH key propagation
- Check if host is online

### TensorDock

**"Provider authentication failed"**
- Verify both `TENSORDOCK_AUTH_ID` and `TENSORDOCK_API_TOKEN`
- Check credentials haven't been revoked

**"No offers available"**
- Check specific location availability
- Try different GPU types
- Contact TensorDock support if persistent

### Blue Lobster

**"Provider authentication failed"**
- Verify `BLUELOBSTER_API_KEY` is correct
- Check key hasn't been revoked

**"Task poll timeout"**
- Instance creation is async — the task may take up to 5 minutes
- Check Blue Lobster dashboard for instance status
- Retry the request

**SSH connection works but GPU commands fail**
- Blue Lobster instances rebuild NVIDIA kernel modules on boot (7-19 minutes)
- Wait for the DKMS rebuild to complete — `nvidia-smi` will return errors until then
- Cloud GPU Shopper's readiness probe handles this automatically for benchmarks

**"system readiness timeout"**
- The dpkg lock or nvidia-smi stability check exceeded 20 minutes
- This can happen if the instance is performing a large system update
- Check instance status on the Blue Lobster dashboard

For more troubleshooting help, see [[TROUBLESHOOTING]].
