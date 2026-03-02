#!/bin/bash
# gpu-benchmark.sh — Self-contained GPU benchmark script for Ollama instances.
# Zero dependencies beyond curl, jq, nvidia-smi.
#
# Usage: ./gpu-benchmark.sh MODEL SESSION_ID PRICE_PER_HOUR PROVIDER LOCATION
#
# Results are written to /tmp/benchmark_result.json and a marker file
# /tmp/benchmark_complete is created when finished. The server collects
# results via SSH pull.
set -euo pipefail

# ── Args ────────────────────────────────────────────────────────────────────
MODEL="${1:?Usage: gpu-benchmark.sh MODEL SESSION_ID PRICE_PER_HOUR PROVIDER LOCATION}"
SESSION_ID="${2:?missing SESSION_ID}"
PRICE_PER_HOUR="${3:?missing PRICE_PER_HOUR}"
PROVIDER="${4:?missing PROVIDER}"
LOCATION="${5:-unknown}"

OLLAMA_URL="http://localhost:11434"
RESULT_FILE="/tmp/benchmark_result.json"
RESULTS_JSONL="/tmp/benchmark_results.jsonl"
GPU_STATS_FILE="/tmp/benchmark_gpu_stats.csv"
MARKER_FILE="/tmp/benchmark_complete"
THROUGHPUT_DURATION=300  # 5 minutes

# Remove stale marker
rm -f "$MARKER_FILE" "$RESULT_FILE" "$RESULTS_JSONL" "$GPU_STATS_FILE"

log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }

die() { log "FATAL: $*"; exit 1; }

# ── Structured test prompts ─────────────────────────────────────────────────
# Each prompt: ID|CATEGORY|PROMPT|EXPECTED_CONTAINS(comma-sep, empty=none)|MAX_TOKENS
# Expected contains uses OR logic: match if ANY expected substring is found.
QUALITY_PROMPTS=(
  'math_simple|reasoning|What is 15 + 27? Answer with just the number.|42|20'
  'code_simple|coding|Write a Python function that returns the square of a number.|def,return|100'
  'knowledge|knowledge|What is the capital of France? Answer in one word.|Paris|20'
  'creative|creative|Write a haiku about programming.||100'
  'instruction|instruction|List 3 primary colors, separated by commas.|red,blue|50'
)

THROUGHPUT_PROMPT="Explain the concept of machine learning in detail. Cover supervised learning, unsupervised learning, and reinforcement learning. Discuss common algorithms, their applications, and the mathematical foundations behind gradient descent and backpropagation."
THROUGHPUT_MAX_TOKENS=500

# ── Step 1: Start Ollama if not running, then wait ──────────────────────────
if ! curl -sf "$OLLAMA_URL/api/tags" >/dev/null 2>&1; then
  log "Ollama not running, attempting to start it..."
  # Try common install locations
  OLLAMA_BIN=""
  for candidate in /usr/local/bin/ollama /usr/bin/ollama ollama; do
    if command -v "$candidate" >/dev/null 2>&1; then
      OLLAMA_BIN="$candidate"
      break
    fi
  done
  if [ -z "$OLLAMA_BIN" ]; then
    log "Ollama not found, installing..."
    curl -fsSL https://ollama.com/install.sh | sh 2>/dev/null || true
    OLLAMA_BIN="ollama"
  fi
  log "Starting Ollama server ($OLLAMA_BIN)..."
  nohup "$OLLAMA_BIN" serve > /tmp/ollama.log 2>&1 &
  sleep 3
fi

log "Waiting for Ollama at $OLLAMA_URL ..."
OLLAMA_TIMEOUT=300
OLLAMA_START=$(date +%s)
while true; do
  if curl -sf "$OLLAMA_URL/api/tags" >/dev/null 2>&1; then
    log "Ollama is ready."
    break
  fi
  ELAPSED=$(( $(date +%s) - OLLAMA_START ))
  if [ "$ELAPSED" -ge "$OLLAMA_TIMEOUT" ]; then
    die "Ollama not ready after ${OLLAMA_TIMEOUT}s"
  fi
  sleep 5
done

# ── Step 2: Pull model if not present ───────────────────────────────────────
log "Checking if model '$MODEL' is available..."
if ! curl -sf "$OLLAMA_URL/api/tags" | jq -e ".models[] | select(.name == \"$MODEL\")" >/dev/null 2>&1; then
  log "Model not found locally, pulling '$MODEL'..."
  PULL_START=$(date +%s)
  curl -sf -X POST "$OLLAMA_URL/api/pull" -d "$(jq -n --arg name "$MODEL" '{name:$name}')" >/dev/null 2>&1 &
  PULL_PID=$!
  PULL_TIMEOUT=900  # 15 min
  while kill -0 "$PULL_PID" 2>/dev/null; do
    ELAPSED=$(( $(date +%s) - PULL_START ))
    if [ "$ELAPSED" -ge "$PULL_TIMEOUT" ]; then
      kill "$PULL_PID" 2>/dev/null || true
      die "Model pull timed out after ${PULL_TIMEOUT}s"
    fi
    sleep 10
    log "  Still pulling... (${ELAPSED}s)"
  done
  wait "$PULL_PID" || die "Model pull failed"
  PULL_ELAPSED=$(( $(date +%s) - PULL_START ))
  log "Model pulled in ${PULL_ELAPSED}s"
else
  log "Model '$MODEL' already available."
fi

# ── Step 3: Collect hardware info ───────────────────────────────────────────
log "Collecting hardware info..."
GPU_NAME="unknown"
GPU_MEMORY_MIB=0
GPU_COUNT=0
DRIVER_VERSION="unknown"
CUDA_VERSION="unknown"
CPU_MODEL="unknown"
CPU_CORES=0
RAM_GIB=0

if command -v nvidia-smi >/dev/null 2>&1; then
  GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs) || true
  GPU_MEMORY_MIB=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs) || true
  GPU_COUNT=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | wc -l | xargs) || true
  DRIVER_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader,nounits 2>/dev/null | head -1 | xargs) || true
  CUDA_VERSION=$(nvidia-smi 2>/dev/null | awk '/CUDA Version:/{for(i=1;i<=NF;i++)if($i=="Version:")print $(i+1)}' || echo "unknown")
fi

if [ -f /proc/cpuinfo ]; then
  CPU_MODEL=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs) || true
  CPU_CORES=$(grep -c '^processor' /proc/cpuinfo 2>/dev/null) || true
fi

if command -v free >/dev/null 2>&1; then
  RAM_GIB=$(free -g 2>/dev/null | awk '/^Mem:/{print $2}') || true
fi

log "GPU: $GPU_NAME (${GPU_MEMORY_MIB}MiB x${GPU_COUNT}), Driver: $DRIVER_VERSION, CUDA: $CUDA_VERSION"
log "CPU: $CPU_MODEL (${CPU_CORES} cores), RAM: ${RAM_GIB}GiB"

# ── Helper: Make a request and measure TTFT ─────────────────────────────────
# Returns JSON with response text, tokens, tps, ttft_ms, latency_ms, error
ollama_request() {
  local prompt="$1"
  local max_tokens="$2"

  local tmp_file
  tmp_file=$(mktemp)

  # Use curl -w to capture timing info
  local http_code
  # Build JSON payload safely using jq to avoid injection via MODEL or prompt
  local payload
  payload=$(jq -n --arg model "$MODEL" --arg prompt "$prompt" --argjson max_tokens "$max_tokens" \
    '{model:$model,prompt:$prompt,stream:false,options:{num_predict:$max_tokens}}')

  http_code=$(curl -s -o "$tmp_file" -w '%{http_code}\n%{time_starttransfer}\n%{time_total}' \
    -X POST "$OLLAMA_URL/api/generate" \
    -H 'Content-Type: application/json' \
    -d "$payload" \
    2>/dev/null) || true

  # Parse timing from curl output (last 3 lines: http_code, time_starttransfer, time_total)
  local status ttft_s total_s
  status=$(echo "$http_code" | head -1)
  ttft_s=$(echo "$http_code" | sed -n '2p')
  total_s=$(echo "$http_code" | sed -n '3p')

  if [ "$status" != "200" ] || [ ! -s "$tmp_file" ]; then
    rm -f "$tmp_file"
    echo '{"error":true,"error_msg":"http_status_'"${status}"'","tokens":0,"tps":0,"ttft_ms":0,"latency_ms":0,"response":""}'
    return
  fi

  # Parse the Ollama response
  local response_text eval_count eval_duration_ns total_duration_ns
  response_text=$(jq -r '.response // ""' "$tmp_file" 2>/dev/null) || true
  eval_count=$(jq -r '.eval_count // 0' "$tmp_file" 2>/dev/null) || true
  eval_duration_ns=$(jq -r '.eval_duration // 0' "$tmp_file" 2>/dev/null) || true
  total_duration_ns=$(jq -r '.total_duration // 0' "$tmp_file" 2>/dev/null) || true

  rm -f "$tmp_file"

  # Calculate TPS from eval_duration (generation time only)
  local tps=0
  if [ "$eval_duration_ns" -gt 0 ] 2>/dev/null; then
    tps=$(echo "$eval_count $eval_duration_ns" | awk '{printf "%.2f", $1 / ($2 / 1000000000)}')
  fi

  # TTFT from curl timing (seconds -> ms)
  local ttft_ms=0
  if [ -n "$ttft_s" ]; then
    ttft_ms=$(echo "$ttft_s" | awk '{printf "%.1f", $1 * 1000}')
  fi

  # Total latency
  local latency_ms=0
  if [ -n "$total_s" ]; then
    latency_ms=$(echo "$total_s" | awk '{printf "%.1f", $1 * 1000}')
  fi

  # Output as JSON
  jq -n \
    --arg response "$response_text" \
    --argjson tokens "${eval_count:-0}" \
    --argjson tps "${tps:-0}" \
    --argjson ttft_ms "${ttft_ms:-0}" \
    --argjson latency_ms "${latency_ms:-0}" \
    '{error:false,response:$response,tokens:$tokens,tps:$tps,ttft_ms:$ttft_ms,latency_ms:$latency_ms}'
}

# ── Step 4: Warmup ──────────────────────────────────────────────────────────
log "Running 2 warmup requests..."
for i in 1 2; do
  ollama_request "Hello, how are you?" 20 >/dev/null
  log "  Warmup $i complete"
done

# ── Step 5: Quality prompts ─────────────────────────────────────────────────
log "Running quality prompts..."
QUALITY_RESULTS='[]'
PROMPTS_WITH_EXPECTED=0
PROMPTS_MATCHING=0
ALL_TTFT_MS='[]'

for entry in "${QUALITY_PROMPTS[@]}"; do
  IFS='|' read -r prompt_id category prompt expected_csv max_tokens <<< "$entry"
  log "  Quality test: $prompt_id ($category)"

  result=$(ollama_request "$prompt" "$max_tokens")
  is_error=$(echo "$result" | jq -r '.error')

  if [ "$is_error" = "true" ]; then
    log "    ERROR: $(echo "$result" | jq -r '.error_msg')"
    QUALITY_RESULTS=$(echo "$QUALITY_RESULTS" | jq \
      --arg id "$prompt_id" --arg cat "$category" \
      '. + [{"id":$id,"category":$cat,"error":true,"matched":false}]')
    continue
  fi

  response_text=$(echo "$result" | jq -r '.response')
  ttft_ms=$(echo "$result" | jq -r '.ttft_ms')
  tps=$(echo "$result" | jq -r '.tps')

  ALL_TTFT_MS=$(echo "$ALL_TTFT_MS" | jq --argjson t "$ttft_ms" '. + [$t]')

  # Match-rate validation (case-insensitive substring, OR logic)
  matched=false
  if [ -n "$expected_csv" ]; then
    PROMPTS_WITH_EXPECTED=$((PROMPTS_WITH_EXPECTED + 1))
    response_lower=$(echo "$response_text" | tr '[:upper:]' '[:lower:]')
    IFS=',' read -ra expected_list <<< "$expected_csv"
    for exp in "${expected_list[@]}"; do
      exp_lower=$(echo "$exp" | tr '[:upper:]' '[:lower:]')
      if echo "$response_lower" | grep -qF "$exp_lower"; then
        matched=true
        PROMPTS_MATCHING=$((PROMPTS_MATCHING + 1))
        break
      fi
    done
    log "    Match: $matched (expected any of: $expected_csv)"
  fi

  QUALITY_RESULTS=$(echo "$QUALITY_RESULTS" | jq \
    --arg id "$prompt_id" --arg cat "$category" \
    --argjson matched "$matched" --argjson ttft "$ttft_ms" --argjson tps "$tps" \
    '. + [{"id":$id,"category":$cat,"matched":$matched,"ttft_ms":$ttft,"tps":$tps}]')
done

# Calculate match rate
MATCH_RATE=0
if [ "$PROMPTS_WITH_EXPECTED" -gt 0 ]; then
  MATCH_RATE=$(echo "$PROMPTS_MATCHING $PROMPTS_WITH_EXPECTED" | awk '{printf "%.4f", $1 / $2}')
fi
log "Match rate: $PROMPTS_MATCHING/$PROMPTS_WITH_EXPECTED = $MATCH_RATE"

# ── Step 6: Start GPU stats collection in background ────────────────────────
log "Starting GPU stats collection..."
(
  while true; do
    if command -v nvidia-smi >/dev/null 2>&1; then
      nvidia-smi --query-gpu=utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw \
        --format=csv,noheader,nounits 2>/dev/null | while read -r line; do
          echo "$(date +%s),$line" >> "$GPU_STATS_FILE"
        done
    fi
    sleep 5
  done
) &
GPU_STATS_PID=$!

# ── Step 7: Throughput test ─────────────────────────────────────────────────
log "Running throughput test for ${THROUGHPUT_DURATION}s..."
THROUGHPUT_START=$(date +%s)
REQUEST_NUM=0
TOTAL_TOKENS=0
TOTAL_PROMPT_TOKENS=0
TOTAL_ERRORS=0
ALL_TPS='[]'
ALL_LATENCY='[]'

while true; do
  ELAPSED=$(( $(date +%s) - THROUGHPUT_START ))
  if [ "$ELAPSED" -ge "$THROUGHPUT_DURATION" ]; then
    break
  fi

  REQUEST_NUM=$((REQUEST_NUM + 1))
  result=$(ollama_request "$THROUGHPUT_PROMPT" "$THROUGHPUT_MAX_TOKENS")
  is_error=$(echo "$result" | jq -r '.error')

  if [ "$is_error" = "true" ]; then
    TOTAL_ERRORS=$((TOTAL_ERRORS + 1))
    echo "{\"t\":$(date +%s),\"n\":$REQUEST_NUM,\"tok\":0,\"tps\":0,\"err\":true,\"error_msg\":$(echo "$result" | jq '.error_msg')}" >> "$RESULTS_JSONL"
    continue
  fi

  tokens=$(echo "$result" | jq -r '.tokens')
  tps=$(echo "$result" | jq -r '.tps')
  ttft_ms=$(echo "$result" | jq -r '.ttft_ms')
  latency_ms=$(echo "$result" | jq -r '.latency_ms')

  TOTAL_TOKENS=$((TOTAL_TOKENS + tokens))
  ALL_TPS=$(echo "$ALL_TPS" | jq --argjson t "$tps" '. + [$t]')
  ALL_LATENCY=$(echo "$ALL_LATENCY" | jq --argjson t "$latency_ms" '. + [$t]')
  ALL_TTFT_MS=$(echo "$ALL_TTFT_MS" | jq --argjson t "$ttft_ms" '. + [$t]')

  echo "{\"t\":$(date +%s),\"n\":$REQUEST_NUM,\"tok\":$tokens,\"tps\":$tps,\"dur\":$(echo "$latency_ms" | awk '{printf "%.3f", $1/1000}')}" >> "$RESULTS_JSONL"
  log "  Request $REQUEST_NUM: ${tokens} tokens, ${tps} tps, ttft=${ttft_ms}ms"
done

THROUGHPUT_END=$(date +%s)
DURATION_SECONDS=$((THROUGHPUT_END - THROUGHPUT_START))

# Stop GPU stats collection
kill "$GPU_STATS_PID" 2>/dev/null || true
wait "$GPU_STATS_PID" 2>/dev/null || true

# ── Step 8: Compute statistics ──────────────────────────────────────────────
log "Computing statistics..."

# TPS stats
compute_stats() {
  local arr="$1"
  echo "$arr" | jq -r '
    if length == 0 then
      {"avg":0,"min":0,"max":0,"p50":0,"p95":0,"p99":0}
    else
      sort | {
        avg: (add / length),
        min: first,
        max: last,
        p50: .[((length * 0.5) | floor)],
        p95: .[((length * 0.95) | floor)],
        p99: .[((length * 0.99) | floor)]
      }
    end
  '
}

TPS_STATS=$(compute_stats "$ALL_TPS")
LATENCY_STATS=$(compute_stats "$ALL_LATENCY")
TTFT_STATS=$(compute_stats "$ALL_TTFT_MS")

# Error rate
ERROR_RATE=0
if [ "$REQUEST_NUM" -gt 0 ]; then
  ERROR_RATE=$(echo "$TOTAL_ERRORS $REQUEST_NUM" | awk '{printf "%.4f", $1 / $2}')
fi

# Requests per minute
RPM=0
if [ "$DURATION_SECONDS" -gt 0 ]; then
  RPM=$(echo "$REQUEST_NUM $DURATION_SECONDS" | awk '{printf "%.1f", $1 / $2 * 60}')
fi

# Average tokens per request
AVG_TOKENS_PER_REQ=0
SUCCESSFUL_REQS=$((REQUEST_NUM - TOTAL_ERRORS))
if [ "$SUCCESSFUL_REQS" -gt 0 ]; then
  AVG_TOKENS_PER_REQ=$(echo "$TOTAL_TOKENS $SUCCESSFUL_REQS" | awk '{printf "%.1f", $1 / $2}')
fi

# ── Step 9: Compute GPU stats ──────────────────────────────────────────────
GPU_AVG_UTIL=0
GPU_MAX_UTIL=0
GPU_AVG_MEM=0
GPU_MAX_MEM=0
GPU_AVG_TEMP=0
GPU_MAX_TEMP=0
GPU_AVG_POWER=0
GPU_MAX_POWER=0

if [ -f "$GPU_STATS_FILE" ] && [ -s "$GPU_STATS_FILE" ]; then
  GPU_AVG_UTIL=$(awk -F',' '{sum+=$2;n++} END{if(n>0)printf "%.1f",sum/n;else print 0}' "$GPU_STATS_FILE")
  GPU_MAX_UTIL=$(awk -F',' 'BEGIN{max=0}{if($2>max)max=$2}END{printf "%.1f",max}' "$GPU_STATS_FILE")
  GPU_AVG_MEM=$(awk -F',' '{sum+=$3;n++} END{if(n>0)printf "%d",sum/n;else print 0}' "$GPU_STATS_FILE")
  GPU_MAX_MEM=$(awk -F',' 'BEGIN{max=0}{if($3>max)max=$3}END{printf "%d",max}' "$GPU_STATS_FILE")
  GPU_AVG_TEMP=$(awk -F',' '{sum+=$5;n++} END{if(n>0)printf "%.1f",sum/n;else print 0}' "$GPU_STATS_FILE")
  GPU_MAX_TEMP=$(awk -F',' 'BEGIN{max=0}{if($5>max)max=$5}END{printf "%.1f",max}' "$GPU_STATS_FILE")
  GPU_AVG_POWER=$(awk -F',' '{sum+=$6;n++} END{if(n>0)printf "%.1f",sum/n;else print 0}' "$GPU_STATS_FILE")
  GPU_MAX_POWER=$(awk -F',' 'BEGIN{max=0}{if($6>max)max=$6}END{printf "%.1f",max}' "$GPU_STATS_FILE")
fi

# ── Step 10: Get model info from Ollama ─────────────────────────────────────
MODEL_FAMILY=$(echo "$MODEL" | cut -d: -f1)
MODEL_TAG=$(echo "$MODEL" | cut -d: -f2-)
PARAMETER_COUNT=""
QUANTIZATION=""
MODEL_SIZE_GB=0

# Try to get model details from Ollama API
MODEL_INFO=$(curl -sf "$OLLAMA_URL/api/show" -d "$(jq -n --arg name "$MODEL" '{name:$name}')" 2>/dev/null) || true
if [ -n "$MODEL_INFO" ]; then
  MODEL_SIZE_BYTES=$(echo "$MODEL_INFO" | jq -r '.size // 0' 2>/dev/null) || true
  if [ "$MODEL_SIZE_BYTES" -gt 0 ] 2>/dev/null; then
    MODEL_SIZE_GB=$(echo "$MODEL_SIZE_BYTES" | awk '{printf "%.2f", $1 / 1073741824}')
  fi
  QUANTIZATION=$(echo "$MODEL_INFO" | jq -r '.details.quantization_level // ""' 2>/dev/null) || true
  PARAMETER_COUNT=$(echo "$MODEL_INFO" | jq -r '.details.parameter_size // ""' 2>/dev/null) || true
fi

# Get Ollama version
RUNTIME_VERSION=$(curl -sf "$OLLAMA_URL/api/version" 2>/dev/null | jq -r '.version // "unknown"') || RUNTIME_VERSION="unknown"

# ── Step 11: Build result JSON ──────────────────────────────────────────────
log "Building result JSON..."

jq -n \
  --arg session_id "$SESSION_ID" \
  --arg gpu_name "$GPU_NAME" \
  --argjson gpu_memory_mib "${GPU_MEMORY_MIB:-0}" \
  --argjson gpu_count "${GPU_COUNT:-0}" \
  --arg driver_version "$DRIVER_VERSION" \
  --arg cuda_version "$CUDA_VERSION" \
  --arg cpu_model "$CPU_MODEL" \
  --argjson cpu_cores "${CPU_CORES:-0}" \
  --argjson ram_gib "${RAM_GIB:-0}" \
  --arg model_name "$MODEL" \
  --arg model_family "$MODEL_FAMILY" \
  --arg parameter_count "$PARAMETER_COUNT" \
  --arg quantization "$QUANTIZATION" \
  --argjson model_size_gb "${MODEL_SIZE_GB:-0}" \
  --arg runtime "ollama" \
  --arg runtime_version "$RUNTIME_VERSION" \
  --argjson duration_minutes "$((THROUGHPUT_DURATION / 60))" \
  --argjson max_tokens "$THROUGHPUT_MAX_TOKENS" \
  --argjson total_requests "$REQUEST_NUM" \
  --argjson total_tokens "$TOTAL_TOKENS" \
  --argjson total_errors "$TOTAL_ERRORS" \
  --argjson duration_seconds "$DURATION_SECONDS" \
  --argjson tps_stats "$TPS_STATS" \
  --argjson latency_stats "$LATENCY_STATS" \
  --argjson ttft_stats "$TTFT_STATS" \
  --argjson rpm "${RPM:-0}" \
  --argjson avg_tokens_per_req "${AVG_TOKENS_PER_REQ:-0}" \
  --argjson error_rate "${ERROR_RATE:-0}" \
  --argjson match_rate "${MATCH_RATE:-0}" \
  --argjson prompts_with_expected "$PROMPTS_WITH_EXPECTED" \
  --argjson prompts_matching "$PROMPTS_MATCHING" \
  --argjson gpu_avg_util "${GPU_AVG_UTIL:-0}" \
  --argjson gpu_max_util "${GPU_MAX_UTIL:-0}" \
  --argjson gpu_avg_mem "${GPU_AVG_MEM:-0}" \
  --argjson gpu_max_mem "${GPU_MAX_MEM:-0}" \
  --argjson gpu_avg_temp "${GPU_AVG_TEMP:-0}" \
  --argjson gpu_max_temp "${GPU_MAX_TEMP:-0}" \
  --argjson gpu_avg_power "${GPU_AVG_POWER:-0}" \
  --argjson gpu_max_power "${GPU_MAX_POWER:-0}" \
  --arg provider "$PROVIDER" \
  --arg location "$LOCATION" \
  --argjson price_per_hour "$PRICE_PER_HOUR" \
  --argjson quality_results "$QUALITY_RESULTS" \
  '{
    timestamp: (now | strftime("%Y-%m-%dT%H:%M:%SZ")),
    hardware: {
      gpu_name: $gpu_name,
      gpu_memory_mib: $gpu_memory_mib,
      gpu_count: $gpu_count,
      driver_version: $driver_version,
      cuda_version: $cuda_version,
      cpu_model: $cpu_model,
      cpu_cores: $cpu_cores,
      ram_gib: $ram_gib
    },
    model: {
      name: $model_name,
      family: $model_family,
      parameter_count: $parameter_count,
      quantization: $quantization,
      size_gb: $model_size_gb,
      runtime: $runtime,
      runtime_version: $runtime_version
    },
    test_config: {
      duration_minutes: $duration_minutes,
      max_tokens: $max_tokens,
      prompt_types: ["reasoning","coding","knowledge","creative","instruction","throughput"],
      concurrent_reqs: 1,
      warmup_requests: 2
    },
    results: {
      total_requests: $total_requests,
      total_tokens: $total_tokens,
      total_prompt_tokens: 0,
      total_errors: $total_errors,
      duration_seconds: $duration_seconds,
      avg_tokens_per_second: $tps_stats.avg,
      min_tokens_per_second: $tps_stats.min,
      max_tokens_per_second: $tps_stats.max,
      p50_tokens_per_second: $tps_stats.p50,
      p95_tokens_per_second: $tps_stats.p95,
      p99_tokens_per_second: $tps_stats.p99,
      avg_latency_ms: $latency_stats.avg,
      min_latency_ms: $latency_stats.min,
      max_latency_ms: $latency_stats.max,
      p50_latency_ms: $latency_stats.p50,
      p95_latency_ms: $latency_stats.p95,
      p99_latency_ms: $latency_stats.p99,
      requests_per_minute: $rpm,
      avg_tokens_per_request: $avg_tokens_per_req,
      error_rate: $error_rate,
      avg_ttft_ms: $ttft_stats.avg,
      p50_ttft_ms: $ttft_stats.p50,
      p95_ttft_ms: $ttft_stats.p95,
      match_rate: $match_rate,
      prompts_with_expected: $prompts_with_expected,
      prompts_matching: $prompts_matching
    },
    gpu_stats: {
      avg_utilization_pct: $gpu_avg_util,
      max_utilization_pct: $gpu_max_util,
      avg_memory_used_mib: $gpu_avg_mem,
      max_memory_used_mib: $gpu_max_mem,
      avg_temperature_c: $gpu_avg_temp,
      max_temperature_c: $gpu_max_temp,
      avg_power_draw_w: $gpu_avg_power,
      max_power_draw_w: $gpu_max_power
    },
    provider: $provider,
    location: $location,
    price_per_hour: $price_per_hour,
    quality_results: $quality_results,
    session_id: $session_id
  }' > "$RESULT_FILE"

# ── Step 12: Write completion marker ────────────────────────────────────────
log "Benchmark complete. Results in $RESULT_FILE"
echo "done" > "$MARKER_FILE"
log "Marker written to $MARKER_FILE"
