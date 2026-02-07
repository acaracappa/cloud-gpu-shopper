# GPU Benchmark Report

**Generated**: February 7, 2026
**Total Benchmarks**: 49 (45 successful)
**GPUs Tested**: 9 (RTX 3090, RTX 4090, RTX 5060 Ti, RTX 5070, RTX 5070 Ti, RTX 5080, RTX 5090, A100 80GB, H200 NVL)
**Models Tested**: 8 (qwen2:1.5b, qwen2:7b, phi3:mini, mistral:7b, llama3.1:8b, deepseek-r1:14b, deepseek-r1:32b, deepseek-r1:70b)
**Providers**: Vast.ai, TensorDock

## Executive Summary

This benchmark suite evaluated LLM inference performance across consumer and datacenter GPUs using Ollama. Key findings:

1. **RTX 3090 on Vast.ai is the best value** - $0.14/M tokens for llama3.1:8b and mistral:7b at $0.08/hr
2. **RTX 5090 leads consumer GPUs** - 305 TPS on qwen2:7b, handles 32b models effectively (72.5 TPS)
3. **Vast.ai is 3-4x cheaper** than TensorDock for equivalent GPU performance
4. **H200 NVL dominates large models** - 36 TPS on deepseek-r1:70b (10x faster than A100)
5. **Provider variance is significant** - Same GPU can differ 20-80% between providers
6. **Quality metrics now tracked** - TTFT ranges 4.4-10.4s, match rate up to 100% for llama3.1:8b

## Performance Results by Model

### llama3.1:8b (Medium Model — NEW)

| GPU | Provider | TPS | $/hr | $/M tokens | TTFT | Match Rate | Location |
|-----|----------|-----|------|-----------|------|------------|----------|
| RTX 4090 | TensorDock | 169.0 | $0.396 | $0.65 | N/A | N/A | Chubbuck, ID |
| **RTX 3090** | **Vast.ai** | **144.8** | **$0.076** | **$0.14** | 4454ms | 100% | Spain, ES |
| RTX A6000 | TensorDock | 121.9 | $0.400 | $0.91 | N/A | N/A | Chubbuck, ID |
| RTX 5060 Ti | Vast.ai | 83.3 | $0.069 | $0.23 | 6149ms | 100% | Ohio, US |

**Key finding**: RTX 3090 on Vast.ai delivers llama3.1:8b at **$0.14/M tokens** — the cheapest inference in our entire dataset.

### mistral:7b (Medium Model — NEW)

| GPU | Provider | TPS | $/hr | $/M tokens | Location |
|-----|----------|-----|------|-----------|----------|
| RTX 4090 | TensorDock | 176.0-179.0 | $0.377-0.439 | $0.59-0.68 | Manassas/Orlando/Joplin |
| RTX 5080 | Vast.ai | 168.4 | $0.118 | $0.19 | California, US |
| **RTX 3090** | **Vast.ai** | **159.2** | **$0.082** | **$0.14** | Quebec, CA |
| RTX 5060 Ti | Vast.ai | 89.3 | $0.069 | $0.21 | Ohio, US |

**Key finding**: RTX 3090 on Vast.ai matches llama3.1:8b pricing at **$0.14/M tokens** for mistral:7b too.

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

| GPU | Provider | TPS | $/hr | $/M tokens | TTFT | Location |
|-----|----------|-----|------|-----------|------|----------|
| **RTX 5090** | Vast.ai | **149.2** | $0.21 | $0.39 | N/A | US |
| RTX 4090 | Vast.ai | 93.8-96.7 | $0.16-0.32 | $0.46-0.95 | N/A | Washington/US |
| RTX 4090 | TensorDock | 92.3-93.8 | $0.377-0.44 | $1.14-1.30 | 265ms | Manassas/Joplin |
| RTX 5080 | Vast.ai | 88.8 | $0.127 | $0.40 | 21ms | Unknown |
| A100 80GB | Vast.ai | 86.3 | $0.33 | $1.06 | N/A | US |
| RTX 3090 | Vast.ai | 81.1-82.8 | $0.08-0.079 | **$0.26** | 6890ms | Spain/Quebec |
| RTX 3090 | TensorDock | 80.1 | $0.20 | $0.69 | N/A | Manassas, VA |
| RTX A6000 | TensorDock | 68.0 | $0.40 | $1.63 | N/A | Chubbuck, ID |

### deepseek-r1:32b (Very Large Model)

| GPU | Provider | TPS | $/hr | $/M tokens | TTFT | Location |
|-----|----------|-----|------|-----------|------|----------|
| **RTX 5090** | Vast.ai | **72.5** | $0.21 | **$0.80** | N/A | US |
| **RTX 4090** | **Vast.ai** | **44.5** | **$0.141** | **$0.88** | 10422ms | India |
| A100 80GB | Vast.ai | 42.1 | $0.33 | $2.18 | N/A | US |
| RTX 4090 | TensorDock | 13.0 | $0.44 | $9.38 | 3082ms | Unknown |
| RTX 3090 | TensorDock | 11.3 | $0.20 | $4.91 | N/A | Manassas, VA |
| RTX 3090 | Vast.ai | 3.6 | $0.08 | $6.20 | N/A | Spain |

**Key finding**: RTX 4090 on Vast.ai can now handle deepseek-r1:32b at **44.5 TPS** — previously only TensorDock data existed (13 TPS). The 3.4x difference shows Vast.ai's Docker template provides much better runtime optimization.

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
| **RTX 5080** | 16GB | $0.12 | Mid-range Blackwell | 168 TPS mistral:7b, 89 TPS deepseek-r1:14b; strong value | 16GB limits to medium models |
| **RTX 4090** | 24GB | $0.08-0.44 | Production inference <24GB | 44.5 TPS on 32B (Vast.ai); 169 TPS on llama3.1:8b (TensorDock) | Vast.ai vs TensorDock is 3.4x on deepseek-r1:32b |
| **RTX 5070 Ti** | 16GB | $0.094 | Best value for small models | 285 TPS on phi3:mini; 10.9M tokens/dollar; exceptional consistency | 16GB limits model size |
| **RTX 5070** | 12GB | $0.18 | Entry Blackwell | 173 TPS on 1.5B; competitive pricing | 12GB VRAM limits to small models |
| **RTX 5060 Ti** | 16GB | $0.06-0.07 | Budget inference | 83 TPS llama3.1:8b, 89 TPS mistral:7b at $0.21/M tokens | Half the TPS of RTX 4090 |
| **RTX 3090** | 24GB | $0.08-0.20 | **Best cost efficiency** | $0.14/M tokens for llama3.1:8b and mistral:7b (Vast.ai) | Older arch hurts on 32B+ models |

## Provider Comparison

| Dimension | Vast.ai | TensorDock |
|-----------|---------|------------|
| **Pricing** | Significantly cheaper (RTX 3090 $0.08 vs $0.20, RTX 4090 $0.16 vs $0.44) | 2-3x more expensive for same GPU |
| **Performance** | Higher and more consistent TPS | Lower TPS with higher variance |
| **Consistency** | Tight TPS ranges (e.g. RTX 3090 qwen2:7b: 167.2-168.4) | Wide TPS ranges (e.g. RTX 3090 qwen2:7b: 88.6-169.5) |
| **Provisioning** | Generally faster, more reliable | 80%+ stale inventory; frequent provisioning failures |
| **GPU Selection** | Consumer + datacenter (H200, 50-series, A100) | Mostly consumer GPUs |
| **RTX 3090 llama3.1:8b** | 144.8 TPS @ $0.076/hr = **$0.14/M tok** | N/A |
| **RTX 3090 qwen2:7b** | 167.4 TPS @ $0.08/hr = $0.13/M tok | 126.7 TPS @ $0.20/hr = $0.44/M tok |
| **RTX 4090 deepseek-r1:14b** | 96.7 TPS @ $0.16/hr = $0.46/M tok | 93.7 TPS @ $0.44/hr = $1.30/M tok |
| **RTX 4090 deepseek-r1:32b** | 44.5 TPS @ $0.14/hr = $0.88/M tok | 13 TPS @ $0.44/hr = $9.38/M tok |
| **Best Feature** | RTX 3090 at $0.08/hr is exceptional value; 3.4x faster 32B inference | Higher TPS on llama3.1:8b (169 vs 145) |

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
| Dev/Test (7-14B models) | RTX 3090 | Vast.ai | $0.08 | $0.14/M tokens for llama3.1:8b; best value |
| Production (8B models) | RTX 3090 | Vast.ai | $0.08 | 145 TPS, $0.14/M tokens, 100% match rate |
| Production (14B models) | RTX 5090 | Vast.ai | $0.21 | 149 TPS, best performance/price balance |
| Production (32B models) | RTX 4090 | Vast.ai | $0.14 | 44.5 TPS, $0.88/M tokens |
| Production (32B-70B) | A100 80GB | Vast.ai | $0.33 | Required VRAM headroom |
| High-throughput (70B+) | H200 NVL | Vast.ai | $2.00 | 10x faster than A100 |

## Methodology

### Test Configuration
- **Duration**: 5-minute throughput test + 5 quality prompts per model per GPU
- **Max Tokens**: 500 per request
- **Concurrency**: 1 (sequential requests)
- **Prompts**: 6 types (reasoning, coding, knowledge, creative, instruction, throughput)
- **Runtime**: Ollama 0.15.4+
- **Automation**: Benchmark runner provisions instances, uploads script via SCP, collects results

### Metrics Collected
- Tokens per second (TPS) per request with min/max/p50/p95/p99
- Time to first token (TTFT) with percentiles
- Match rate (output correctness on quality prompts)
- GPU utilization, memory, temperature, power draw
- Total requests, tokens, errors, cost

### Data Collection
All benchmark data is stored in the application database and can be queried via the API:
```
GET /api/v1/benchmarks
GET /api/v1/benchmarks/best?model=deepseek-r1:32b
GET /api/v1/benchmarks/recommendations?model=qwen2:7b
```

## Raw Data

49 benchmarks stored in `benchmarks` table (45 with valid TPS). Query with:
```sql
SELECT gpu_name, model_name, provider, avg_tokens_per_second, price_per_hour
FROM benchmarks ORDER BY model_name, avg_tokens_per_second DESC;
```

Query via API:
```bash
curl http://localhost:8080/api/v1/benchmarks
curl "http://localhost:8080/api/v1/benchmarks/best?model=llama3.1:8b"
curl "http://localhost:8080/api/v1/benchmarks/compare?model=deepseek-r1:14b"
```
