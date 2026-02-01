# Vast.ai API Comprehensive Reference

**Compiled**: 2026-01-31
**Source**: Official Vast.ai documentation and vast.py source code analysis

---

## Table of Contents

1. [API Overview](#api-overview)
2. [Authentication](#authentication)
3. [Base URLs and Configuration](#base-urls-and-configuration)
4. [Complete API Endpoint List](#complete-api-endpoint-list)
5. [Instance Creation Workflow](#instance-creation-workflow)
6. [Port Networking and Exposure](#port-networking-and-exposure)
7. [SSH Key Management](#ssh-key-management)
8. [Instance Types and Pricing](#instance-types-and-pricing)
9. [Connection Methods](#connection-methods)
10. [CLI Reference](#cli-reference)
11. [Python SDK](#python-sdk)
12. [Data Movement and Storage](#data-movement-and-storage)
13. [Serverless Architecture](#serverless-architecture)
14. [Rate Limits and Best Practices](#rate-limits-and-best-practices)

---

## API Overview

Vast.ai provides a REST API for programmatic control over GPU instances. The API enables:
- Searching and filtering GPU offers
- Provisioning and destroying instances
- Managing SSH keys and credentials
- Monitoring instance status and costs
- Data transfer between instances and cloud storage

---

## Authentication

### API Key

- **Header Format**: `Authorization: Bearer YOUR_API_KEY`
- **Alternative**: Query parameter `?api_key=YOUR_API_KEY`
- **Storage Location**: `~/.config/vastai/vast_api_key` (XDG-compliant) or legacy `~/.vast_api_key`

### Setting Up API Key (CLI)

```bash
vastai set api-key YOUR_HEXADECIMAL_KEY
```

### API Key Permissions

Default keys have full access. Restricted keys can be created with custom permission JSON structures using the `create api-key` command.

---

## Base URLs and Configuration

| Environment | Base URL |
|-------------|----------|
| **Production** | `https://console.vast.ai/api/v0` |
| **Override** | Set via `VAST_URL` environment variable |

All endpoints are prefixed with `/api/v0` if not already present.

---

## Complete API Endpoint List

### Instance Management

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /instances/` | GET | List all user instances |
| `GET /instances/{id}/` | GET | Get specific instance details |
| `PUT /asks/{offer_id}/` | PUT | Create instance from offer |
| `DELETE /instances/{id}/` | DELETE | Destroy instance permanently |
| `PUT /instances/{id}/` | PUT | Manage instance state (start/stop/label) |
| `POST /instances/{id}/ssh/` | POST | Attach SSH key to instance |
| `PUT /instances/bid_price/{id}/` | PUT | Modify spot instance bid |
| `POST /instances/take_snapshot/{id}/` | POST | Create container snapshot |

### Offers and Search

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /bundles/` | GET | Search available GPU offers |
| `GET /bundles/?q={json_query}` | GET | Search with filters |

### SSH Keys

| Endpoint | Method | Description |
|----------|--------|-------------|
| `POST /ssh/` | POST | Add SSH key to account |
| `GET /ssh/` | GET | List account SSH keys |
| `DELETE /ssh/{id}/` | DELETE | Remove SSH key from account |
| `POST /instances/{id}/ssh/` | POST | Attach SSH key to instance |
| `DELETE /instances/{id}/ssh/{key_id}/` | DELETE | Detach SSH key from instance |

### Data Transfer

| Endpoint | Method | Description |
|----------|--------|-------------|
| `PUT /commands/copy_direct/` | PUT | Instance-to-instance copy |
| `DELETE /commands/copy_direct/` | DELETE | Cancel copy operation |
| `PUT /commands/rsync/` | PUT | Local-to-remote sync |
| `POST /commands/rclone/` | POST | Cloud provider sync |
| `DELETE /commands/rclone/` | DELETE | Cancel cloud sync |

### Secrets and Environment

| Endpoint | Method | Description |
|----------|--------|-------------|
| `POST /secrets/` | POST | Create encrypted environment variable |
| `GET /secrets/` | GET | List environment variables |
| `DELETE /secrets/{id}/` | DELETE | Remove environment variable |

### Billing

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /invoices/` | GET | List invoices |
| `GET /earnings/` | GET | Earnings history (for hosts) |
| `GET /deposit/{instance_id}/` | GET | Deposit details for instance |

### Serverless

| Endpoint | Method | Description |
|----------|--------|-------------|
| `POST /endptjobs/` | POST | Create endpoint group |
| `DELETE /endptjobs/{id}/` | DELETE | Delete endpoint |
| `PUT /endptjobs/{id}/` | PUT | Update endpoint |
| `POST /autojobs/` | POST | Create workergroup |
| `DELETE /autojobs/{id}/` | DELETE | Delete workergroup |
| `GET /route/` | POST | Get GPU instance for processing |

### Volumes

| Endpoint | Method | Description |
|----------|--------|-------------|
| `GET /volumes/` | GET | List volumes |
| `POST /volumes/` | POST | Create volume |
| `DELETE /volumes/{id}/` | DELETE | Delete volume |

---

## Instance Creation Workflow

### Step 1: Search for Offers

```bash
GET /bundles/?q={"rentable":{"eq":true},"gpu_name":{"eq":"RTX 4090"}}
```

**Query Filter Fields**:
- `rentable` - Boolean, filter for available instances
- `gpu_name` - GPU model name
- `gpu_ram` - GPU memory in MB (use `gte` for minimum)
- `dph_total` - Total price per hour (use `lte` for maximum)
- `reliability2` - Reliability score 0-1 (use `gte` for minimum)
- `num_gpus` - Number of GPUs
- `compute_cap` - CUDA compute capability (e.g., 800 = 8.0)
- `direct_port_count` - Number of direct ports available

**Response Fields** (Bundle/Offer):
```json
{
  "offers": [
    {
      "id": 12345,
      "ask_contract_id": 67890,
      "machine_id": 111,
      "gpu_name": "RTX 4090",
      "gpu_ram": 24576,
      "num_gpus": 1,
      "cpu_cores": 16,
      "cpu_ram": 65536,
      "disk_space": 500,
      "dph_base": 0.45,
      "dph_total": 0.50,
      "inet_down": 1000,
      "inet_up": 500,
      "geolocation": "US",
      "rentable": true,
      "rented": false,
      "reliability2": 0.95,
      "verified": true,
      "static_ip": false,
      "public_ipaddr": "1.2.3.4"
    }
  ]
}
```

### Step 2: Create Instance

```bash
PUT /asks/{offer_id}/
Content-Type: application/json
Authorization: Bearer YOUR_API_KEY

{
  "client_id": "me",
  "image": "nvidia/cuda:12.2.0-runtime-ubuntu22.04",
  "disk": 50,
  "label": "my-session-label",
  "runtype": "ssh_proxy",
  "ssh_key": "ssh-ed25519 AAAA... user@host",
  "onstart": "#!/bin/bash\necho 'Starting...'",
  "env": {
    "MY_VAR": "value"
  },
  "ports": "8000/http,8080/http"
}
```

**Create Request Fields**:
| Field | Type | Description |
|-------|------|-------------|
| `client_id` | string | Always "me" |
| `image` | string | Docker image reference |
| `disk` | int | Disk space in GB (cannot be changed after creation) |
| `label` | string | Instance label/name |
| `runtype` | string | Launch mode: `ssh_proxy`, `jupyter_proxy`, `ssh_direct`, `args` |
| `ssh_key` | string | SSH public key (optional, better to use separate endpoint) |
| `onstart` | string | On-start script (bash) |
| `env` | object | Environment variables |
| `ports` | string | Port mappings (e.g., "8000/http,8080/http") |
| `args` | string | Container arguments (for runtype=args) |

**Response**:
```json
{
  "success": true,
  "new_contract": 12345678,
  "error": ""
}
```

### Step 3: Attach SSH Key (Recommended Approach)

```bash
POST /instances/{instance_id}/ssh/
Content-Type: application/json
Authorization: Bearer YOUR_API_KEY

{
  "ssh_key": "ssh-ed25519 AAAA... user@host"
}
```

**Important**: The `ssh_key` parameter in the create request doesn't reliably register the key. Always call the dedicated SSH key attachment endpoint after creation. The key takes approximately 10-15 seconds to propagate.

### Step 4: Poll for Ready Status

```bash
GET /instances/{instance_id}/
Authorization: Bearer YOUR_API_KEY
```

**Instance Status Response**:
```json
{
  "instances": {
    "id": 12345678,
    "actual_status": "running",
    "intended_status": "running",
    "ssh_host": "ssh.vast.ai",
    "ssh_port": 20544,
    "public_ipaddr": "1.2.3.4",
    "gpu_name": "RTX 4090",
    "num_gpus": 1,
    "dph_total": 0.50,
    "start_date": 1706745600.0
  }
}
```

**Status Values**:
- `creating` - Instance being provisioned
- `loading` - Docker image being pulled (10-60 minutes for large images)
- `running` - Ready for use
- `exited` - Container stopped
- `offline` - Host machine offline

---

## Port Networking and Exposure

### Architecture

Vast.ai instances share public IP addresses. Each instance receives a subset of ports on the shared public IP, with internal ports mapped randomly to external ports.

### Opening Ports

**Method 1: Docker Options in Create Request**
```json
{
  "ports": "8081/http,8082/udp"
}
```

**Method 2: Environment Variables**
```json
{
  "env": {
    "OPEN_BUTTON_PORT": "8000"
  }
}
```

### Default Ports

| Mode | Internal Port | Automatic |
|------|---------------|-----------|
| SSH | 22 | Yes |
| Jupyter | 8080 | Yes |

### Port Limits

- Maximum 64 open ports per container
- Identity port mapping (external=internal) available for ports above 70000: `-p 70000:70000`

### Accessing Mapped Ports

**Environment Variables Available in Container**:
- `$VAST_TCP_PORT_22` - External SSH port
- `$VAST_TCP_PORT_8080` - External Jupyter port
- `$VAST_TCP_PORT_X` - External port for internal TCP port X
- `$VAST_UDP_PORT_X` - External port for internal UDP port X

**API Response**: Instance details include port mappings in the format:
```
65.130.162.74:33526 -> 8081/tcp
```

### UI Control Variables

| Variable | Purpose |
|----------|---------|
| `OPEN_BUTTON_PORT` | Links instance button to specific internal port |
| `JUPYTER_PORT` | Controls Jupyter button mapping |
| `JUPYTER_TOKEN` | Sets Jupyter authentication token |

---

## SSH Key Management

### Key Generation

**ED25519 (Recommended)**:
```bash
ssh-keygen -t ed25519 -C "your_email@example.com"
```

Generates:
- `~/.ssh/id_ed25519` (private key - keep confidential)
- `~/.ssh/id_ed25519.pub` (public key - add to account/instance)

### Account-Level Keys

Adding a key to account applies only to NEW instances:
```bash
POST /ssh/
{
  "ssh_key": "ssh-ed25519 AAAA..."
}
```

### Instance-Level Keys

Attach to specific instance:
```bash
POST /instances/{id}/ssh/
{
  "ssh_key": "ssh-ed25519 AAAA..."
}
```

### Connection Command

```bash
ssh -p {PORT} root@{HOST} -L 8080:localhost:8080
```

Example:
```bash
ssh -p 20544 root@ssh.vast.ai -L 8080:localhost:8080
```

**Notes**:
- SSH uses lowercase `-p` for port
- SCP/SFTP use uppercase `-P` for port
- Password authentication is disabled - SSH keys only
- Instances default to tmux sessions

### Port Forwarding

Create secure tunnels with `-L` flag:
```bash
ssh -p 20544 root@ssh.vast.ai -L 8080:localhost:8080 -L 8000:localhost:8000
```

---

## Instance Types and Pricing

### Three Instance Types

| Type | Priority | Pricing | Interruption | Best For |
|------|----------|---------|--------------|----------|
| **On-demand** | High | Fixed | None | Production workloads |
| **Reserved** | High | Up to 50% discount | None | Long-term projects |
| **Interruptible** | Low (bid-based) | 50%+ cheaper | Possible | Fault-tolerant work |

### Pricing Model

- **Marketplace-based**: Hosts set prices independently
- **Per-second billing**: GPU charges only when running
- **Storage**: Charged continuously, even when stopped
- **Bandwidth**: Per-byte transfer costs

### Cost Components

| Component | When Charged | Notes |
|-----------|--------------|-------|
| GPU Compute | Per-second while running | Stops when instance stopped |
| Storage | Continuous | Continues even when stopped |
| Bandwidth | Per transfer | Varies by host |

### Reserved Instance Details

- Convert any on-demand instance to reserved
- Commitment periods: 1, 3, or 6 months
- Partial refunds available for early cancellation
- Create via CLI: `vastai prepay instance ID AMOUNT`

---

## Connection Methods

### Three Launch Modes

| Mode | RunType | SSH Access | Jupyter Access | Entrypoint |
|------|---------|------------|----------------|------------|
| SSH | `ssh_proxy` or `ssh_direct` | Yes | No | Overridden |
| Jupyter | `jupyter_proxy` or `jupyter_direct` | No | Yes | Overridden |
| Entrypoint | `args` | Optional | No | Runs native |

### SSH Mode

- Establishes terminal connections via port 22
- Tries both direct SSH and backup proxy connections
- Docker entrypoint overridden; startup handled by onstart script
- Supports VS Code Remote-SSH extension

### Jupyter Mode

- Web-based notebook interface via port 8080
- Proxy connection works everywhere
- Direct HTTPS requires TLS certificate installation
- Certificate download: https://console.vast.ai/static/jvastai_root.cer

### Entrypoint Mode

- Runs Docker container's native entrypoint
- Suitable for automated GPU workers
- No SSH/Jupyter unless image provides them
- Use for batch processing or API servers

### Instance Portal

- Provides web access to any application via secure tunnels
- Alternative to SSH for browser-based access

---

## CLI Reference

### Installation

**PyPI (Stable)**:
```bash
pip install vastai
```

**GitHub (Latest)**:
```bash
wget https://raw.githubusercontent.com/vast-ai/vast-python/master/vast.py -O vast
chmod +x vast
```

### Authentication

```bash
vastai set api-key YOUR_API_KEY
```

### Core Commands

```bash
# Search for offers
vastai search offers 'compute_cap >= 800' -o 'dph_total+'

# Create instance
vastai create instance {OFFER_ID} --image pytorch/pytorch --disk 50 --ssh --direct

# List instances
vastai show instances

# Get instance details
vastai show instance {INSTANCE_ID}

# Start/Stop instance
vastai start instance {INSTANCE_ID}
vastai stop instance {INSTANCE_ID}

# Destroy instance
vastai destroy instance {INSTANCE_ID}

# Attach SSH key
vastai attach ssh-key {INSTANCE_ID} {KEY_FILE_OR_STRING}

# Data transfer
vastai copy {SRC_INSTANCE_ID}:/path /local/path
vastai cloud copy {INSTANCE_ID}:/path s3://bucket/path

# Show API call (debug)
vastai --explain search offers
```

### Instance States

Instances transition: creation -> image pulling -> booting -> running

- Storage charges begin at creation
- GPU charges begin when running
- Stopping preserves storage, eliminates GPU costs

---

## Python SDK

### Installation

```bash
pip install vastai-sdk
```

### Basic Usage

```python
from vastai_sdk import VastAI

vast_sdk = VastAI(api_key='YOUR_API_KEY')

# Search offers
offers = vast_sdk.search_offers(
    query='gpu_name=RTX_4090 rented=False rentable=True'
)

# Launch instance
result = vast_sdk.launch_instance(
    num_gpus="1",
    gpu_name="RTX_4090",
    image="pytorch/pytorch"
)

# Instance management
vast_sdk.start_instance(ID=12345678)
vast_sdk.stop_instance(ID=12345678)
vast_sdk.destroy_instance(ID=12345678)

# SSH keys
vast_sdk.create_ssh_key()
vast_sdk.show_ssh_keys()
vast_sdk.delete_ssh_key(ID=123456)

# File transfer
vast_sdk.copy(
    src='source_path',
    dst='destination_path',
    identity='identity_file'
)
```

---

## Data Movement and Storage

### Storage Types

**Container Storage**: Local disk allocated at creation, cannot be modified after

**Volumes**:
- Local volumes only (physically tied to machine)
- Mount at `/data` directory by default
- Cannot be moved between machines
- Docker instances only (not VMs)
- Create: `vastai create volume {offer_id} -s {size} -n {name}`
- Mount: `-v {volume_name}:{mount_point}`

### Transfer Methods

**CLI Copy (rsync)**:
```bash
vastai copy {src_instance_id}:/path {dst_instance_id}:/path
```

**Cloud Sync** (Google Drive, S3, Backblaze, Dropbox):
```bash
vastai cloud copy {instance_id}:/path s3://bucket/path
```

**SCP/SFTP**:
```bash
scp -P {PORT} file.txt root@{HOST}:/path/
sftp -P {PORT} root@{HOST}
```

### Important Notes

- Cloud sync only on Docker instances (not VMs)
- Use verified datacenter hosts for cloud integration
- Same-machine transfers are faster and free of internet costs
- Avoid copying to `/root` or `/` directories

---

## Serverless Architecture

### Overview

The Vast.ai serverless system uses PyWorker - a Python web server that enables serverless GPU computing.

### Components

1. **Serverless System** - Manages routing and scaling
2. **GPU Instances** - Execute workloads via PyWorker
3. **Client Application** - Initiates requests

### Request Flow

1. Client calls `POST https://run.vast.ai/route/` to request a worker
2. System returns suitable GPU instance address
3. Client communicates directly with GPU instance
4. PyWorker processes request through ML model
5. PyWorker sends metrics to serverless system

### Security

- Payloads sent directly to GPU instances, never stored on Vast servers
- Route endpoint signs messages using public key for validation

### Pre-built Templates

- Text-Generation-Inference (TGI)
- vLLM
- ComfyUI

---

## Rate Limits and Best Practices

### Recommended Practices

1. **Rate Limiting**: Minimum 1 second between requests
2. **Circuit Breaker**: Implement exponential backoff for failures
3. **Retry Logic**: Retry on 5xx errors and rate limits (429)
4. **Polling**: Poll instance status with 5-10 second intervals

### Error Handling

| Status Code | Meaning | Action |
|-------------|---------|--------|
| 200/201 | Success | Continue |
| 400 | Bad Request | Check parameters |
| 401/403 | Auth Error | Verify API key |
| 404 | Not Found | Resource doesn't exist |
| 429 | Rate Limited | Back off and retry |
| 500+ | Server Error | Retry with backoff |

### SSH Key Propagation

After calling AttachSSHKey, the key takes approximately 10-15 seconds to propagate to the instance. Implement polling with retry for SSH verification.

### Instance Creation Best Practices

1. Disk size is permanent - choose carefully
2. Use SSH key attachment endpoint rather than create request parameter
3. Poll for "running" status before connecting
4. Image pulls can take 10-60 minutes for large images

---

## Environment Variables Reference

| Variable | Purpose |
|----------|---------|
| `VAST_URL` | Override base API URL |
| `VASTAI_API_KEY` | API key (alternative to config file) |
| `VLLM_MODEL` | Model ID for vLLM template |
| `VLLM_ARGS` | Additional vLLM arguments |
| `MODEL_ID` | Model ID for TGI template |
| `QUANTIZE` | Quantization setting for TGI |
| `OPEN_BUTTON_PORT` | Custom port for instance button |
| `JUPYTER_PORT` | Custom Jupyter port |
| `JUPYTER_TOKEN` | Jupyter authentication token |
| `$VAST_TCP_PORT_X` | External port for internal TCP port X |
| `$VAST_UDP_PORT_X` | External port for internal UDP port X |

---

## Quick Reference: Common Operations

### Provision Instance with SSH Access

```bash
# 1. Search for offer
vastai search offers 'gpu_name=RTX_4090 rentable=True' -o 'dph_total+'

# 2. Create instance
vastai create instance {OFFER_ID} --image nvidia/cuda:12.2.0-runtime-ubuntu22.04 --disk 50 --ssh

# 3. Attach SSH key
vastai attach ssh-key {INSTANCE_ID} ~/.ssh/id_ed25519.pub

# 4. Wait for running status
vastai show instance {INSTANCE_ID}

# 5. Connect
ssh -p {PORT} root@{HOST}
```

### API Equivalent

```python
# 1. Search
GET /bundles/?q={"rentable":{"eq":true},"gpu_name":{"eq":"RTX 4090"}}

# 2. Create
PUT /asks/{offer_id}/
{"client_id":"me","image":"nvidia/cuda:12.2.0-runtime-ubuntu22.04","disk":50,"runtype":"ssh_proxy"}

# 3. Attach SSH key
POST /instances/{id}/ssh/
{"ssh_key":"ssh-ed25519 AAAA..."}

# 4. Poll status
GET /instances/{id}/

# 5. Get connection info from response: ssh_host, ssh_port
```

### Destroy Instance

```bash
# CLI
vastai destroy instance {INSTANCE_ID}

# API
DELETE /instances/{instance_id}/
```

---

## Notes for cloud-gpu-shopper Integration

Based on the existing implementation in `/Users/avc/Documents/cloud-gpu-shopper/internal/provider/vastai/`:

1. **Base URL**: `https://console.vast.ai/api/v0`
2. **Search Endpoint**: `GET /bundles/?q={json_query}`
3. **Create Endpoint**: `PUT /asks/{offer_id}/`
4. **SSH Key Attachment**: `POST /instances/{id}/ssh/` (critical - must use this, not create parameter)
5. **Instance Status**: `GET /instances/{id}/`
6. **Destroy**: `DELETE /instances/{id}/`
7. **List All Instances**: `GET /instances/`

**Key Learnings from Live Testing**:
- SSH key parameter in create request doesn't reliably register the key
- Must call dedicated SSH key attachment endpoint after creation
- Key propagation takes 10-15 seconds
- Circuit breaker pattern recommended for production reliability
