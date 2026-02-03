---
type: reference
title: Provider Reference
created: 2026-02-02
tags:
  - providers
  - vastai
  - tensordock
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

### Known Limitations

1. **SSH Key Propagation Delay**: After provisioning, SSH keys take 10-15 seconds to propagate. Cloud GPU Shopper handles this automatically with verification polling.

2. **Image Pull Times**: Large Docker images (10-50GB) can take 10-60 minutes to pull. Factor this into your workflow.

3. **Shared IP Addresses**: Instances share public IPs. Port mappings are randomized, which Cloud GPU Shopper handles transparently.

4. **Rate Limiting**: Vast.ai has API rate limits. Cloud GPU Shopper implements caching and backoff automatically.

5. **No Instance Resize**: You cannot change GPU count or type after creation. Destroy and recreate instead.

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

## Provider Comparison

### Feature Comparison

| Feature | Vast.ai | TensorDock |
|---------|---------|------------|
| GPU Variety | Extensive (marketplace) | Limited (owned) |
| Pricing Model | Variable (market-based) | Fixed |
| Spot/Interruptible | Yes | No |
| Storage Charges | Separate | Included |
| Bandwidth Charges | Yes | Included |
| SSH Access | Yes | Yes |
| Docker Support | Yes | Yes (VM-based) |
| Instance Tagging | Labels | VM names |
| Rate Limits | Moderate | Conservative |

### Pricing Comparison (Approximate)

*Prices vary by availability and change frequently*

| GPU Type | Vast.ai Range | TensorDock Range |
|----------|---------------|------------------|
| RTX 4090 | $0.30-0.60/hr | $0.40-0.50/hr |
| A100 40GB | $1.00-2.00/hr | $1.50-2.00/hr |
| A100 80GB | $1.50-3.00/hr | $2.00-2.50/hr |
| H100 | $2.50-4.00/hr | N/A |

### Reliability Comparison

| Aspect | Vast.ai | TensorDock |
|--------|---------|------------|
| Uptime | Varies by host | Consistent |
| Performance | Varies | Consistent |
| Support | Community | Direct |
| SLA | None | Service-dependent |

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

For more troubleshooting help, see [[TROUBLESHOOTING]].
