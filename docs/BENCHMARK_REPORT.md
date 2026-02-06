# GPU Benchmark Report

**Generated**: February 6, 2026
**Total Benchmarks**: 23
**GPUs Tested**: 9 (RTX 3090, RTX 4090, RTX 5060 Ti, RTX 5070, RTX 5070 Ti, RTX 5090, A100 80GB, H200 NVL)
**Models Tested**: 6 (qwen2:1.5b, qwen2:7b, phi3:mini, deepseek-r1:14b, deepseek-r1:32b, deepseek-r1:70b)
**Providers**: Vast.ai, TensorDock

## Executive Summary

This benchmark suite evaluated LLM inference performance across consumer and datacenter GPUs using Ollama. Key findings:

1. **H200 NVL dominates large models** - 36.27 TPS on deepseek-r1:70b (10x faster than A100)
2. **RTX 5090 leads consumer GPUs** - 305 TPS on qwen2:7b, handles 32b models effectively
3. **RTX 5070 Ti best value overall** - 285 TPS on phi3:mini at $0.094/hr delivers 10.9M tokens/dollar
4. **RTX 3090 best value for medium models** - $0.08/hr on Vast.ai delivers 7.5M tokens/dollar
5. **Provider variance is significant** - Same GPU can differ 20-80% between providers
6. **New Blackwell GPUs (50-series)** show strong small-model performance at competitive prices

## Performance Results by Model

### phi3:mini (3.8B Small Model)

| GPU | Provider | TPS | $/hr | Tokens/$ |
|-----|----------|-----|------|----------|
| **RTX 5070 Ti** | Vast.ai | **284.7** | $0.094 | **10.9M** |

**Note**: RTX 5070 Ti showed exceptional consistency (282.8-286.7 TPS range across 20 requests).

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

| GPU | VRAM | $/hr | Best For | Key Strength | Key Limitation |
|-----|------|------|----------|--------------|----------------|
| **H200 NVL** | 144GB HBM3e | $2.00 | 70B+ models | 10x faster than A100 on 70B; no memory pressure on any model | Expensive; only worth it for 70B+ |
| **RTX 5090** | 32GB | $0.21 | Best consumer GPU overall | 72.5 TPS on 32B (7x faster than RTX 4090); excellent price/perf | Still consumer-grade reliability |
| **A100 80GB** | 80GB HBM2e | $0.33 | Large models (>24GB VRAM) | Consistent performance; datacenter reliability; ECC memory | Higher base cost than consumer GPUs |
| **RTX 4090** | 24GB | $0.16-0.44 | Production inference <24GB | Strong small/medium model performance | Struggles with 32B (13 TPS); significant provider variance |
| **RTX 5070 Ti** | 16GB | $0.094 | Best value for small models | 285 TPS on phi3:mini; 10.9M tokens/dollar; exceptional consistency | 16GB limits model size |
| **RTX 5070** | 12GB | $0.18 | Entry Blackwell | 173 TPS on 1.5B; competitive pricing | 12GB VRAM limits to small models |
| **RTX 5060 Ti** | 16GB | $0.15 | Mid-range small models | 214 TPS on 1.5B; good value | Limited benchmark coverage |
| **RTX 3090** | 24GB | $0.08-0.20 | Best cost efficiency | 7.5M tokens/dollar on qwen2:7b at $0.08/hr (Vast.ai) | Older arch hurts on large models; Vast.ai 2x faster than TensorDock |

## Provider Comparison

| Dimension | Vast.ai | TensorDock |
|-----------|---------|------------|
| **Pricing** | Significantly cheaper (RTX 3090 $0.08 vs $0.20, RTX 4090 $0.16 vs $0.44) | 2-3x more expensive for same GPU |
| **Performance** | Higher and more consistent TPS | Lower TPS with higher variance |
| **Consistency** | Tight TPS ranges (e.g. RTX 3090 qwen2:7b: 167.2-168.4) | Wide TPS ranges (e.g. RTX 3090 qwen2:7b: 88.6-169.5) |
| **Provisioning** | Generally faster, more reliable | 80%+ stale inventory; frequent provisioning failures |
| **GPU Selection** | Consumer + datacenter (H200, 50-series, A100) | Mostly consumer GPUs |
| **RTX 3090 qwen2:7b** | 167.4 TPS @ $0.08/hr = **7.5M tok/$** | 126.7 TPS @ $0.20/hr = 2.3M tok/$ |
| **RTX 4090 deepseek-r1:14b** | 96.7 TPS @ $0.16/hr = **2.2M tok/$** | 93.7 TPS @ $0.44/hr = 767K tok/$ |
| **Best Feature** | RTX 3090 at $0.08/hr is exceptional value | Competitive on specific niche configurations |

## Recommendations

### By Model Size

| Model Size | Budget Pick | Performance Pick |
|------------|-------------|------------------|
| 1.5-4B | RTX 5070 Ti @ $0.094/hr | RTX 5070 Ti @ $0.094/hr |
| 7B | RTX 3090 Vast @ $0.08/hr | RTX 5090 @ $0.21/hr |
| 14B | RTX 3090 Vast @ $0.08/hr | RTX 5090 @ $0.21/hr |
| 32B | RTX 5090 @ $0.21/hr | A100 80GB @ $0.33/hr |
| 70B | A100 80GB @ $0.33/hr | H200 NVL @ $2.00/hr |

### By Use Case

| Use Case | GPU | Provider | $/hr | Why |
|----------|-----|----------|------|-----|
| Dev/Test (small models) | RTX 5070 Ti | Vast.ai | $0.094 | Best tokens per dollar (10.9M/dollar) |
| Dev/Test (medium models) | RTX 3090 | Vast.ai | $0.08 | Best value for 7B+ models |
| Production (<24GB models) | RTX 5090 | Vast.ai | $0.21 | Best performance/price balance |
| Production (32B-70B) | A100 80GB | Vast.ai | $0.33 | Required VRAM headroom |
| High-throughput (70B+) | H200 NVL | Vast.ai | $2.00 | 10x faster than A100 |

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
