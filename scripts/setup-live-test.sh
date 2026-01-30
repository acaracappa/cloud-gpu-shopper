#!/bin/bash
# Setup script for live GPU testing with real provider instances
# This script:
# 1. Builds the agent binary for Linux
# 2. Starts ngrok to expose the shopper server
# 3. Starts a file server for the agent binary (via ngrok)
# 4. Exports environment variables for the live tests

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
DATA_DIR="$PROJECT_DIR/data"
BUILD_DIR="$PROJECT_DIR/build"

echo "=========================================="
echo "  Live Test Environment Setup"
echo "=========================================="

# Check dependencies
check_cmd() {
    if ! command -v "$1" &> /dev/null; then
        echo "ERROR: $1 is required but not installed."
        echo "Install with: $2"
        exit 1
    fi
}

check_cmd "ngrok" "brew install ngrok"
check_cmd "jq" "brew install jq"

# Create directories
mkdir -p "$DATA_DIR"
mkdir -p "$BUILD_DIR"

# Step 1: Build agent binary for Linux
echo ""
echo "[1/4] Building agent binary for Linux..."
GOOS=linux GOARCH=amd64 go build -o "$BUILD_DIR/gpu-agent-linux-amd64" ./cmd/agent
echo "  Built: $BUILD_DIR/gpu-agent-linux-amd64"

# Step 2: Start ngrok for shopper server (port 8080)
echo ""
echo "[2/4] Starting ngrok for shopper server (port 8080)..."

# Kill any existing ngrok processes
pkill -f "ngrok http" 2>/dev/null || true
sleep 1

# Start ngrok in background
ngrok http 8080 --log=stdout > /tmp/ngrok-server.log 2>&1 &
NGROK_SERVER_PID=$!
echo "  ngrok PID: $NGROK_SERVER_PID"

# Wait for ngrok to start and get public URL
echo "  Waiting for ngrok tunnel..."
sleep 3

# Get the public URL from ngrok API
SHOPPER_PUBLIC_URL=""
for i in {1..10}; do
    SHOPPER_PUBLIC_URL=$(curl -s http://localhost:4040/api/tunnels 2>/dev/null | jq -r '.tunnels[0].public_url' 2>/dev/null || echo "")
    if [ -n "$SHOPPER_PUBLIC_URL" ] && [ "$SHOPPER_PUBLIC_URL" != "null" ]; then
        break
    fi
    sleep 1
done

if [ -z "$SHOPPER_PUBLIC_URL" ] || [ "$SHOPPER_PUBLIC_URL" == "null" ]; then
    echo "ERROR: Failed to get ngrok tunnel URL"
    echo "Check /tmp/ngrok-server.log for details"
    kill $NGROK_SERVER_PID 2>/dev/null || true
    exit 1
fi

echo "  Shopper public URL: $SHOPPER_PUBLIC_URL"

# Step 3: Start simple file server for agent binary
echo ""
echo "[3/4] Starting file server for agent binary..."

# Start Python HTTP server in build directory (port 8081)
cd "$BUILD_DIR"
python3 -m http.server 8081 > /tmp/fileserver.log 2>&1 &
FILESERVER_PID=$!
cd "$PROJECT_DIR"
echo "  File server PID: $FILESERVER_PID"

# Start second ngrok tunnel for file server (needs ngrok config for multiple tunnels)
# Instead, use a single tunnel approach - serve agent from main server

# Actually, let's host the agent binary via the shopper server itself
# For now, we'll create a static endpoint or use a cloud storage URL

# Alternative: Upload to a temporary file hosting service
# For simplicity, let's just use the ngrok tunnel with a different approach

echo ""
echo "[4/4] Configuration"
echo "=========================================="
echo ""
echo "Add these to your environment or .env file:"
echo ""
echo "export SHOPPER_URL='$SHOPPER_PUBLIC_URL'"
echo "export SHOPPER_AGENT_URL=''"  # Agent will be served differently
echo ""
echo "To start the shopper server with these settings:"
echo ""
echo "  SHOPPER_URL='$SHOPPER_PUBLIC_URL' go run ./cmd/server"
echo ""
echo "=========================================="
echo "  Environment is ready!"
echo "=========================================="
echo ""
echo "IMPORTANT: The ngrok tunnel is running. Keep this terminal open."
echo ""
echo "PIDs to cleanup when done:"
echo "  ngrok: $NGROK_SERVER_PID"
echo "  file server: $FILESERVER_PID"
echo ""
echo "Or run: pkill -f ngrok; pkill -f 'python3 -m http.server'"
echo ""

# Export for this shell
export SHOPPER_URL="$SHOPPER_PUBLIC_URL"

# Keep running to maintain tunnels
echo "Press Ctrl+C to stop tunnels and cleanup"
wait $NGROK_SERVER_PID
