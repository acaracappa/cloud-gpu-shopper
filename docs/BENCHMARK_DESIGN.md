# Benchmark Test Design

## Overview

This document outlines the benchmark test architecture, common failure modes, and best practices for reliable benchmark execution.

## Test Matrix

### Planned Test Matrix

| GPU | Provider | Models to Test |
|-----|----------|----------------|
| RTX 3090 | Vast.ai | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| RTX 3090 | TensorDock | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| RTX 4090 | Vast.ai | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| RTX 4090 | TensorDock | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| RTX 5090 | Vast.ai | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| A100 80GB | Vast.ai | qwen2:7b, deepseek-r1:14b, deepseek-r1:32b |
| H200 NVL | Vast.ai | deepseek-r1:70b |
| RTX 5060 Ti | Vast.ai | qwen2:1.5b |
| RTX 5070 | Vast.ai | qwen2:1.5b |

**Total planned**: 28 tests (with some variations for smaller GPUs)

## Known Failure Modes

### 1. Provisioning Failures

| Error | Cause | Mitigation |
|-------|-------|------------|
| "No available public IPs" | TensorDock hostnode exhausted | Retry with different location |
| "Instance stopped" | Provider killed instance | Detect early, fail fast (BUG-011) |
| Stale inventory | TensorDock shows unavailable GPUs | Track location confidence |
| SSH timeout | Heavy image pull (vLLM, etc.) | Template-aware timeouts (BUG-005) |

### 2. Benchmark Execution Failures

| Error | Cause | Mitigation |
|-------|-------|------------|
| OOM during model load | Model too large for VRAM | Check VRAM before loading |
| Ollama not responding | Service crashed | Health check before benchmark |
| Slow/stuck inference | Memory pressure, swapping | Monitor TPS during run |
| Connection lost | Instance terminated | Periodic heartbeat check |

### 3. Result Collection Failures

| Error | Cause | Mitigation |
|-------|-------|------------|
| SCP permission denied | Subagent lacks permissions | Use SSH cat redirect instead |
| Results file missing | Benchmark didn't complete | Check exit status first |
| Partial results | Benchmark interrupted | Save incrementally |
| Results not imported | Manual step forgotten | Auto-import after collection |

## Improved Benchmark Flow

### Phase 1: Setup & Validation

```
1. Provision instance
2. Wait for SSH ready
3. Verify nvidia-smi works
4. Install Ollama
5. Health check Ollama API
6. Record system info (GPU, CUDA, driver)
```

### Phase 2: Model Loading

```
1. Check available VRAM
2. Pull model
3. Verify model loaded (ollama list)
4. Run warmup request
5. Record load time and initial VRAM usage
```

### Phase 3: Benchmark Execution

```
1. Start benchmark with 5-minute timer
2. Log each request: timestamp, tokens, TPS
3. Every 30s: check instance health
4. On completion: save results JSON to /tmp
5. On error: save partial results + error log
```

### Phase 4: Result Collection

```
1. SSH cat results to local file (not SCP!)
2. Validate JSON structure
3. Insert into database immediately
4. Record test status (success/partial/failed)
5. Update test manifest with outcome
```

## Test Manifest Schema

Track all benchmark attempts:

```sql
CREATE TABLE benchmark_manifest (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,           -- Groups tests from same batch
    created_at DATETIME NOT NULL,

    -- What we're testing
    gpu_type TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,

    -- Instance info
    session_id TEXT,
    instance_ip TEXT,
    ssh_port INTEGER,

    -- Status tracking
    status TEXT NOT NULL,           -- pending, running, success, partial, failed, skipped
    started_at DATETIME,
    completed_at DATETIME,

    -- Results
    benchmark_id TEXT,              -- FK to benchmarks table if successful
    tokens_per_second REAL,
    total_requests INTEGER,
    error_count INTEGER,

    -- Failure tracking
    failure_reason TEXT,
    failure_stage TEXT,             -- provision, setup, load, benchmark, collect
    error_message TEXT,

    -- Notes
    notes TEXT
);
```

## Benchmark Script Improvements

### Current Issues

1. Results saved to remote `/tmp/benchmark_results.json` but not auto-retrieved
2. No health checks during benchmark
3. No partial result saving
4. No manifest tracking

### Proposed Changes

```python
#!/usr/bin/env python3
# benchmark.py - Improved benchmark script

import json
import time
import requests
import subprocess
from datetime import datetime

class BenchmarkRunner:
    def __init__(self, model, duration_seconds=300):
        self.model = model
        self.duration = duration_seconds
        self.results = []
        self.start_time = None

    def run(self):
        self.start_time = time.time()
        end_time = self.start_time + self.duration

        # Save initial state
        self._save_checkpoint("started")

        while time.time() < end_time:
            try:
                result = self._run_request()
                self.results.append(result)

                # Save checkpoint every 10 requests
                if len(self.results) % 10 == 0:
                    self._save_checkpoint("running")

            except Exception as e:
                self.results.append({
                    "error": str(e),
                    "timestamp": time.time()
                })

        # Save final results
        self._save_checkpoint("completed")
        return self._compute_summary()

    def _save_checkpoint(self, status):
        """Save results incrementally"""
        checkpoint = {
            "status": status,
            "model": self.model,
            "timestamp": datetime.utcnow().isoformat(),
            "results": self.results,
            "summary": self._compute_summary() if self.results else None
        }
        with open("/tmp/benchmark_checkpoint.json", "w") as f:
            json.dump(checkpoint, f, indent=2)

    def _compute_summary(self):
        if not self.results:
            return None

        valid = [r for r in self.results if "tokens" in r]
        errors = [r for r in self.results if "error" in r]

        total_tokens = sum(r["tokens"] for r in valid)
        duration = time.time() - self.start_time

        return {
            "total_requests": len(self.results),
            "successful_requests": len(valid),
            "error_count": len(errors),
            "total_tokens": total_tokens,
            "duration_seconds": duration,
            "tokens_per_second": total_tokens / duration if duration > 0 else 0
        }
```

## Result Collection Script

```bash
#!/bin/bash
# collect_results.sh - Run on local machine

SESSION_ID=$1
SSH_HOST=$2
SSH_PORT=$3
SSH_KEY=$4

# Use SSH cat redirect (works without SCP permissions)
ssh -i "$SSH_KEY" -p "$SSH_PORT" -o StrictHostKeyChecking=no \
    "root@$SSH_HOST" "cat /tmp/benchmark_checkpoint.json" \
    > "results/${SESSION_ID}_results.json"

# Validate JSON
if jq empty "results/${SESSION_ID}_results.json" 2>/dev/null; then
    echo "Results collected successfully"

    # Auto-import to database
    go run cmd/benchmark-loader/main.go \
        --file "results/${SESSION_ID}_results.json" \
        --session "$SESSION_ID"
else
    echo "ERROR: Invalid JSON received"
    exit 1
fi
```

## Subagent Permissions

When running benchmarks via subagents, pre-approve these commands:

```
# SSH operations (read-only)
ssh -i * -p * -o StrictHostKeyChecking=no root@* "cat /tmp/*"
ssh -i * -p * -o StrictHostKeyChecking=no root@* "nvidia-smi"
ssh -i * -p * -o StrictHostKeyChecking=no root@* "ollama *"

# Local file operations
cat results/*.json
jq * results/*.json
```

## Checklist for Running Benchmarks

### Pre-flight
- [ ] Verify provider credentials in `.env`
- [ ] Check target GPU availability: `GET /api/v1/inventory?gpu_name=RTX%205090`
- [ ] Create benchmark manifest entry with status=pending
- [ ] Ensure results directory exists

### Per-GPU
- [ ] Provision instance via API
- [ ] Wait for SSH ready (check session status)
- [ ] Verify nvidia-smi shows expected GPU
- [ ] Install Ollama if needed
- [ ] Pull target model
- [ ] Run benchmark with checkpoint saving
- [ ] Collect results via SSH cat
- [ ] Import results to database
- [ ] Update manifest with outcome
- [ ] Terminate instance

### Post-run
- [ ] Review manifest for failed tests
- [ ] Document failure reasons
- [ ] Update BENCHMARK_REPORT.md
- [ ] Archive raw results

## Debugging Failed Tests

### Instance won't provision
```bash
# Check recent session failures
curl localhost:8080/api/v1/sessions | jq '.[] | select(.status=="failed")'

# Check provider inventory
curl localhost:8080/api/v1/inventory?provider=tensordock&gpu_name=RTX%203090
```

### Benchmark didn't complete
```bash
# SSH to instance and check
ssh -i key.pem -p PORT root@HOST

# Check if benchmark is running
ps aux | grep python

# Check partial results
cat /tmp/benchmark_checkpoint.json

# Check Ollama status
ollama list
curl localhost:11434/api/tags
```

### Results not imported
```bash
# Check if JSON is valid
jq empty results/SESSION_results.json

# Manual import
sqlite3 data/gpu-shopper.db "INSERT INTO benchmarks ..."
```
