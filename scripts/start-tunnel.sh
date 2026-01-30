#!/bin/bash
# start-tunnel.sh - Expose local shopper server to GPU instances via Cloudflare Tunnel
#
# Usage:
#   ./scripts/start-tunnel.sh [port]
#
# The tunnel URL will be printed and also saved to .tunnel-url
# Use this URL as SHOPPER_URL when running tests

set -e

PORT="${1:-8080}"

echo "═══════════════════════════════════════════════════════════════════════════"
echo "  CLOUDFLARE TUNNEL - Exposing localhost:$PORT"
echo "═══════════════════════════════════════════════════════════════════════════"

# Check for cloudflared
if ! command -v cloudflared &> /dev/null; then
    echo ""
    echo "cloudflared not found. Installing..."

    if command -v brew &> /dev/null; then
        echo "Installing via Homebrew..."
        brew install cloudflare/cloudflare/cloudflared
    elif command -v apt-get &> /dev/null; then
        echo "Installing via apt..."
        curl -L --output cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb
        sudo dpkg -i cloudflared.deb
        rm cloudflared.deb
    else
        echo "ERROR: Cannot auto-install cloudflared."
        echo "Please install manually:"
        echo "  macOS:   brew install cloudflare/cloudflare/cloudflared"
        echo "  Linux:   https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation"
        echo "  Windows: https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/installation"
        exit 1
    fi
fi

# Verify local server is running
echo ""
echo "Checking if local server is running on port $PORT..."
if ! curl -s "http://localhost:$PORT/health" > /dev/null 2>&1; then
    echo "WARNING: Server not responding at http://localhost:$PORT"
    echo "Make sure the shopper server is running before using the tunnel."
    echo ""
fi

# Create a temporary file for capturing output
TUNNEL_OUTPUT=$(mktemp)
trap "rm -f $TUNNEL_OUTPUT" EXIT

echo ""
echo "Starting Cloudflare Tunnel..."
echo "This may take a few seconds to establish..."
echo ""

# Start cloudflared and capture output
cloudflared tunnel --url "http://localhost:$PORT" 2>&1 | tee "$TUNNEL_OUTPUT" &
TUNNEL_PID=$!

# Wait for the tunnel URL to appear
echo "Waiting for tunnel URL..."
for i in {1..30}; do
    sleep 1
    TUNNEL_URL=$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' "$TUNNEL_OUTPUT" | head -1 || true)
    if [ -n "$TUNNEL_URL" ]; then
        break
    fi
done

if [ -z "$TUNNEL_URL" ]; then
    echo ""
    echo "ERROR: Could not extract tunnel URL."
    echo "Check the output above for errors."
    echo ""
    echo "Common issues:"
    echo "  - No internet connection"
    echo "  - Cloudflare service unavailable"
    echo "  - Port $PORT already in use"
    kill $TUNNEL_PID 2>/dev/null || true
    exit 1
fi

# Save tunnel URL
echo "$TUNNEL_URL" > .tunnel-url

echo ""
echo "═══════════════════════════════════════════════════════════════════════════"
echo "  TUNNEL ESTABLISHED"
echo "═══════════════════════════════════════════════════════════════════════════"
echo ""
echo "  Tunnel URL:  $TUNNEL_URL"
echo "  Saved to:    .tunnel-url"
echo ""
echo "  Use this for live tests:"
echo "    export SHOPPER_URL=$TUNNEL_URL"
echo ""
echo "  Or run tests directly:"
echo "    SHOPPER_URL=$TUNNEL_URL go test -v -tags=live ./test/live/..."
echo ""
echo "═══════════════════════════════════════════════════════════════════════════"
echo ""
echo "Press Ctrl+C to stop the tunnel"
echo ""

# Keep the tunnel running
wait $TUNNEL_PID
