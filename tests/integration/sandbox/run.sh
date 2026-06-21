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
if ! "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" up -d --build --wait --wait-timeout 60; then
  echo ""
  echo "--- COMPOSE UP FAILED — dumping container logs ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" logs 2>&1 | tail -50
  echo ""
  echo "--- Container status ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" ps -a 2>&1
  exit 1
fi

# Wait for agent entrypoint to complete
sleep 3

echo ""

# The one check audit can't do: credential injection verification.
# This requires a known secret and a mirror endpoint to confirm injection.
echo "--- Credential injection check ---"
AGENT_SERVICE="sandbox-test"
GATEWAY_SERVICE="sandbox-test-gateway"

# Retry loop: networking inside the container may take a moment after entrypoint completes.
RESPONSE=""
for attempt in 1 2 3 4 5; do
  RESPONSE=$("$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" exec "$AGENT_SERVICE" curl -so- --max-time 10 https://httpbin.org/headers 2>&1 || true)
  if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then
    break
  fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done
if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then
  echo -e "  \033[32m✓\033[0m Gateway injects credentials into outbound requests"
else
  echo -e "  \033[31m✗\033[0m Gateway did not inject credentials"
  echo "    Response: $RESPONSE"
  echo ""
  echo "--- Container logs (agent) ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" logs "$AGENT_SERVICE" 2>&1 | tail -30
  echo ""
  echo "--- Container logs (gateway) ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" logs "$GATEWAY_SERVICE" 2>&1 | tail -20
  exit 1
fi

echo ""
echo "--- Sidecar network connectivity (Alpine/BusyBox) ---"
BUILDKIT_SERVICE="sandbox-test-agent-docker-buildkit"

# The buildkit sidecar is Alpine-based. If routing works, it can reach httpbin via HTTPS.
SIDECAR_RESPONSE=""
for attempt in 1 2 3 4 5; do
  SIDECAR_RESPONSE=$("$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" exec "$BUILDKIT_SERVICE" wget -qO- --timeout=10 https://httpbin.org/get 2>&1 || true)
  if echo "$SIDECAR_RESPONSE" | grep -q "Host"; then
    break
  fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done
if echo "$SIDECAR_RESPONSE" | grep -q "Host"; then
  echo -e "  \033[32m✓\033[0m Alpine sidecar: HTTPS connectivity works"
else
  echo -e "  \033[31m✗\033[0m Alpine sidecar: HTTPS connectivity failed"
  echo "    Response: $SIDECAR_RESPONSE"
  echo ""
  echo "--- Sidecar routing ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" exec "$BUILDKIT_SERVICE" ip route 2>&1 || true
  echo ""
  echo "--- Container logs (buildkit) ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" logs "$BUILDKIT_SERVICE" 2>&1 | tail -20
  exit 1
fi

echo ""
echo "--- Node.js CA trust ---"

# Node.js must trust the gateway's MITM CA cert (NODE_EXTRA_CA_CERTS / NODE_USE_SYSTEM_CA).
NODE_RESPONSE=""
for attempt in 1 2 3 4 5; do
  NODE_RESPONSE=$("$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" exec "$AGENT_SERVICE" node -e "fetch('https://httpbin.org/get').then(r=>r.text()).then(t=>{console.log(t);process.exit(0)}).catch(e=>{console.error(e.message);process.exit(1)})" 2>&1 || true)
  if echo "$NODE_RESPONSE" | grep -q "Host"; then
    break
  fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done
if echo "$NODE_RESPONSE" | grep -q "Host"; then
  echo -e "  \033[32m✓\033[0m Node.js: HTTPS with MITM CA trust works"
else
  echo -e "  \033[31m✗\033[0m Node.js: HTTPS with MITM CA trust failed"
  echo "    Response: $NODE_RESPONSE"
  echo ""
  echo "--- Agent env (CA vars) ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" exec "$AGENT_SERVICE" env 2>&1 | grep -i "NODE\|SSL\|CA" || true
  exit 1
fi

echo ""
echo "--- BuildKit build verification ---"

# Verify the buildkit sidecar can actually execute RUN commands (catches cgroup issues).
BUILD_RESULT=""
for attempt in 1 2 3; do
  BUILD_RESULT=$("$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" exec "$AGENT_SERVICE" \
    sh -c 'printf "FROM alpine:3.20\nRUN echo buildkit-ok" | docker buildx build --builder remote --no-cache -' 2>&1 || true)
  if echo "$BUILD_RESULT" | grep -q "buildkit-ok\|exporting to image"; then
    break
  fi
  echo "  attempt $attempt: build not ready, retrying in 3s..."
  sleep 3
done
if echo "$BUILD_RESULT" | grep -q "buildkit-ok\|exporting to image"; then
  echo -e "  \033[32m✓\033[0m BuildKit: can build Dockerfiles (runc + cgroup working)"
else
  echo -e "  \033[31m✗\033[0m BuildKit: build failed"
  echo "    Output: $(echo "$BUILD_RESULT" | tail -5)"
  echo ""
  echo "--- BuildKit sidecar logs ---"
  "$CLI" -C "$SCRIPT_DIR" compose -f "$SCRIPT_DIR/compose-override.yml" logs "$BUILDKIT_SERVICE" 2>&1 | tail -15
  exit 1
fi

echo ""
echo "=== All checks passed ==="
