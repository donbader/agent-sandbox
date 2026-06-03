#!/usr/bin/env bash
# Integration test: validates the sandbox security contract.
# Runs on Linux CI (requires Docker with compose v2).
# Uses `agent-sandbox audit` for core checks, plus credential injection test.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI="${CLI_PATH:-agent-sandbox}"

cleanup() {
  echo "--- Cleaning up ---"
  "$CLI" -C "$SCRIPT_DIR" compose down -v 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Sandbox Integration Tests ==="
echo ""

echo "--- Generating build artifacts ---"
"$CLI" generate -C "$SCRIPT_DIR"

echo ""
echo "--- Building and starting containers ---"
# Export test secrets so compose picks them up
export $(grep -v '^#' "$SCRIPT_DIR/test.env" | xargs)
"$CLI" -C "$SCRIPT_DIR" compose up -d --build --wait --wait-timeout 60

# Wait for agent entrypoint to complete
sleep 3

echo ""
echo "--- Running audit checks ---"
"$CLI" -C "$SCRIPT_DIR" audit
echo ""

# The one check audit can't do: credential injection verification.
# This requires a known secret and a mirror endpoint to confirm injection.
echo "--- Credential injection check ---"
AGENT_CONTAINER="agent-sandbox-sandbox-test-1"

RESPONSE=$(docker exec "$AGENT_CONTAINER" curl -so- --max-time 30 https://httpbin.org/headers 2>&1 || true)
if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then
  echo -e "  \033[32m✓\033[0m Gateway injects credentials into outbound requests"
else
  echo -e "  \033[31m✗\033[0m Gateway did not inject credentials"
  echo "    Response: $RESPONSE"
  echo ""
  echo "--- Container logs (gateway) ---"
  docker logs "agent-sandbox-sandbox-test-gateway-1" 2>&1 | tail -20
  exit 1
fi

echo ""
echo "=== All checks passed ==="
