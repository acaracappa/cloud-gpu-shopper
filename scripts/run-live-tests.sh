#!/bin/bash
set -e

echo "═══════════════════════════════════════════════════════════════════════════"
echo "  LIVE TEST SUITE - Real GPU Servers (Multi-Provider)"
echo "  Budget: \$5.00 total | Max Runtime: 90 minutes"
echo "═══════════════════════════════════════════════════════════════════════════"

# Check for at least one provider API key
if [ -z "$VASTAI_API_KEY" ] && [ -z "$TENSORDOCK_API_KEY" ]; then
    echo "ERROR: No provider API keys set"
    echo "Set at least one of:"
    echo "  - VASTAI_API_KEY"
    echo "  - TENSORDOCK_API_KEY (also needs TENSORDOCK_ORG_ID)"
    exit 1
fi

# Show enabled providers
echo ""
echo "Provider Status:"
if [ -n "$VASTAI_API_KEY" ]; then
    echo "  ✓ Vast.ai: Enabled"
    # Check balance
    BALANCE=$(curl -s -H "Authorization: Bearer $VASTAI_API_KEY" \
        "https://console.vast.ai/api/v0/users/current/" 2>/dev/null | jq -r '.balance // "unknown"')
    echo "    Balance: \$$BALANCE"
else
    echo "  ✗ Vast.ai: Not configured"
fi

if [ -n "$TENSORDOCK_API_KEY" ] && [ -n "$TENSORDOCK_ORG_ID" ]; then
    echo "  ✓ TensorDock: Enabled"
    # Note: TensorDock doesn't have a simple balance check endpoint
else
    echo "  ✗ TensorDock: Not configured"
fi
echo ""

# Check server URL
SHOPPER_URL="${SHOPPER_URL:-http://localhost:8080}"
echo "Server URL: $SHOPPER_URL"

# Start server if not running
echo "Checking server health..."
if ! curl -s "$SHOPPER_URL/health" > /dev/null 2>&1; then
    echo "Server not running. Starting..."

    # Create data directory
    mkdir -p ./data

    # Start server in background
    DATABASE_PATH=./data/live-test.db go run ./cmd/server &
    SERVER_PID=$!

    # Wait for server to be ready
    echo "Waiting for server to start..."
    for i in {1..30}; do
        if curl -s "$SHOPPER_URL/health" > /dev/null 2>&1; then
            echo "Server ready!"
            break
        fi
        sleep 1
    done

    if ! curl -s "$SHOPPER_URL/health" > /dev/null 2>&1; then
        echo "ERROR: Server failed to start"
        kill $SERVER_PID 2>/dev/null || true
        exit 1
    fi

    # Cleanup function
    cleanup() {
        echo ""
        echo "Cleaning up..."
        kill $SERVER_PID 2>/dev/null || true

        # Force cleanup all test sessions
        echo "Destroying any remaining test sessions..."
        curl -s -X GET "$SHOPPER_URL/api/v1/sessions" 2>/dev/null | \
            jq -r '.sessions[]? | select(.consumer_id | startswith("live-test-")) | .id' | \
            while read session_id; do
                echo "  Destroying session: $session_id"
                curl -s -X DELETE "$SHOPPER_URL/api/v1/sessions/$session_id" > /dev/null 2>&1 || true
            done

        echo "Cleanup complete"
    }
    trap cleanup EXIT
else
    echo "Server already running"
fi

echo ""

# Setup diagnostics directory
DIAG_OUTPUT_DIR="${DIAG_OUTPUT_DIR:-./diagnostics/$(date +%Y%m%d_%H%M%S)}"
mkdir -p "$DIAG_OUTPUT_DIR"
export DIAG_OUTPUT_DIR
echo "Diagnostics directory: $DIAG_OUTPUT_DIR"

echo ""
echo "═══════════════════════════════════════════════════════════════════════════"
echo "  RUNNING LIVE TESTS"
echo "═══════════════════════════════════════════════════════════════════════════"
echo ""

# Run live tests with timeout
# -timeout 95m allows 90 min of tests + 5 min margin
# -v for verbose output
# -count=1 to disable test caching
timeout 95m go test -v -tags=live -timeout=90m -count=1 ./test/live/... 2>&1 | tee live-test.log

TEST_EXIT_CODE=${PIPESTATUS[0]}

echo ""
echo "═══════════════════════════════════════════════════════════════════════════"
if [ $TEST_EXIT_CODE -eq 0 ]; then
    echo "  LIVE TESTS PASSED"
else
    echo "  LIVE TESTS FAILED (exit code: $TEST_EXIT_CODE)"
fi
echo "═══════════════════════════════════════════════════════════════════════════"

# Show diagnostics location
if [ -d "$DIAG_OUTPUT_DIR" ]; then
    DIAG_COUNT=$(find "$DIAG_OUTPUT_DIR" -name "*.json" 2>/dev/null | wc -l)
    if [ "$DIAG_COUNT" -gt 0 ]; then
        echo ""
        echo "Diagnostics collected: $DIAG_COUNT files"
        echo "Location: $DIAG_OUTPUT_DIR"
        ls -la "$DIAG_OUTPUT_DIR" 2>/dev/null || true
    fi
fi

exit $TEST_EXIT_CODE
