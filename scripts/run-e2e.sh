#!/bin/bash
set -e

# E2E Test Runner Script
# Usage: ./scripts/run-e2e.sh [--build] [--keep]
#
# Options:
#   --build   Force rebuild of Docker images
#   --keep    Keep containers running after tests

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

BUILD_FLAG=""
KEEP_FLAG=""

for arg in "$@"; do
    case $arg in
        --build)
            BUILD_FLAG="--build"
            ;;
        --keep)
            KEEP_FLAG="true"
            ;;
    esac
done

echo "================================================"
echo "  Cloud GPU Shopper - E2E Test Suite"
echo "================================================"
echo ""

cd "$PROJECT_DIR"

# Function to cleanup on exit
cleanup() {
    if [ -z "$KEEP_FLAG" ]; then
        echo ""
        echo "Cleaning up containers..."
        docker-compose -f deploy/docker-compose.e2e.yml down -v 2>/dev/null || true
    else
        echo ""
        echo "Containers kept running. To stop them:"
        echo "  docker-compose -f deploy/docker-compose.e2e.yml down -v"
    fi
}

trap cleanup EXIT

# Start services
echo "Starting E2E test environment..."
docker-compose -f deploy/docker-compose.e2e.yml up -d $BUILD_FLAG mock-provider server

# Wait for services to be healthy
echo "Waiting for services to be healthy..."
MAX_WAIT=60
WAITED=0

while [ $WAITED -lt $MAX_WAIT ]; do
    if docker-compose -f deploy/docker-compose.e2e.yml ps | grep -q "healthy"; then
        # Check if both services are healthy
        MOCK_HEALTHY=$(docker-compose -f deploy/docker-compose.e2e.yml ps mock-provider | grep -c "healthy" || echo "0")
        SERVER_HEALTHY=$(docker-compose -f deploy/docker-compose.e2e.yml ps server | grep -c "healthy" || echo "0")

        if [ "$MOCK_HEALTHY" -gt 0 ] && [ "$SERVER_HEALTHY" -gt 0 ]; then
            echo "All services healthy!"
            break
        fi
    fi

    sleep 2
    WAITED=$((WAITED + 2))
    echo "  Waited ${WAITED}s..."
done

if [ $WAITED -ge $MAX_WAIT ]; then
    echo "ERROR: Services did not become healthy within ${MAX_WAIT}s"
    docker-compose -f deploy/docker-compose.e2e.yml logs
    exit 1
fi

# Run tests
echo ""
echo "Running E2E tests..."
echo ""

# Run tests in container
docker-compose -f deploy/docker-compose.e2e.yml run --rm e2e-tests

TEST_EXIT_CODE=$?

if [ $TEST_EXIT_CODE -eq 0 ]; then
    echo ""
    echo "================================================"
    echo "  E2E TESTS PASSED"
    echo "================================================"
else
    echo ""
    echo "================================================"
    echo "  E2E TESTS FAILED (exit code: $TEST_EXIT_CODE)"
    echo "================================================"

    # Show logs on failure
    echo ""
    echo "Server logs:"
    docker-compose -f deploy/docker-compose.e2e.yml logs server --tail=50
fi

exit $TEST_EXIT_CODE
