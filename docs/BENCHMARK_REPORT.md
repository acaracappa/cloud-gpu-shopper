# GPU Benchmark Report

**Generated**: February 7, 2026
**Total Benchmarks**: 50 (46 with valid TPS)
**GPUs Tested**: 10 (RTX 3090, RTX 4090, RTX 5060 Ti, RTX 5070, RTX 5070 Ti, RTX 5080, RTX 5090, RTX A6000, A100 80GB, H200 NVL)
**Models Tested**: 8 (qwen2:1.5b, qwen2:7b, phi3:mini, mistral:7b, llama3.1:8b, deepseek-r1:14b, deepseek-r1:32b, deepseek-r1:70b)
**Providers**: Vast.ai, TensorDock
**Total Spend**: $25.50 across 99 sessions

## Executive Summary

This benchmark suite evaluated LLM inference performance across consumer and datacenter GPUs using Ollama. Key findings:

1. **RTX 3090 on Vast.ai is the best value** - $0.13-0.14/M tokens across qwen2:7b, mistral:7b, and llama3.1:8b at $0.08/hr
2. **RTX 5090 leads consumer GPUs** - 305 TPS on qwen2:7b, 149 TPS on deepseek-r1:14b, 72.5 TPS on deepseek-r1:32b
3. **Vast.ai wins 6/7 head-to-head matchups** against TensorDock on both TPS and cost
4. **H200 NVL dominates large models** - 36 TPS on deepseek-r1:70b (10x faster than A100)
5. **32GB VRAM boundary is critical** - RTX 5090 handles 32B models at 72.5 TPS; 24GB GPUs collapse to 3-13 TPS
6. **Geographic pricing arbitrage** - Same GPU varies 2.6-3.8x in price across regions
7. **Quality metrics tracked** - TTFT ranges 21ms-10.6s; match rate up to 100% for llama3.1:8b

## Raw Throughput Leaderboard (Top 20)

| Rank | GPU | Model | Provider | TPS | $/hr | $/M tokens | Location |
|------|-----|-------|----------|-----|------|-----------|----------|
| 1 | **RTX 5090** | qwen2:7b | Vast.ai | **304.8** | $0.210 | $0.19 | US |
| 2 | RTX 3090 | qwen2:1.5b | TensorDock | 235.7 | $0.200 | $0.24 | Unknown |
| 3 | RTX 5060 Ti | qwen2:1.5b | Vast.ai | 214.0 | $0.150 | $0.19 | Unknown |
| 4 | A100 80GB | qwen2:7b | Vast.ai | 199.9 | $0.330 | $0.46 | US |
| 5 | RTX 4090 | qwen2:7b | Vast.ai | 195.3 | $0.160 | $0.23 | US |
| 6 | RTX 4090 | qwen2:7b | TensorDock | 189.5 | $0.440 | $0.65 | Joplin, MO |
| 7 | RTX 4090 | mistral:7b | TensorDock | 179.0 | $0.439 | $0.68 | Joplin, MO |
| 8 | RTX 4090 | mistral:7b | TensorDock | 178.4 | $0.377 | $0.59 | Orlando, FL |
| 9 | RTX 4090 | mistral:7b | TensorDock | 176.0 | $0.377 | $0.60 | Manassas, VA |
| 10 | RTX 5070 | qwen2:1.5b | Vast.ai | 173.5 | $0.180 | $0.29 | Unknown |
| 11 | RTX 4090 | llama3.1:8b | TensorDock | 169.0 | $0.396 | $0.65 | Chubbuck, ID |
| 12 | RTX 5080 | mistral:7b | Vast.ai | 168.4 | $0.118 | $0.19 | California, US |
| 13 | RTX 3090 | qwen2:7b | Vast.ai | 167.4 | $0.080 | $0.13 | Spain |
| 14 | RTX 3090 | mistral:7b | Vast.ai | 159.2 | $0.082 | $0.14 | Quebec, CA |
| 15 | RTX 5090 | deepseek-r1:14b | Vast.ai | 149.2 | $0.210 | $0.39 | US |
| 16 | RTX 3090 | llama3.1:8b | Vast.ai | 144.8 | $0.076 | $0.14 | Spain, ES |
| 17 | RTX 3090 | qwen2:7b | TensorDock | 126.7 | $0.200 | $0.44 | Manassas, VA |
| 18 | RTX A6000 | llama3.1:8b | TensorDock | 121.9 | $0.400 | $0.91 | Chubbuck, ID |
| 19 | RTX 4090 | deepseek-r1:14b | Vast.ai | 96.7 | $0.160 | $0.46 | US |
| 20 | RTX 4090 | deepseek-r1:14b | Vast.ai | 93.8 | $0.322 | $0.95 | Washington, US |

## Cost Efficiency Leaderboard (Top 15 by $/M Tokens)

| Rank | GPU | Model | Provider | $/M tokens | TPS | $/hr | Location |
|------|-----|-------|----------|-----------|-----|------|----------|
| **1** | **RTX 3090** | **qwen2:7b** | **Vast.ai** | **$0.13** | 167.4 | $0.080 | Spain |
| **2** | **RTX 3090** | **mistral:7b** | **Vast.ai** | **$0.14** | 159.2 | $0.082 | Quebec, CA |
| **3** | **RTX 3090** | **llama3.1:8b** | **Vast.ai** | **$0.14** | 144.8 | $0.076 | Spain, ES |
| 4 | RTX 5060 Ti | llama3.1:8b | Vast.ai | $0.19 | 83.2 | $0.056 | Spain, ES |
| 5 | RTX 5090 | qwen2:7b | Vast.ai | $0.19 | 304.8 | $0.210 | US |
| 6 | RTX 5080 | mistral:7b | Vast.ai | $0.19 | 168.4 | $0.118 | California, US |
| 7 | RTX 5060 Ti | qwen2:1.5b | Vast.ai | $0.19 | 214.0 | $0.150 | Unknown |
| 8 | RTX 5060 Ti | mistral:7b | Vast.ai | $0.21 | 89.3 | $0.069 | Ohio, US |
| 9 | RTX 4090 | qwen2:7b | Vast.ai | $0.23 | 195.3 | $0.160 | US |
| 10 | RTX 5060 Ti | llama3.1:8b | Vast.ai | $0.23 | 83.3 | $0.069 | Ohio, US |
| 11 | RTX 3090 | qwen2:1.5b | TensorDock | $0.24 | 235.7 | $0.200 | Unknown |
| 12 | RTX 3090 | deepseek-r1:14b | Vast.ai | $0.26 | 82.8 | $0.079 | Quebec, CA |
| 13 | RTX 3090 | deepseek-r1:14b | Vast.ai | $0.27 | 81.1 | $0.080 | Spain |
| 14 | RTX 5070 | qwen2:1.5b | Vast.ai | $0.29 | 173.5 | $0.180 | Unknown |
| 15 | RTX 5060 Ti | deepseek-r1:14b | Vast.ai | $0.36 | 44.4 | $0.057 | Vietnam, VN |

RTX 3090 on Vast.ai dominates the top 3 spots. All top 15 entries except one are on Vast.ai.

## Performance Results by Model

### qwen2:1.5b (1.5B Small Model)

| GPU | Provider | TPS | $/hr | $/M tokens |
|-----|----------|-----|------|-----------|
| RTX 3090 | TensorDock | 235.7 | $0.200 | $0.24 |
| **RTX 5060 Ti** | **Vast.ai** | **214.0** | **$0.150** | **$0.19** |
| RTX 5070 | Vast.ai | 173.5 | $0.180 | $0.29 |

**Fastest**: RTX 3090 (235.7 TPS). **Cheapest**: RTX 5060 Ti on Vast.ai ($0.19/M tokens).

### phi3:mini (3.8B Small Model)

| GPU | Provider | TPS | $/hr | $/M tokens |
|-----|----------|-----|------|-----------|
| **RTX 5070 Ti** | Vast.ai | **284.7** | $0.094 | $0.09 |

RTX 5070 Ti showed exceptional consistency (282.8-286.7 TPS range across 20 requests).

### qwen2:7b (7B Medium Model)

| GPU | Provider | TPS | $/hr | $/M tokens |
|-----|----------|-----|------|-----------|
| **RTX 5090** | Vast.ai | **304.8** | $0.210 | $0.19 |
| A100 80GB | Vast.ai | 199.9 | $0.330 | $0.46 |
| RTX 4090 | Vast.ai | 195.3 | $0.160 | $0.23 |
| RTX 4090 | TensorDock | 189.5 | $0.440 | $0.65 |
| **RTX 3090** | **Vast.ai** | **167.4** | **$0.080** | **$0.13** |
| RTX 3090 | TensorDock | 126.7 | $0.200 | $0.44 |

**Fastest**: RTX 5090 (304.8 TPS). **Cheapest**: RTX 3090 on Vast.ai ($0.13/M tokens — cheapest inference in our entire dataset).

### mistral:7b (7B Medium Model)

| GPU | Provider | TPS | $/hr | $/M tokens | Location |
|-----|----------|-----|------|-----------|----------|
| RTX 4090 | TensorDock | 176.0-179.0 | $0.377-0.439 | $0.59-0.68 | Manassas/Orlando/Joplin |
| RTX 5080 | Vast.ai | 168.4 | $0.118 | $0.19 | California, US |
| **RTX 3090** | **Vast.ai** | **159.2** | **$0.082** | **$0.14** | Quebec, CA |
| RTX 5060 Ti | Vast.ai | 89.3 | $0.069 | $0.21 | Ohio, US |

**Fastest**: RTX 4090 on TensorDock (179 TPS). **Cheapest**: RTX 3090 on Vast.ai ($0.14/M tokens).

### llama3.1:8b (8B Medium Model)

| GPU | Provider | TPS | $/hr | $/M tokens | TTFT | Match Rate | Location |
|-----|----------|-----|------|-----------|------|------------|----------|
| RTX 4090 | TensorDock | 169.0 | $0.396 | $0.65 | N/A | N/A | Chubbuck, ID |
| **RTX 3090** | **Vast.ai** | **144.8** | **$0.076** | **$0.14** | 4454ms | 100% | Spain, ES |
| RTX A6000 | TensorDock | 121.9 | $0.400 | $0.91 | N/A | N/A | Chubbuck, ID |
| RTX 5060 Ti | Vast.ai | 83.3 | $0.069 | $0.23 | 6149ms | 100% | Ohio, US |
| RTX 5060 Ti | Vast.ai | 83.2 | $0.056 | $0.19 | 6200ms | N/A | Spain, ES |

**Fastest**: RTX 4090 on TensorDock (169 TPS). **Cheapest**: RTX 3090 on Vast.ai ($0.14/M tokens).

### deepseek-r1:14b (14B Large Model — 17 benchmarks)

| GPU | Provider | TPS | $/hr | $/M tokens | TTFT | Location |
|-----|----------|-----|------|-----------|------|----------|
| **RTX 5090** | Vast.ai | **149.2** | $0.210 | $0.39 | N/A | US |
| RTX 4090 | Vast.ai | 93.6-96.7 | $0.16-0.54 | $0.46-1.59 | 6429ms | US/Washington/Ohio |
| RTX 4090 | TensorDock | 92.3-93.8 | $0.377-0.44 | $1.14-1.30 | 265ms | Manassas/Joplin |
| RTX 5080 | Vast.ai | 88.8 | $0.127 | $0.40 | 21ms | Unknown |
| A100 80GB | Vast.ai | 86.3 | $0.330 | $1.06 | N/A | US |
| **RTX 3090** | **Vast.ai** | **81.1-82.8** | **$0.079-0.080** | **$0.26-0.27** | 6890ms | Spain/Quebec |
| RTX 3090 | TensorDock | 44.7-80.1 | $0.200 | $0.69-1.24 | N/A | Manassas, VA |
| RTX A6000 | TensorDock | 68.0 | $0.400 | $1.63 | N/A | Chubbuck, ID |
| RTX 5060 Ti | Vast.ai | 44.4 | $0.057 | $0.36 | 10618ms | Vietnam, VN |
| RTX 5090 | TensorDock | 11.1 | $0.540 | $13.54 | N/A | Chubbuck, ID |

**Fastest**: RTX 5090 on Vast.ai (149.2 TPS). **Cheapest**: RTX 3090 on Vast.ai ($0.26/M tokens).

Note: TensorDock RTX 5090 at 11.1 TPS is an outlier caused by incomplete driver initialization — Vast.ai delivers 13.4x more TPS for the same GPU.

### deepseek-r1:32b (32B Very Large Model — 7 benchmarks)

| GPU | Provider | TPS | $/hr | $/M tokens | TTFT | Location |
|-----|----------|-----|------|-----------|------|----------|
| **RTX 5090** | **Vast.ai** | **72.5** | **$0.210** | **$0.80** | N/A | US |
| RTX 4090 | Vast.ai | 44.5 | $0.141 | $0.88 | 10422ms | India, IN |
| A100 80GB | Vast.ai | 42.1 | $0.330 | $2.18 | N/A | US |
| RTX 4090 | TensorDock | 10.0-13.0 | $0.377-0.44 | $9.38-10.48 | 3082ms | Joplin/Unknown |
| RTX 3090 | TensorDock | 11.3 | $0.200 | $4.91 | N/A | Manassas, VA |
| RTX 3090 | Vast.ai | 3.6 | $0.080 | $6.20 | N/A | Spain |

**Key findings**:
- RTX 5090 is the only consumer GPU that handles 32B well (72.5 TPS) — its 32GB VRAM keeps the model in memory
- RTX 4090 on Vast.ai at 44.5 TPS is 3.4x faster than TensorDock (13 TPS) for the same GPU — Docker template optimization
- 24GB GPUs (RTX 3090, RTX 4090 on TensorDock) collapse to 3-13 TPS due to CPU offloading
- A100 at 42.1 TPS despite 80GB VRAM shows older HBM2e bandwidth loses to RTX 5090's faster GDDR7

### deepseek-r1:70b (70B Extra Large Model)

| GPU | Provider | TPS | $/hr | $/M tokens |
|-----|----------|-----|------|-----------|
| **H200 NVL** | Vast.ai | **36.3** | $2.000 | $15.32 |
| A100 80GB | Vast.ai | 3.5 | $0.330 | $26.57 |

The H200 NVL is 10x faster than A100 on 70B models due to its 144GB HBM3e memory fitting the entire model without offloading.

## GPU Tier Analysis

| GPU | VRAM | $/hr Range | Benchmarks | Best For | Key Strength | Key Limitation |
|-----|------|-----------|------------|----------|--------------|----------------|
| **H200 NVL** | 144GB HBM3e | $2.00 | 1 | 70B+ models | 10x faster than A100 on 70B; entire model fits in VRAM | Expensive; only justified for 70B+ |
| **A100 80GB** | 80GB HBM2e | $0.33 | 4 | Reliable large model inference | Consistent performance; ECC memory; 4 models tested | Older HBM2e bandwidth limits TPS vs consumer GPUs |
| **RTX 5090** | 32GB GDDR7 | $0.21-0.54 | 4 | Best consumer GPU overall | 305 TPS qwen2:7b; 72.5 TPS deepseek-r1:32b; handles 32B in-VRAM | 2.6x pricing spread between providers |
| **RTX 5080** | 16GB GDDR7 | $0.12-0.13 | 2 | Mid-range 7-14B models | 168 TPS mistral:7b, 89 TPS deepseek-r1:14b; 21ms TTFT | 16GB limits to medium models |
| **RTX 5070 Ti** | 16GB GDDR7 | $0.094 | 1 | Best value for small models | 285 TPS phi3:mini; exceptional consistency | 16GB limits model size |
| **RTX 5070** | 12GB GDDR7 | $0.18 | 1 | Entry Blackwell | 173 TPS qwen2:1.5b | 12GB VRAM limits to small models |
| **RTX 5060 Ti** | 16GB GDDR7 | $0.06-0.15 | 5 | Budget inference (7-8B) | 83 TPS llama3.1:8b at $0.19-0.23/M tokens | Half the TPS of RTX 4090 |
| **RTX 4090** | 24GB GDDR6X | $0.14-0.54 | 16 | Most-tested GPU; versatile | 195 TPS qwen2:7b; 44.5 TPS deepseek-r1:32b (Vast.ai) | 3.8x pricing spread; 3.4x TPS gap Vast.ai vs TensorDock on 32B |
| **RTX A6000** | 48GB GDDR6 | $0.40 | 2 | Workstation inference | 122 TPS llama3.1:8b; large VRAM for model headroom | Poor value — RTX 3090 is 2x cheaper at similar TPS |
| **RTX 3090** | 24GB GDDR6X | $0.08-0.20 | 11 | **Best cost efficiency** | $0.13-0.14/M tokens; 6 models tested; top 3 cost rankings | Older architecture collapses on 32B+ (3-11 TPS) |

## Provider Head-to-Head Comparison

### Direct Matchups (Same GPU + Model)

| GPU | Model | Vast.ai TPS | Vast.ai $/hr | TensorDock TPS | TensorDock $/hr | TPS Winner | Cost Winner |
|-----|-------|-------------|-------------|----------------|----------------|------------|-------------|
| RTX 3090 | deepseek-r1:14b | 82.8 | $0.079 | 80.1 | $0.200 | **Vast.ai** | **Vast.ai** |
| RTX 3090 | deepseek-r1:32b | 3.6 | $0.080 | 11.3 | $0.200 | TensorDock | TensorDock |
| RTX 3090 | qwen2:7b | 167.4 | $0.080 | 126.7 | $0.200 | **Vast.ai** | **Vast.ai** |
| RTX 4090 | deepseek-r1:14b | 96.7 | $0.160 | 93.8 | $0.439 | **Vast.ai** | **Vast.ai** |
| RTX 4090 | deepseek-r1:32b | 44.5 | $0.141 | 13.0 | $0.440 | **Vast.ai** | **Vast.ai** |
| RTX 4090 | qwen2:7b | 195.3 | $0.160 | 189.5 | $0.440 | **Vast.ai** | **Vast.ai** |
| RTX 5090 | deepseek-r1:14b | 149.2 | $0.210 | 11.1 | $0.540 | **Vast.ai** | **Vast.ai** |

**Vast.ai wins 6/7 on TPS and 6/7 on cost.** TensorDock only wins RTX 3090 deepseek-r1:32b where both GPUs struggle with CPU offloading.

### Provider Summary

| Dimension | Vast.ai | TensorDock |
|-----------|---------|------------|
| **Pricing** | 2-3x cheaper (RTX 3090 $0.08 vs $0.20, RTX 4090 $0.16 vs $0.44) | 2-3x more expensive for same GPU |
| **Performance** | Higher TPS in 6/7 matchups | Only wins when model exceeds VRAM |
| **Consistency** | Tight TPS ranges across instances | 57% variance on RTX 3090 deepseek-r1:14b (44.7-80.1 TPS) |
| **32B Inference** | RTX 4090: 44.5 TPS with Docker template | RTX 4090: 13 TPS (3.4x slower) |
| **Reliability** | ~60% automated benchmark success rate | 0% automated benchmark success (driver issues) |
| **GPU Selection** | Consumer + datacenter (H200, 50-series, A100) | Mostly consumer GPUs |

## Quality Metrics — Time to First Token (TTFT)

10 benchmarks captured TTFT data:

| GPU | Model | Provider | TTFT | TPS | Location |
|-----|-------|----------|------|-----|----------|
| RTX 5080 | deepseek-r1:14b | Vast.ai | **21ms** | 88.8 | Unknown |
| RTX 4090 | deepseek-r1:14b | TensorDock | 265ms | 93.7 | Unknown |
| RTX 4090 | deepseek-r1:32b | TensorDock | 3,082ms | 10.0 | Unknown |
| RTX 3090 | llama3.1:8b | Vast.ai | 4,454ms | 144.8 | Spain, ES |
| RTX 5060 Ti | llama3.1:8b | Vast.ai | 6,149ms | 83.3 | Ohio, US |
| RTX 5060 Ti | llama3.1:8b | Vast.ai | 6,200ms | 83.2 | Spain, ES |
| RTX 4090 | deepseek-r1:14b | Vast.ai | 6,429ms | 93.6 | Ohio, US |
| RTX 3090 | deepseek-r1:14b | Vast.ai | 6,890ms | 82.8 | Quebec, CA |
| RTX 4090 | deepseek-r1:32b | Vast.ai | 10,422ms | 44.5 | India, IN |
| RTX 5060 Ti | deepseek-r1:14b | Vast.ai | 10,618ms | 44.4 | Vietnam, VN |

Note: High TTFT values (4-10s) likely include cold-start model loading time. The RTX 5080's 21ms TTFT suggests the model was already loaded in memory.

## Model Size Scaling

How TPS drops as model parameters increase — best result per GPU:

```
A100 80GB PCIe:
  qwen2:7b        (  7B):  199.9 TPS  ########################################
  deepseek-r1:14b ( 14B):   86.3 TPS  #################
  deepseek-r1:32b ( 32B):   42.1 TPS  ########
  deepseek-r1:70b ( 70B):    3.5 TPS  #

RTX 5090:
  qwen2:7b        (  7B):  304.8 TPS  #############################################################
  deepseek-r1:14b ( 14B):  149.2 TPS  ##############################
  deepseek-r1:32b ( 32B):   72.5 TPS  ##############

RTX 5060 Ti:
  qwen2:1.5b      (1.5B):  214.0 TPS  ###########################################
  mistral:7b      (  7B):   89.3 TPS  ##################
  llama3.1:8b     (  8B):   83.3 TPS  #################
  deepseek-r1:14b ( 14B):   44.4 TPS  #########

RTX 4090:
  qwen2:7b        (  7B):  195.3 TPS  #######################################
  mistral:7b      (  7B):  179.0 TPS  ####################################
  llama3.1:8b     (  8B):  169.0 TPS  ##################################
  deepseek-r1:14b ( 14B):   96.7 TPS  ###################
  deepseek-r1:32b ( 32B):   44.5 TPS  #########

RTX 3090:
  qwen2:1.5b      (1.5B):  235.7 TPS  ###############################################
  qwen2:7b        (  7B):  167.4 TPS  ##################################
  mistral:7b      (  7B):  159.2 TPS  ################################
  llama3.1:8b     (  8B):  144.8 TPS  #############################
  deepseek-r1:14b ( 14B):   82.8 TPS  #################
  deepseek-r1:32b ( 32B):   11.3 TPS  ##
```

**Key insight**: The 32GB VRAM boundary is critical for 32B models. RTX 5090 (32GB) delivers 72.5 TPS while RTX 3090 (24GB) collapses to 11 TPS due to CPU offloading. The A100 (80GB) at 42.1 TPS shows that memory bandwidth matters more than capacity once the model fits.

## Pareto-Optimal Picks (Best Tradeoff by Model)

| Model | Budget Pick (cheapest $/M) | Performance Pick (highest TPS) |
|-------|---------------------------|-------------------------------|
| qwen2:1.5b | RTX 5060 Ti Vast.ai — $0.19/M, 214 TPS | RTX 3090 TensorDock — $0.24/M, 236 TPS |
| qwen2:7b | RTX 3090 Vast.ai — $0.13/M, 167 TPS | RTX 5090 Vast.ai — $0.19/M, 305 TPS |
| mistral:7b | RTX 3090 Vast.ai — $0.14/M, 159 TPS | RTX 5080 Vast.ai — $0.19/M, 168 TPS |
| llama3.1:8b | RTX 3090 Vast.ai — $0.14/M, 145 TPS | RTX 4090 TensorDock — $0.65/M, 169 TPS |
| deepseek-r1:14b | RTX 3090 Vast.ai — $0.26/M, 83 TPS | RTX 5090 Vast.ai — $0.39/M, 149 TPS |
| deepseek-r1:32b | RTX 5090 Vast.ai — $0.80/M, 72.5 TPS | (same — only viable consumer option) |
| deepseek-r1:70b | H200 NVL Vast.ai — $15.32/M, 36.3 TPS | (same — only viable option) |

## Anomalies and Notable Findings

### High Variance Instances
- **RTX 3090 deepseek-r1:14b on TensorDock**: 44.7-80.1 TPS (57% spread) — same GPU, model, and provider
- **RTX 4090 deepseek-r1:32b on TensorDock**: 10.0-13.0 TPS (26% spread)

### VRAM Boundary Effects (deepseek-r1:32b)
| GPU | VRAM | TPS | Analysis |
|-----|------|-----|----------|
| RTX 5090 | 32GB | 72.5 | Model fits in VRAM |
| RTX 4090 (Vast.ai) | 24GB | 44.5 | CPU offload, but Docker template optimizes |
| A100 80GB | 80GB | 42.1 | Model fits, but older HBM2e limits bandwidth |
| RTX 4090 (TensorDock) | 24GB | 13.0 | CPU offload, no Docker optimization |
| RTX 3090 (TensorDock) | 24GB | 11.3 | CPU offload |
| RTX 3090 (Vast.ai) | 24GB | 3.6 | CPU offload, poor host |

### Geographic Pricing Arbitrage
| GPU | Cheapest | Most Expensive | Range |
|-----|----------|---------------|-------|
| RTX 4090 | $0.141/hr (India) | $0.536/hr (Ohio) | 3.8x |
| RTX 5060 Ti | $0.056/hr (Spain) | $0.150/hr (Unknown) | 2.7x |
| RTX 5090 | $0.210/hr (Vast.ai US) | $0.540/hr (TensorDock) | 2.6x |
| RTX 3090 | $0.076/hr (Spain) | $0.200/hr (Manassas) | 2.6x |

### TensorDock Driver Anomaly
RTX 5090 on TensorDock: 11.1 TPS vs 149.2 TPS on Vast.ai (13.4x gap). Caused by incomplete nvidia driver initialization — nvidia-smi fails before drivers fully load on TensorDock instances.

## Recommendations

### By Model Size

| Model Size | Budget Pick | Performance Pick |
|------------|-------------|------------------|
| 1.5-4B | RTX 5060 Ti Vast.ai @ $0.07/hr | RTX 5070 Ti Vast.ai @ $0.094/hr |
| 7-8B | RTX 3090 Vast.ai @ $0.08/hr | RTX 5090 Vast.ai @ $0.21/hr |
| 14B | RTX 3090 Vast.ai @ $0.08/hr | RTX 5090 Vast.ai @ $0.21/hr |
| 32B | RTX 5090 Vast.ai @ $0.21/hr | A100 80GB Vast.ai @ $0.33/hr |
| 70B | A100 80GB Vast.ai @ $0.33/hr | H200 NVL Vast.ai @ $2.00/hr |

### By Use Case

| Use Case | GPU | Provider | $/hr | Why |
|----------|-----|----------|------|-----|
| Hobby / Dev | RTX 3090 | Vast.ai | $0.08 | $0.13-0.14/M tokens; best value in entire dataset |
| Budget production (7-8B) | RTX 5060 Ti | Vast.ai | $0.07 | 83 TPS llama3.1:8b at $0.19-0.23/M tokens |
| Fast production (7-14B) | RTX 5090 | Vast.ai | $0.21 | 305 TPS qwen2:7b; 149 TPS deepseek-r1:14b |
| Production (32B) | RTX 5090 | Vast.ai | $0.21 | 72.5 TPS; only consumer GPU that handles 32B well |
| Reliable production | A100 80GB | Vast.ai | $0.33 | ECC memory; consistent performance; 42 TPS deepseek-r1:32b |
| High-throughput (70B+) | H200 NVL | Vast.ai | $2.00 | 10x faster than A100; 144GB fits 70B+ entirely in VRAM |

### Provider Selection

**Always prefer Vast.ai.** It wins on pricing (2-3x cheaper), performance (6/7 head-to-head wins), consistency, and reliability. TensorDock has 100% automated benchmark failure due to driver issues and should only be used for manual SSH workloads where you can wait for driver installation.

## Methodology

### Test Configuration
- **Duration**: 5-minute throughput test + 5 quality prompts per model per GPU
- **Max Tokens**: 500 per request
- **Concurrency**: 1 (sequential requests)
- **Prompts**: 6 types (reasoning, coding, knowledge, creative, instruction, throughput)
- **Runtime**: Ollama 0.15.4+
- **Automation**: Benchmark runner provisions instances, uploads script via SCP, polls for JSON results

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

Automated benchmark runs:
```bash
# Launch a benchmark matrix
curl -X POST localhost:8080/api/v1/benchmark-runs \
  -d '{"models":["llama3.1:8b","deepseek-r1:14b"],"gpu_types":["RTX 3090","RTX 4090"],"providers":["vastai"],"max_budget":1.00}'

# Monitor progress
curl localhost:8080/api/v1/benchmark-runs/{id}
```

## Raw Data

50 benchmarks stored in `benchmarks` table (46 with valid TPS). Query with:
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
