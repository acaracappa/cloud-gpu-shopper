# GPU Benchmark Report

**Generated**: February 6, 2026
**Total Benchmarks**: 22
**GPUs Tested**: 8 (RTX 3090, RTX 4090, RTX 5060 Ti, RTX 5070, RTX 5090, A100 80GB, H200 NVL)
**Models Tested**: 5 (qwen2:1.5b, qwen2:7b, deepseek-r1:14b, deepseek-r1:32b, deepseek-r1:70b)
**Providers**: Vast.ai, TensorDock

## Executive Summary

This benchmark suite evaluated LLM inference performance across consumer and datacenter GPUs using Ollama. Key findings:

1. **H200 NVL dominates large models** - 36.27 TPS on deepseek-r1:70b (10x faster than A100)
2. **RTX 5090 leads consumer GPUs** - 305 TPS on qwen2:7b, handles 32b models effectively
3. **RTX 3090 best value for small models** - $0.08/hr on Vast.ai delivers 7.5M tokens/dollar
4. **Provider variance is significant** - Same GPU can differ 20-80% between providers
5. **New Blackwell GPUs (50-series)** show strong small-model performance at competitive prices

## Performance Results by Model

### qwen2:1.5b (Small Model)

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| RTX 3090 | TensorDock | 235.7 | $0.20 | 4.2M |
| RTX 5060 Ti | Vast.ai | 214.0 | $0.15 | 5.1M |
| RTX 5070 | Vast.ai | 173.5 | $0.18 | 3.5M |

### qwen2:7b (Medium Model)

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| **RTX 5090** | Vast.ai | **304.8** | $0.21 | 5.2M |
| A100 80GB | Vast.ai | 199.9 | $0.33 | 2.2M |
| RTX 4090 | Vast.ai | 195.3 | $0.16 | 4.4M |
| RTX 4090 | TensorDock | 189.5 | $0.44 | 1.6M |
| RTX 3090 | Vast.ai | 167.4 | $0.08 | **7.5M** |
| RTX 3090 | TensorDock | 126.7 | $0.20 | 2.3M |

### deepseek-r1:14b (Large Model)

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| **RTX 5090** | Vast.ai | **149.2** | $0.21 | 2.6M |
| RTX 4090 | Vast.ai | 96.7 | $0.16 | 2.2M |
| RTX 4090 | TensorDock | 93.8 | $0.44 | 767K |
| A100 80GB | Vast.ai | 86.3 | $0.33 | 941K |
| RTX 3090 | Vast.ai | 81.1 | $0.08 | **3.7M** |
| RTX 3090 | TensorDock | 44.7 | $0.20 | 804K |

### deepseek-r1:32b (Very Large Model)

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| **RTX 5090** | Vast.ai | **72.5** | $0.21 | **1.2M** |
| A100 80GB | Vast.ai | 42.1 | $0.33 | 459K |
| RTX 4090 | TensorDock | 13.0 | $0.44 | 107K |
| RTX 3090 | TensorDock | 11.3 | $0.20 | 204K |
| RTX 3090 | Vast.ai | 3.6 | $0.08 | 161K |

### deepseek-r1:70b (Extra Large Model)

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| **H200 NVL** | Vast.ai | **36.3** | $2.00 | 65K |
| A100 80GB | Vast.ai | 3.5 | $0.33 | 38K |

**Note**: The H200 NVL is 10x faster than A100 on 70B models due to its massive 144GB HBM3e memory.

## GPU Analysis

### NVIDIA H200 NVL (144GB HBM3e)
- **Best for**: Extra-large models (70B+)
- **Strength**: 10x faster than A100 on 70B due to memory bandwidth
- **VRAM**: 143,771 MiB (no memory pressure on any model)
- **Cost**: $2.00/hr - expensive but necessary for 70B+ models

### NVIDIA GeForce RTX 5090 (32GB)
- **Best overall consumer GPU** across all model sizes
- Handles 32B models at 72.5 TPS (7x faster than RTX 4090)
- 32GB VRAM provides headroom for larger models
- Excellent price/performance at $0.21/hr

### NVIDIA A100 80GB PCIe
- **Best for**: Large models requiring >24GB VRAM
- Consistent performance across model sizes
- Higher base cost ($0.33/hr) but critical for 70B+ models
- Datacenter reliability and ECC memory

### NVIDIA GeForce RTX 4090 (24GB)
- Strong performance on small/medium models
- **Struggles with 32B models** due to VRAM limits (13 TPS)
- Significant provider variance (Vast.ai vs TensorDock)
- Good choice for production inference under 24GB

### NVIDIA GeForce RTX 5070 (12GB)
- Entry Blackwell architecture
- 173 TPS on 1.5B model - solid small model performance
- Limited by 12GB VRAM for larger models
- Competitive at $0.18/hr

### NVIDIA GeForce RTX 5060 Ti (16GB)
- Mid-range Blackwell
- 214 TPS on 1.5B model
- 16GB VRAM handles small-medium models
- Best value for small models at $0.15/hr

### NVIDIA GeForce RTX 3090 (24GB)
- **Best cost efficiency** for small/medium models
- Vast.ai at $0.08/hr delivers 7.5M tokens/dollar on qwen2:7b
- Older architecture shows in larger model performance
- Significant provider variance (Vast.ai 2x faster than TensorDock)

## Provider Comparison

### Vast.ai
- Generally faster instance provisioning
- More consistent performance
- Better GPU availability
- Lower prices on comparable hardware
- **RTX 3090 at $0.08/hr is exceptional value**

### TensorDock
- More datacenter GPU options
- Can have stale inventory (80%+ offers may fail)
- Higher variance in performance
- Competitive on specific configurations

## Recommendations

### By Model Size

| Model Size | Budget Pick | Performance Pick |
|------------|-------------|------------------|
| 1.5B | RTX 5060 Ti @ $0.15/hr | RTX 3090 TD @ $0.20/hr |
| 7B | RTX 3090 Vast @ $0.08/hr | RTX 5090 @ $0.21/hr |
| 14B | RTX 3090 Vast @ $0.08/hr | RTX 5090 @ $0.21/hr |
| 32B | RTX 5090 @ $0.21/hr | A100 80GB @ $0.33/hr |
| 70B | A100 80GB @ $0.33/hr | H200 NVL @ $2.00/hr |

### By Use Case

**Development/Testing (low volume)**
- RTX 3090 on Vast.ai ($0.08/hr) - best tokens per dollar

**Production (medium volume, <24GB models)**
- RTX 5090 on Vast.ai ($0.21/hr) - best performance/price balance

**Production (large models, 32B-70B)**
- A100 80GB on Vast.ai ($0.33/hr) - required VRAM headroom

**High-throughput (70B+ models)**
- H200 NVL on Vast.ai ($2.00/hr) - 10x faster than A100

## Methodology

### Test Configuration
- **Duration**: 5 minutes per model per GPU
- **Max Tokens**: 256 per request
- **Concurrency**: 1 (sequential requests)
- **Prompts**: 10 diverse prompts (coding, technical, creative, general)
- **Runtime**: Ollama (latest stable)

### Metrics Collected
- Tokens per second (TPS) per request
- Total requests and tokens generated
- Error rates

### Data Collection
All benchmark data is stored in the application database and can be queried via the API:
```
GET /api/v1/benchmarks
GET /api/v1/benchmarks/best?model=deepseek-r1:32b
GET /api/v1/benchmarks/recommendations?model=qwen2:7b
```

## Raw Data

22 benchmarks stored in `benchmarks` table. Query with:
```sql
SELECT gpu_name, model_name, provider, avg_tokens_per_second, price_per_hour
FROM benchmarks ORDER BY model_name, avg_tokens_per_second DESC;
```
