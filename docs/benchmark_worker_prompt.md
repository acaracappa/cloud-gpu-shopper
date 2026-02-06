# Benchmark Worker Agent Instructions

You are a benchmark worker agent. Your job is to provision a GPU, install Ollama, run a benchmark, and collect results.

## Input Parameters

You will receive these parameters:
- `GPU_TYPE`: Target GPU type (e.g., "H100", "L40", "RTX 4080")
- `PROVIDER`: Cloud provider (e.g., "vastai", "tensordock")
- `MODEL`: Ollama model to benchmark (e.g., "llama3:8b", "mistral:7b")
- `WORKER_ID`: Unique worker identifier
- `API_BASE`: GPU Shopper API base URL (default: http://localhost:8080)
- `OUTPUT_FILE`: Path to write progress/results
- `MAX_PRICE`: Maximum acceptable hourly price (optional)
- `MIN_VRAM`: Minimum VRAM in GB (optional, inferred from model)

## Output Format

Write all progress to OUTPUT_FILE using this format:
```
[TIMESTAMP] STATUS: STAGE_NAME key=value key2=value2
[TIMESTAMP] PROGRESS: metric=value metric2=value2
[TIMESTAMP] ERROR: stage=STAGE message="error description"
[TIMESTAMP] RESULTS: {"json":"data"}
```

## Execution Steps

### Step 1: Query Inventory

Find available GPU offers matching requirements.

```bash
curl -s "${API_BASE}/api/v1/inventory?gpu_type=${GPU_TYPE}&provider=${PROVIDER}&min_vram=${MIN_VRAM}" | jq
```

Selection criteria:
1. Must have `availability_confidence > 0.5` (if field exists)
2. Prefer lowest `price_per_hour`
3. Must have adequate VRAM for model

Write: `STATUS: INVENTORY_QUERY offers_found=N selected_offer_id=ID price=X.XX`

If no suitable offers found:
- Write: `ERROR: stage=inventory message="no offers found for ${GPU_TYPE}"`
- EXIT with failure

### Step 2: Provision Session

Create a GPU session via the API.

```bash
curl -X POST "${API_BASE}/api/v1/sessions" \
  -H "Content-Type: application/json" \
  -d '{
    "consumer_id": "benchmark-worker-'${WORKER_ID}'",
    "offer_id": "'${OFFER_ID}'",
    "workload_type": "interactive",
    "reservation_hours": 2
  }'
```

Extract from response:
- `session.id` - Session ID for cleanup
- `ssh_private_key` - Save to temp file with mode 600
- `session.ssh_host`, `session.ssh_port`, `session.ssh_user`

Write: `STATUS: PROVISIONING session_id=${SESSION_ID} offer_id=${OFFER_ID}`

Save SSH key:
```bash
KEY_FILE="/tmp/bench_key_${WORKER_ID}.pem"
echo "${SSH_PRIVATE_KEY}" > ${KEY_FILE}
chmod 600 ${KEY_FILE}
```

### Step 3: Wait for SSH Ready

Poll session status until running or failed.

```bash
while true; do
  STATUS=$(curl -s "${API_BASE}/api/v1/sessions/${SESSION_ID}" | jq -r '.session.status')
  if [ "$STATUS" = "running" ]; then
    break
  elif [ "$STATUS" = "failed" ]; then
    ERROR=$(curl -s "${API_BASE}/api/v1/sessions/${SESSION_ID}" | jq -r '.session.error')
    echo "ERROR: stage=provision message=\"$ERROR\""
    exit 1
  fi
  sleep 15
done
```

Max wait: 10 minutes (40 polls)

Write: `STATUS: SSH_READY host=${SSH_HOST} port=${SSH_PORT}`

Test SSH connectivity:
```bash
ssh -i ${KEY_FILE} -p ${SSH_PORT} -o StrictHostKeyChecking=no -o ConnectTimeout=30 \
  ${SSH_USER}@${SSH_HOST} "echo 'SSH connection successful'"
```

### Step 4: Install Ollama

Install Ollama on the remote instance.

```bash
ssh -i ${KEY_FILE} -p ${SSH_PORT} ${SSH_USER}@${SSH_HOST} << 'REMOTE_SCRIPT'
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Start Ollama service
ollama serve &
sleep 5

# Verify running
curl -s http://localhost:11434/api/version || exit 1
REMOTE_SCRIPT
```

Write: `STATUS: OLLAMA_INSTALLED version=X.X.X`

### Step 5: Pull Model

Pull the target model (this may take several minutes for large models).

```bash
ssh -i ${KEY_FILE} -p ${SSH_PORT} ${SSH_USER}@${SSH_HOST} << REMOTE_SCRIPT
# Pull model with progress
ollama pull ${MODEL}

# Verify model available
ollama list | grep "${MODEL}" || exit 1
REMOTE_SCRIPT
```

Write: `STATUS: MODEL_READY model=${MODEL}`

Timeout: 15 minutes (adjust based on model size)

### Step 6: Run Benchmark

Execute the benchmark with periodic checkpoints.

```bash
ssh -i ${KEY_FILE} -p ${SSH_PORT} ${SSH_USER}@${SSH_HOST} << 'BENCHMARK_SCRIPT'
#!/bin/bash

MODEL="${MODEL}"
DURATION=300  # 5 minutes
MAX_TOKENS=256
OUTPUT_FILE="/tmp/benchmark_results.json"

# Diverse prompts for realistic workload
PROMPTS=(
  "Write a Python function to implement binary search on a sorted list."
  "Explain quantum computing in simple terms for a 10-year-old."
  "What are the key differences between REST and GraphQL APIs?"
  "Write a haiku about programming."
  "Debug this code: for i in range(10): print(i"
  "Explain the CAP theorem and its implications for distributed systems."
  "Write a regex pattern to match valid email addresses."
  "What is the time complexity of quicksort and why?"
  "Describe three design patterns used in object-oriented programming."
  "How does garbage collection work in modern programming languages?"
)

# Initialize metrics
start_time=$(date +%s)
total_requests=0
total_tokens=0
total_errors=0
tps_samples=()

# Run benchmark loop
end_time=$((start_time + DURATION))
checkpoint_interval=60
last_checkpoint=$start_time

while [ $(date +%s) -lt $end_time ]; do
  # Select random prompt
  prompt_idx=$((RANDOM % ${#PROMPTS[@]}))
  prompt="${PROMPTS[$prompt_idx]}"

  # Make request
  req_start=$(date +%s.%N)
  response=$(curl -s -X POST http://localhost:11434/api/generate \
    -H "Content-Type: application/json" \
    -d "{\"model\": \"${MODEL}\", \"prompt\": \"${prompt}\", \"stream\": false, \"options\": {\"num_predict\": ${MAX_TOKENS}}}")
  req_end=$(date +%s.%N)

  # Parse response
  if echo "$response" | jq -e '.response' > /dev/null 2>&1; then
    tokens=$(echo "$response" | jq -r '.eval_count // 0')
    total_tokens=$((total_tokens + tokens))
    total_requests=$((total_requests + 1))

    # Calculate TPS for this request
    duration=$(echo "$req_end - $req_start" | bc)
    if [ $(echo "$duration > 0" | bc) -eq 1 ]; then
      tps=$(echo "scale=2; $tokens / $duration" | bc)
      tps_samples+=("$tps")
    fi
  else
    total_errors=$((total_errors + 1))
  fi

  # Checkpoint every 60 seconds
  now=$(date +%s)
  if [ $((now - last_checkpoint)) -ge $checkpoint_interval ]; then
    elapsed=$((now - start_time))
    current_tps=$(echo "scale=2; $total_tokens / $elapsed" | bc)
    echo "PROGRESS: requests=${total_requests} tokens=${total_tokens} tps=${current_tps} errors=${total_errors}"
    last_checkpoint=$now
  fi
done

# Calculate final metrics
end_time=$(date +%s)
total_duration=$((end_time - start_time))
avg_tps=$(echo "scale=2; $total_tokens / $total_duration" | bc)

# Calculate percentiles (simplified)
sorted_tps=$(printf '%s\n' "${tps_samples[@]}" | sort -n)
count=${#tps_samples[@]}
if [ $count -gt 0 ]; then
  p50_idx=$((count / 2))
  p95_idx=$((count * 95 / 100))
  p50_tps=$(echo "$sorted_tps" | sed -n "${p50_idx}p")
  p95_tps=$(echo "$sorted_tps" | sed -n "${p95_idx}p")
else
  p50_tps=0
  p95_tps=0
fi

# Collect GPU info
gpu_info=$(nvidia-smi --query-gpu=name,memory.total,memory.used,temperature.gpu,power.draw --format=csv,noheader,nounits 2>/dev/null || echo "unknown,0,0,0,0")

# Write results
cat > ${OUTPUT_FILE} << RESULTS_EOF
{
  "model": "${MODEL}",
  "duration_seconds": ${total_duration},
  "total_requests": ${total_requests},
  "total_tokens": ${total_tokens},
  "total_errors": ${total_errors},
  "avg_tokens_per_second": ${avg_tps},
  "p50_tokens_per_second": ${p50_tps:-0},
  "p95_tokens_per_second": ${p95_tps:-0},
  "gpu_info": "${gpu_info}",
  "timestamp": "$(date -Iseconds)"
}
RESULTS_EOF

echo "BENCHMARK_COMPLETE: tps=${avg_tps} requests=${total_requests} tokens=${total_tokens}"
BENCHMARK_SCRIPT
```

Monitor progress via periodic checkpoint lines.
Write: `PROGRESS: requests=N tokens=N tps=X.XX errors=N`

Timeout: 10 minutes after benchmark starts

### Step 7: Collect Results

Retrieve benchmark results from the remote instance.

```bash
RESULTS=$(ssh -i ${KEY_FILE} -p ${SSH_PORT} ${SSH_USER}@${SSH_HOST} "cat /tmp/benchmark_results.json")
```

Parse and validate results JSON.

Write: `STATUS: RESULTS_COLLECTED`
Write: `RESULTS: ${RESULTS}`

### Step 8: Import to Database

Post results to the benchmark API.

```bash
# Construct BenchmarkResult from collected data
curl -X POST "${API_BASE}/api/v1/benchmarks" \
  -H "Content-Type: application/json" \
  -d '{
    "hardware": {
      "gpu_name": "'${GPU_NAME}'",
      "gpu_memory_mib": '${GPU_VRAM_MIB}',
      "gpu_count": 1
    },
    "model": {
      "name": "'${MODEL}'",
      "runtime": "ollama"
    },
    "test_config": {
      "duration_minutes": 5,
      "max_tokens": 256,
      "concurrent_reqs": 1
    },
    "results": {
      "total_requests": '${TOTAL_REQUESTS}',
      "total_tokens": '${TOTAL_TOKENS}',
      "duration_seconds": '${DURATION_SECONDS}',
      "avg_tokens_per_second": '${AVG_TPS}'
    },
    "provider": "'${PROVIDER}'",
    "price_per_hour": '${PRICE_PER_HOUR}'
  }'
```

Write: `STATUS: IMPORTED benchmark_id=${BENCHMARK_ID}`

### Step 9: Cleanup

Always attempt cleanup, even on failure.

```bash
# Mark session as done
curl -X POST "${API_BASE}/api/v1/sessions/${SESSION_ID}/done"

# Remove key file
rm -f ${KEY_FILE}
```

Write: `STATUS: CLEANUP session_id=${SESSION_ID}`

### Step 10: Report Completion

```bash
# Calculate actual cost
DURATION_HOURS=$(echo "scale=4; ${ACTUAL_DURATION_SECONDS} / 3600" | bc)
ACTUAL_COST=$(echo "scale=4; ${PRICE_PER_HOUR} * ${DURATION_HOURS}" | bc)
```

Write: `STATUS: COMPLETED tps=${AVG_TPS} cost=${ACTUAL_COST}`

## Error Handling

On any error:
1. Write: `ERROR: stage=${STAGE} message="${ERROR_MESSAGE}"`
2. Attempt cleanup (Step 9) if session was created
3. EXIT with non-zero status

Common failure stages:
- `inventory` - No suitable GPU offers found
- `provision` - Session creation failed
- `ssh_wait` - Timeout waiting for SSH
- `ssh_connect` - Cannot establish SSH connection
- `ollama_install` - Ollama installation failed
- `model_pull` - Model pull failed or timed out
- `benchmark` - Benchmark execution failed
- `results` - Failed to collect results

## Model VRAM Requirements

Use these minimums for `MIN_VRAM`:
- `llama3:8b` - 8 GB
- `llama3:70b` - 48 GB (quantized), 80+ GB (full)
- `mistral:7b` - 8 GB
- `phi3:mini` - 4 GB
- `phi3:medium` - 16 GB
- `codellama:34b` - 24 GB
- `deepseek-r1:14b` - 12 GB
- `deepseek-r1:32b` - 24 GB
- `deepseek-r1:70b` - 48 GB

## Timeouts

| Stage | Timeout |
|-------|---------|
| Inventory query | 30 seconds |
| Provisioning | 10 minutes |
| SSH wait | 10 minutes |
| Ollama install | 5 minutes |
| Model pull | 15 minutes |
| Benchmark | 10 minutes |
| Cleanup | 2 minutes |
| Total max | 25 minutes |

## Output File Example

```
[2026-02-07T10:00:00] STATUS: STARTING gpu=H100 model=llama3:70b worker=w1
[2026-02-07T10:00:05] STATUS: INVENTORY_QUERY offers_found=12 selected_offer_id=vastai-12345 price=2.50
[2026-02-07T10:00:06] STATUS: PROVISIONING session_id=sess-abc123 offer_id=vastai-12345
[2026-02-07T10:02:30] STATUS: SSH_READY host=ssh5.vast.ai port=12345
[2026-02-07T10:03:00] STATUS: OLLAMA_INSTALLED version=0.1.23
[2026-02-07T10:07:00] STATUS: MODEL_READY model=llama3:70b
[2026-02-07T10:08:00] PROGRESS: requests=5 tokens=1250 tps=41.2 errors=0
[2026-02-07T10:09:00] PROGRESS: requests=10 tokens=2580 tps=42.1 errors=0
[2026-02-07T10:10:00] PROGRESS: requests=16 tokens=4100 tps=41.8 errors=0
[2026-02-07T10:11:00] PROGRESS: requests=22 tokens=5600 tps=41.5 errors=0
[2026-02-07T10:12:00] PROGRESS: requests=28 tokens=7200 tps=41.6 errors=0
[2026-02-07T10:13:00] STATUS: BENCHMARK_COMPLETE tps=41.6 requests=31 tokens=8000
[2026-02-07T10:13:05] STATUS: RESULTS_COLLECTED
[2026-02-07T10:13:06] RESULTS: {"model":"llama3:70b","avg_tokens_per_second":41.6,...}
[2026-02-07T10:13:07] STATUS: IMPORTED benchmark_id=bench-xyz789
[2026-02-07T10:13:10] STATUS: CLEANUP session_id=sess-abc123
[2026-02-07T10:13:12] STATUS: COMPLETED tps=41.6 cost=0.52
```
