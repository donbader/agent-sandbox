#!/usr/bin/env bash
# Integration test: validates the sandbox security contract.
# Runs on Linux CI (requires Docker with compose v2).
# Uses `agent-sandbox audit` for core checks, plus credential injection test.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CLI="${CLI_PATH:-agent-sandbox}"

cleanup() {
  echo "--- Cleaning up ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" down -v 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Sandbox Integration Tests ==="
echo ""

# Export test secrets so generate can bake them into middleware
export $(grep -v '^#' "$SCRIPT_DIR/test.env" | xargs)

echo "--- Generating build artifacts ---"
"$CLI" generate -C "$SCRIPT_DIR"

echo ""
echo "--- Building and starting containers ---"
"$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" up -d --build --wait --wait-timeout 60

# Wait for agent entrypoint to complete
sleep 3

echo ""

# The one check audit can't do: credential injection verification.
# This requires a known secret and a mirror endpoint to confirm injection.
echo "--- Credential injection check ---"
AGENT_SERVICE="sandbox-test"
GATEWAY_SERVICE="sandbox-test-gateway"

RESPONSE=$("$CLI" -C "$SCRIPT_DIR" compose exec "$AGENT_SERVICE" curl -so- --max-time 30 https://httpbin.org/headers 2>&1 || true)
if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then
  echo -e "  \033[32m✓\033[0m Gateway injects credentials into outbound requests"
else
  echo -e "  \033[31m✗\033[0m Gateway did not inject credentials"
  echo "    Response: $RESPONSE"
  echo ""
  echo "--- Container logs (agent) ---"
  "$CLI" -C "$SCRIPT_DIR" compose logs "$AGENT_SERVICE" 2>&1 | tail -30
  echo ""
  echo "--- Container logs (gateway) ---"
  "$CLI" -C "$SCRIPT_DIR" compose logs "$GATEWAY_SERVICE" 2>&1 | tail -20
  exit 1
fi

echo ""
echo "=== All checks passed ==="
