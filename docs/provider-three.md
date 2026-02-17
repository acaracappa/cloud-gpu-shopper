# RunPod Provider Research

**Date**: 2026-02-16
**Status**: Research complete, integration viable
**Branch**: `runpod`

## Overview

RunPod is a GPU cloud platform providing dedicated GPU/CPU instances (called "Pods") billed by the second. It follows the same "provision instance, get SSH credentials, hand off access" model as our existing Vast.ai and TensorDock providers.

**Verdict: STRONG FIT** for cloud-gpu-shopper's third provider slot.

---

## Platform Architecture

RunPod offers four products:

| Product | Description | Relevant? |
|---------|-------------|-----------|
| **Pods** | Dedicated GPU containers, per-second billing | Yes -- maps directly to our provisioner |
| **Serverless** | Auto-scaling inference endpoints | No |
| **Public Endpoints** | Pre-deployed model APIs | No |
| **Instant Clusters** | Multi-node training clusters | No |

### Cloud Tiers

| Tier | Description | Pricing | SSH Support |
|------|-------------|---------|-------------|
| **Secure Cloud** | T3/T4 data centers, SOC2, dedicated GPU | Higher (~$0.10-$0.40/hr premium) | Public IP by default |
| **Community Cloud** | Vetted peer-to-peer hosts, shared host | Lower (10-30% less) | Requires `supportPublicIp: true` |

---

## APIs

RunPod provides two independent APIs. Both expose full pod lifecycle management.

### REST API (recommended for our integration)

- **Base URL**: `https://rest.runpod.io/v1`
- **Auth**: `Authorization: Bearer RUNPOD_API_KEY` header
- **OpenAPI 3.0 spec**: `https://rest.runpod.io/v1/openapi.json`
- Standard HTTP methods, JSON request/response bodies

#### Pod Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/pods` | Create and deploy a new pod |
| `GET` | `/v1/pods` | List all pods (supports filters) |
| `GET` | `/v1/pods/{podId}` | Get pod details |
| `PATCH` | `/v1/pods/{podId}` | Update pod (triggers reset) |
| `DELETE` | `/v1/pods/{podId}` | Terminate/delete pod |
| `POST` | `/v1/pods/{podId}/start` | Start/resume a stopped pod |
| `POST` | `/v1/pods/{podId}/stop` | Stop a running pod |
| `POST` | `/v1/pods/{podId}/reset` | Reset a pod |
| `POST` | `/v1/pods/{podId}/restart` | Restart a pod |

#### Other Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/templates` | List templates |
| `POST` | `/v1/templates` | Create template |
| `GET` | `/v1/templates/{templateId}` | Get template |
| `GET` | `/v1/billing/pods` | Pod billing history |
| `GET` | `/v1/networkvolumes` | List network volumes |

### GraphQL API (supplementary)

- **URL**: `https://api.runpod.io/graphql`
- **Auth**: `?api_key=RUNPOD_API_KEY` query param or `Authorization: Bearer` header
- **Spec**: `https://graphql-spec.runpod.io/`

Needed for operations the REST API doesn't expose:
- `addPublicSSHKey` mutation -- register SSH public key (account-level)
- `gpuTypes` query -- GPU inventory with pricing and stock status
- `podFindAndDeployOnDemand` -- has `stopAfter`/`terminateAfter` fields (not in REST)
- `podRentInterruptable` -- create spot/interruptible pods with bidding

#### Key GraphQL-Only Fields

| Field | Description |
|-------|-------------|
| `startSsh: Boolean` | Explicitly enable SSH daemon |
| `stopAfter: DateTime` | Auto-stop at specified time |
| `terminateAfter: DateTime` | Auto-terminate (maps to our 12h hard max) |
| `gpuTypeIdList: [String]` | Multiple acceptable GPU types in one request |
| `stockStatus` | "High", "Medium", "Low" inventory availability |
| `bidPerGpu` | Spot pricing bid amount |

### Go SDK

- Package: `github.com/runpod/go-sdk`
- **Only covers Serverless endpoints** -- does NOT support Pods
- For Pod management, we must use the REST API directly
- `github.com/runpod/runpodctl/api` has Go types for Pod creation (`CreatePodInput` with `StartSSH` and `SupportPublicIp` fields)

---

## Pod Creation Request

### REST API (`POST /v1/pods`)

```json
{
  "name": "gpu-shopper-session-abc123",
  "imageName": "runpod/pytorch:2.1.0-py3.10-cuda11.8.0-devel-ubuntu22.04",
  "gpuTypeIds": ["NVIDIA GeForce RTX 4090"],
  "gpuCount": 1,
  "cloudType": "SECURE",
  "containerDiskInGb": 50,
  "volumeInGb": 20,
  "volumeMountPath": "/workspace",
  "ports": ["22/tcp", "8888/http"],
  "supportPublicIp": true,
  "env": {
    "PUBLIC_KEY": "ssh-ed25519 AAAA..."
  },
  "allowedCudaVersions": ["12.8", "12.9", "13.0"],
  "dataCenterIds": ["US-TX-3"],
  "interruptible": false
}
```

### Key Create Fields

| Field | Type | Description |
|-------|------|-------------|
| `gpuTypeIds` | `[]string` | GPU types by full name (e.g., `"NVIDIA GeForce RTX 4090"`) |
| `gpuCount` | `int` | Number of GPUs (default: 1) |
| `cloudType` | `string` | `"SECURE"` or `"COMMUNITY"` |
| `imageName` | `string` | Docker image (any registry) |
| `ports` | `[]string` | Port exposure (e.g., `"22/tcp"`, `"8888/http"`) |
| `supportPublicIp` | `bool` | Request public IP for SSH/SCP |
| `env` | `map[string]string` | Environment variables (inc. `PUBLIC_KEY` for SSH) |
| `containerDiskInGb` | `int` | Ephemeral disk (default: 50, wiped on stop) |
| `volumeInGb` | `int` | Persistent volume (default: 20, survives restarts) |
| `allowedCudaVersions` | `[]string` | CUDA version filter |
| `dataCenterIds` | `[]string` | Data center filter |
| `countryCode` | `string` | Country filter |
| `interruptible` | `bool` | Spot instance if `true` |
| `templateId` | `string` | Use a pre-configured template |
| `dockerStartCmd` | `[]string` | Override Docker CMD |
| `dockerEntrypoint` | `[]string` | Override Docker ENTRYPOINT |
| `minVcpuCount` | `int` | Minimum vCPUs |
| `minMemoryInGb` | `int` | Minimum RAM per GPU |

### GraphQL (`podFindAndDeployOnDemand`)

Same fields plus:
- `startSsh: Boolean`
- `stopAfter: DateTime` / `terminateAfter: DateTime`
- `gpuTypeIdList: [String]` (multiple fallback GPU types)
- `bidPerGpu: Float` (for `podRentInterruptable` mutation)

---

## Pod Response

### REST API (`GET /v1/pods/{podId}`)

```json
{
  "id": "xedezhzb9la3ye",
  "name": "gpu-shopper-session-abc123",
  "desiredStatus": "RUNNING",
  "lastStartedAt": "2026-02-16T20:00:00Z",
  "costPerHr": 0.69,
  "adjustedCostPerHr": 0.69,
  "gpu": {
    "id": "NVIDIA GeForce RTX 4090",
    "count": 1,
    "displayName": "RTX 4090",
    "securePrice": 0.69,
    "communityPrice": 0.44
  },
  "image": "runpod/pytorch:2.1.0-py3.10-cuda11.8.0-devel-ubuntu22.04",
  "ports": ["22/tcp", "8888/http"],
  "publicIp": "213.173.108.12",
  "portMappings": {"22": 17445, "8888": 17446},
  "interruptible": false,
  "machine": {
    "gpuTypeId": "NVIDIA GeForce RTX 4090",
    "location": "US-TX-3",
    "dataCenterId": "US-TX-3",
    "supportPublicIp": true,
    "secureCloud": true,
    "costPerHr": 0.69
  }
}
```

**SSH connection**: `ssh root@213.173.108.12 -p 17445 -i <private_key>`

**Machine details**: Use `?includeMachine=true` query param to include machine info.

### Pod Status Values

| `desiredStatus` | Our Mapping | Description |
|-----------------|-------------|-------------|
| `CREATED` | `starting` | Container being set up |
| `RUNNING` | `running` | Active and serving |
| `STOPPED` | `stopped` | Halted, data preserved on volume disk |
| `EXITED` | `error` | Container process exited |
| `TERMINATED` | (deleted) | Permanently deleted |

---

## SSH Access

### Two Connection Methods

| Method | SCP/SFTP | Requirements |
|--------|----------|-------------|
| **Proxied SSH** | No | Always available. `ssh {podId}@ssh.runpod.io` |
| **Direct SSH (public IP)** | Yes | `supportPublicIp: true` + `ports: ["22/tcp"]` + public IP on machine |

**We need Direct SSH** for our provisioning model (SCP file transfers for benchmarks, etc.).

### SSH Key Model

**Critical difference from Vast.ai**: RunPod does NOT generate per-instance SSH keys. Instead:

1. SSH public keys are registered at the **account level** (RunPod settings or `addPublicSSHKey` GraphQL mutation)
2. Per-pod override via `PUBLIC_KEY` environment variable (writes to `~/.ssh/authorized_keys`)
3. The `SSH_PUBLIC_KEY` env var name also works (historical alias)
4. SSH user is always `root`

### SSH Connection Details Extraction

After pod creation, poll `GET /v1/pods/{podId}` until:
- `publicIp` is populated (not null)
- `portMappings["22"]` has the external port

Connection: `ssh root@{publicIp} -p {portMappings["22"]} -i <private_key>`

Inside the pod, these env vars are set automatically:
- `RUNPOD_PUBLIC_IP` -- the pod's public IP
- `RUNPOD_TCP_PORT_22` -- the external SSH port

**Known issue**: The top-level `ipAddress` field in the GraphQL response sometimes returns `null`. The `runtime.ports` array (or REST `publicIp`/`portMappings`) is more reliable.

### Integration Approach for SSH

Two options:

1. **Per-session keys** (recommended): Generate ed25519 keypair per session, set `PUBLIC_KEY` env var at pod creation. Return private key to caller. Simple, matches our existing ephemeral key model.
2. **Account-level key**: Generate one keypair at startup, register via GraphQL `addPublicSSHKey`. Reuse for all sessions. Less API overhead but shared key across sessions.

---

## Networking

### Port Exposure

| Method | Protocol | Requirements |
|--------|----------|-------------|
| **HTTP Proxy** | HTTP/HTTPS only | Always available. URL: `https://{podId}-{port}.proxy.runpod.net` |
| **TCP Port Forwarding** | Any TCP | Requires `supportPublicIp: true`. Random external port assigned. |

- External port numbers are **randomly assigned** (cannot request specific ports)
- Port mappings returned in pod response: `portMappings: {"22": 17445}`
- Services must bind to `0.0.0.0` (not `127.0.0.1`)
- HTTP proxy has 100-second connection timeout (Cloudflare-fronted)
- Symmetrical port trick: ports above 70000 get matching internal/external numbers

### Architecture Constraints

- **linux/amd64 only** -- no ARM support
- No Docker-in-Docker
- No UDP connections (TCP and HTTP only)

---

## GPU Types

48 GPU types available (from OpenAPI spec `gpuTypeIds` enum):

### Enterprise / Data Center

| GPU Type ID | VRAM | Architecture |
|-------------|------|-------------|
| `NVIDIA B200` | 192 GB HBM3e | Blackwell |
| `NVIDIA H200` | 141 GB HBM3e | Hopper |
| `NVIDIA H200 NVL` | 141 GB HBM3e | Hopper |
| `NVIDIA H100 80GB HBM3` | 80 GB HBM3 | Hopper (SXM) |
| `NVIDIA H100 PCIe` | 80 GB HBM2e | Hopper |
| `NVIDIA H100 NVL` | 94 GB HBM3 | Hopper |
| `AMD Instinct MI300X OAM` | 192 GB HBM3 | CDNA 3 |
| `NVIDIA A100-SXM4-80GB` | 80 GB HBM2e | Ampere |
| `NVIDIA A100 80GB PCIe` | 80 GB HBM2e | Ampere |
| `NVIDIA A100-SXM4-40GB` | 40 GB HBM2e | Ampere |

### Professional / Workstation

| GPU Type ID | VRAM | Architecture |
|-------------|------|-------------|
| `NVIDIA RTX PRO 6000 Blackwell Server Edition` | 96 GB GDDR7 | Blackwell |
| `NVIDIA RTX PRO 6000 Blackwell Workstation Edition` | 96 GB GDDR7 | Blackwell |
| `NVIDIA RTX PRO 6000 Blackwell Max-Q Workstation Edition` | 96 GB GDDR7 | Blackwell |
| `NVIDIA RTX 6000 Ada Generation` | 48 GB GDDR6 | Ada Lovelace |
| `NVIDIA RTX 5000 Ada Generation` | 32 GB GDDR6 | Ada Lovelace |
| `NVIDIA A5000 Ada` | 32 GB GDDR6 | Ada Lovelace |
| `NVIDIA RTX A6000` | 48 GB GDDR6 | Ampere |
| `NVIDIA RTX A5000` | 24 GB GDDR6 | Ampere |
| `NVIDIA RTX A4500` | 20 GB GDDR6 | Ampere |
| `NVIDIA RTX A4000` | 16 GB GDDR6 | Ampere |
| `NVIDIA RTX A2000` | 6/12 GB GDDR6 | Ampere |
| `NVIDIA L40S` | 48 GB GDDR6 | Ada Lovelace |
| `NVIDIA L40` | 48 GB GDDR6 | Ada Lovelace |
| `NVIDIA L4` | 24 GB GDDR6 | Ada Lovelace |
| `NVIDIA A40` | 48 GB GDDR6 | Ampere |
| `NVIDIA A30` | 24 GB HBM2e | Ampere |
| `NVIDIA RTX 4000 Ada Generation` | 20 GB GDDR6 | Ada Lovelace |
| `NVIDIA RTX 4000 SFF Ada Generation` | 20 GB GDDR6 | Ada Lovelace |
| `NVIDIA RTX 2000 Ada Generation` | 16 GB GDDR6 | Ada Lovelace |

### Consumer

| GPU Type ID | VRAM | Architecture |
|-------------|------|-------------|
| `NVIDIA GeForce RTX 5090` | 32 GB GDDR7 | Blackwell |
| `NVIDIA GeForce RTX 5080` | 16 GB GDDR7 | Blackwell |
| `NVIDIA GeForce RTX 4090` | 24 GB GDDR6X | Ada Lovelace |
| `NVIDIA GeForce RTX 4080 SUPER` | 16 GB GDDR6X | Ada Lovelace |
| `NVIDIA GeForce RTX 4080` | 16 GB GDDR6X | Ada Lovelace |
| `NVIDIA GeForce RTX 4070 Ti` | 12 GB GDDR6X | Ada Lovelace |
| `NVIDIA GeForce RTX 3090 Ti` | 24 GB GDDR6X | Ampere |
| `NVIDIA GeForce RTX 3090` | 24 GB GDDR6X | Ampere |
| `NVIDIA GeForce RTX 3080 Ti` | 12 GB GDDR6X | Ampere |
| `NVIDIA GeForce RTX 3080` | 10 GB GDDR6X | Ampere |
| `NVIDIA GeForce RTX 3070` | 8 GB GDDR6 | Ampere |

### Legacy

| GPU Type ID | VRAM |
|-------------|------|
| `Tesla V100-SXM2-32GB` | 32 GB HBM2 |
| `Tesla V100-SXM2-16GB` | 16 GB HBM2 |
| `Tesla V100-PCIE-32GB` | 32 GB HBM2 |
| `Tesla V100-PCIE-16GB` | 16 GB HBM2 |
| `Tesla V100-FHHL-16GB` | 16 GB HBM2 |
| `Tesla T4` | 16 GB GDDR6 |

---

## Pricing

### Billing Model

- **Per-second billing** (displayed as hourly rate)
- **No ingress/egress fees**
- **No minimum usage** -- pay only for actual runtime
- Must have 1 hour balance to deploy a pod (credit deposit requirement)
- Account spend limit: $80/hour by default (can be increased)

### GPU Pricing (February 2026, approximate)

| GPU | Secure (On-Demand) | Community | Spot (approx) |
|-----|--------------------|-----------|---------------|
| B200 192GB | ~$5.98/hr | -- | -- |
| H200 141GB | ~$3.59/hr | -- | -- |
| H100 SXM 80GB | ~$2.69/hr | ~$2.39/hr | ~$1.50/hr |
| H100 PCIe 80GB | ~$1.99/hr | -- | -- |
| A100 SXM 80GB | ~$1.74/hr | ~$0.79/hr | -- |
| A100 PCIe 80GB | ~$1.64/hr | ~$0.60/hr | -- |
| L40S 48GB | ~$1.22/hr | -- | -- |
| RTX 5090 32GB | ~$1.10/hr | -- | -- |
| RTX 6000 Ada 48GB | ~$0.79/hr | -- | -- |
| RTX 4090 24GB | ~$0.69/hr | ~$0.44/hr | ~$0.34/hr |
| L4 24GB | ~$0.44/hr | -- | -- |
| A40 48GB | ~$0.35/hr | -- | -- |
| RTX 3090 24GB | ~$0.22/hr | -- | -- |
| RTX A4000 16GB | ~$0.19/hr | -- | -- |

Spot instances are typically **30-60% cheaper** than on-demand.

### Savings Plans

| Term | Typical Discount |
|------|-----------------|
| 1 week | Small discount |
| 1 month | ~5-10% off |
| 3 months | ~10-15% off |
| 6 months | ~15-20% off |

### Comparison with Vast.ai

| GPU | RunPod | Vast.ai | Delta |
|-----|--------|---------|-------|
| A100 PCIe 40GB | $0.60/hr | $0.52/hr | +15% |
| A100 SXM 80GB | $0.79/hr | $0.67/hr | +18% |
| L40 48GB | $0.69/hr | $0.31/hr | +123% |
| H100 80GB | ~$1.50/hr+ | varies | ~20-30% higher |

RunPod is generally **20-30% more expensive** than Vast.ai but offers better reliability, per-second billing, and no egress fees.

### Storage Pricing

| Type | Price | Persistence |
|------|-------|-------------|
| Container disk | Included | Wiped on stop |
| Pod volume | $0.10/GB/month (running), $0.20/GB/month (stopped) | Survives restarts, lost on terminate |
| Network volume (<1TB) | $0.07/GB/month | Survives termination, region-specific |
| Network volume (>1TB) | $0.05/GB/month | Max 4TB |

---

## Data Centers

26+ data centers across 4 continents:

| Region | IDs |
|--------|-----|
| **US East** | US-GA-1, US-GA-2, US-NC-1, US-DE-1 |
| **US Central** | US-IL-1, US-TX-1, US-TX-3, US-TX-4, US-KS-2, US-KS-3 |
| **US West** | US-WA-1, US-CA-2 |
| **Canada** | CA-MTL-1, CA-MTL-2, CA-MTL-3 |
| **Europe** | EU-RO-1, EU-SE-1, EU-CZ-1, EU-NL-1, EU-FR-1 |
| **Iceland** | EUR-IS-1, EUR-IS-2, EUR-IS-3 |
| **Norway** | EUR-NO-1 |
| **Asia Pacific** | AP-JP-1 |
| **Oceania** | OC-AU-1 |

---

## CUDA Version Support

Supported values for `allowedCudaVersions`: `13.0`, `12.9`, `12.8`, `12.7`, `12.6`, `12.5`, `12.4`, `12.3`, `12.2`, `12.1`, `12.0`, `11.8`

---

## Template System

- Templates are Docker image configs with pre-set env vars, ports, storage, and commands
- Template IDs are strings (e.g., `"30zmvf89kd"`)
- Official RunPod templates (PyTorch, etc.) have SSH pre-configured
- Custom templates supported via any Docker image (public or private registry with auth)
- vLLM worker templates pre-built and cached on all machines for fast startup
- Ollama: community-maintained templates (not official RunPod)

### Template Fields

| Field | Description |
|-------|-------------|
| `imageName` | Docker image tag |
| `containerDiskInGb` | Container disk (default: 50) |
| `volumeInGb` | Persistent volume (default: 20) |
| `volumeMountPath` | Mount path (default: `/workspace`) |
| `ports` | Exposed ports (default: `8888/http,22/tcp`) |
| `env` | Environment variables |
| `dockerStartCmd` | Override Docker CMD |
| `dockerEntrypoint` | Override ENTRYPOINT |

---

## Provider Interface Mapping

How RunPod maps to our `internal/provider/interface.go`:

| Our Interface | RunPod Implementation |
|---------------|----------------------|
| `Name()` | Returns `"runpod"` |
| `ListOffers()` | GraphQL `gpuTypes` query with `lowestPrice` for pricing + stock status |
| `CreateInstance()` | `POST /v1/pods` with `supportPublicIp: true`, `ports: ["22/tcp"]`, `env.PUBLIC_KEY` |
| `DestroyInstance()` | `DELETE /v1/pods/{podId}` |
| `GetInstanceStatus()` | `GET /v1/pods/{podId}?includeMachine=true`, extract SSH from `publicIp`/`portMappings` |
| `ListAllInstances()` | `GET /v1/pods`, filter by pod name prefix for orphan detection |
| `SupportsFeature(SpotPricing)` | Yes -- `podRentInterruptable` mutation / `interruptible: true` |
| `SupportsFeature(CustomImages)` | Yes -- any Docker image |
| `SupportsFeature(InstanceTags)` | No -- only pod `name` field available |
| `SupportsFeature(IdleDetection)` | No -- must enforce via our lifecycle service |

### Key Integration Differences

| Aspect | Vast.ai | TensorDock | RunPod |
|--------|---------|------------|--------|
| Offer granularity | Per-machine bundles | Per-server offers | Per-GPU-type (no individual machines) |
| Offer IDs | Numeric bundle IDs | Server UUIDs | GPU type strings (e.g., `"NVIDIA GeForce RTX 4090"`) |
| SSH key injection | Separate `AttachSSHKey` API call | Account-level key | `PUBLIC_KEY` env var at creation |
| SSH port | Always 22 (proxied) | Random (port_forwards) | Random (`portMappings["22"]`) |
| Instance tagging | `label` field | Name prefix | Name prefix |
| Max duration | None (our 12h max) | None (our 12h max) | `terminateAfter` field (GraphQL only) |
| CUDA filtering | `cuda_max_good` on bundles | Manual | `allowedCudaVersions` API field |
| Spot pricing | Bid system on bundles | Not supported | `interruptible: true` flag |
| Billing | Per-hour | Per-hour | Per-second |

### Synthetic Offer IDs

Since RunPod doesn't have per-machine offers, we'll generate synthetic offer IDs:

```
runpod-{gpuTypeId}-{cloudType}
```

Example: `runpod-NVIDIA_GeForce_RTX_4090-SECURE`

---

## Lifecycle Behaviors

| Behavior | RunPod | Our System |
|----------|--------|------------|
| Max duration | None (runs forever) | 12h hard max; can use `terminateAfter` as backup |
| Idle detection | None | Handled by our lifecycle service |
| Low balance | Auto-stop at ~10 min remaining | N/A |
| Spot interruption | 5-second SIGTERM warning | Handle gracefully in session cleanup |
| GPU on restart | May get zero GPUs | Don't rely on pod restart; always terminate + create |
| Billing | Continues while RUNNING, stops when STOPPED | Terminate (not stop) to avoid storage charges |

---

## Limitations

1. **No Docker-in-Docker** -- RunPod manages Docker
2. **No UDP connections** -- TCP and HTTP only
3. **No Windows support**
4. **No SCP/SFTP via basic SSH proxy** -- requires public IP
5. **Account spend limit** -- $80/hr default cap
6. **linux/amd64 only** -- our Docker builds already target amd64
7. **No arbitrary metadata tags** -- only pod `name` field
8. **Savings plans are non-refundable**

---

## GraphQL Queries Reference

### List GPU Types with Pricing and Stock

```graphql
query GpuTypes {
  gpuTypes {
    id
    displayName
    memoryInGb
    cudaCores
    secureCloud
    communityCloud
    securePrice
    communityPrice
    secureSpotPrice
    communitySpotPrice
    oneWeekPrice
    oneMonthPrice
    threeMonthPrice
    sixMonthPrice
    lowestPrice(input: {gpuCount: 1}) {
      minimumBidPrice
      uninterruptablePrice
      stockStatus
      minVcpu
      minMemory
      supportPublicIp
      maxUnreservedGpuCount
      availableGpuCounts
    }
  }
}
```

### List Pods with SSH/Runtime Info

```graphql
query Pods {
  myself {
    pods {
      id
      name
      desiredStatus
      costPerHr
      gpuCount
      imageName
      machineId
      runtime {
        uptimeInSeconds
        ports {
          ip
          isIpPublic
          privatePort
          publicPort
          type
        }
        gpus {
          id
          gpuUtilPercent
          memoryUtilPercent
        }
      }
      machine {
        podHostId
        gpuDisplayName
        location
        supportPublicIp
      }
    }
  }
}
```

### Create On-Demand Pod

```graphql
mutation {
  podFindAndDeployOnDemand(input: {
    name: "gpu-shopper-session-abc123"
    imageName: "runpod/pytorch:2.1.0-py3.10"
    gpuTypeId: "NVIDIA GeForce RTX 4090"
    gpuCount: 1
    volumeInGb: 50
    containerDiskInGb: 20
    ports: "22/tcp,8888/http"
    startSsh: true
    supportPublicIp: true
    terminateAfter: "2026-02-17T08:00:00Z"
    env: [{ key: "PUBLIC_KEY", value: "ssh-ed25519 AAAA..." }]
  }) {
    id
    desiredStatus
    runtime {
      ports { ip isIpPublic privatePort publicPort type }
    }
  }
}
```

---

## Environment Variable

```bash
RUNPOD_API_KEY=xxx  # Set in .env file
```

---

## Sources

### Official Documentation
- [RunPod Overview](https://docs.runpod.io/overview)
- [Pods Overview](https://docs.runpod.io/pods/overview)
- [Choose a Pod](https://docs.runpod.io/pods/choose-a-pod)
- [Manage Pods](https://docs.runpod.io/pods/manage-pods)
- [Pod Pricing](https://docs.runpod.io/pods/pricing)
- [SSH Configuration](https://docs.runpod.io/pods/configuration/use-ssh)
- [Override Public Keys](https://docs.runpod.io/pods/configuration/override-public-keys)
- [Expose Ports](https://docs.runpod.io/pods/configuration/expose-ports)
- [Storage Types](https://docs.runpod.io/pods/storage/types)
- [GPU Types Reference](https://docs.runpod.io/references/gpu-types)
- [Templates Overview](https://docs.runpod.io/pods/templates/overview)

### API References
- [REST API Overview](https://docs.runpod.io/api-reference/overview)
- [REST API - Create Pod](https://docs.runpod.io/api-reference/pods/POST/pods)
- [REST API - List Pods](https://docs.runpod.io/api-reference/pods/GET/pods)
- [REST API - Delete Pod](https://docs.runpod.io/api-reference/pods/DELETE/pods/podId)
- [REST API - Stop Pod](https://docs.runpod.io/api-reference/pods/POST/pods/podId/stop)
- [REST API OpenAPI Spec](https://rest.runpod.io/v1/openapi.json)
- [GraphQL API Spec](https://graphql-spec.runpod.io/)
- [GraphQL Manage Pods](https://docs.runpod.io/sdks/graphql/manage-pods)
- [REST API Blog Post](https://www.runpod.io/blog/runpod-rest-api-gpu-management)

### SDKs and Tools
- [Go SDK (Serverless only)](https://github.com/runpod/go-sdk)
- [runpodctl Go API types](https://pkg.go.dev/github.com/runpod/runpodctl/api)
- [RunPod Docs GitHub Repo](https://github.com/runpod/docs)

### Pricing Comparisons
- [RunPod Pricing Page](https://www.runpod.io/pricing)
- [RunPod GPU Pricing](https://www.runpod.io/gpu-pricing)
- [ComputePrices - RunPod](https://computeprices.com/providers/runpod)
- [GPUCost - RunPod](https://gpucost.org/provider/runpod)
- [Vast.ai vs RunPod 2026](https://medium.com/@velinxs/vast-ai-vs-runpod-pricing-in-2026-which-gpu-cloud-is-cheaper-bd4104aa591b)
- [Northflank RunPod Pricing](https://northflank.com/blog/runpod-gpu-pricing)

### Community References
- [DeepWiki: Pod Creation and Configuration](https://deepwiki.com/runpod/docs/3.1-pod-creation-and-configuration)
- [DeepWiki: Networking and Connectivity](https://deepwiki.com/runpod/docs/3.2-networking-and-connectivity)
- [DeepWiki: SSH Key Management](https://deepwiki.com/runpod/runpodctl/6.1-ssh-key-management)
- [How to Achieve True SSH in RunPod](https://blog.runpod.io/how-to-achieve-true-ssh-on-runpod/)
- [RunPod Proxy Guide](https://www.runpod.io/blog/runpod-proxy-guide)
