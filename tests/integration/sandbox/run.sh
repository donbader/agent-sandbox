#!/usr/bin/env bash
# Integration test: validates the sandbox security contract.
# Runs on Linux CI (requires Docker with compose v2).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FIXTURE_DIR="$SCRIPT_DIR"
CLI="${CLI_PATH:-agent-sandbox}"
COMPOSE_PROJECT="sandbox-integration-test"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

PASSED=0
FAILED=0

pass() {
  echo -e "${GREEN}PASS${NC}: $1"
  PASSED=$((PASSED + 1))
}

fail() {
  echo -e "${RED}FAIL${NC}: $1"
  echo "  $2"
  FAILED=$((FAILED + 1))
}

cleanup() {
  echo "--- Cleaning up ---"
  docker compose -p "$COMPOSE_PROJECT" -f "$FIXTURE_DIR/.build/docker-compose.yml" down -v 2>/dev/null || true
}
trap cleanup EXIT

# --- Setup ---
echo "=== Sandbox Integration Tests ==="
echo ""

echo "--- Generating build artifacts ---"
"$CLI" generate -C "$FIXTURE_DIR"

echo ""
echo "--- Building and starting containers ---"
docker compose -p "$COMPOSE_PROJECT" -f "$FIXTURE_DIR/.build/docker-compose.yml" --env-file "$FIXTURE_DIR/test.env" up -d --build --wait --wait-timeout 60

AGENT_CONTAINER="${COMPOSE_PROJECT}-sandbox-test-1"
GATEWAY_CONTAINER="${COMPOSE_PROJECT}-sandbox-test-gateway-1"

# Wait for agent entrypoint to complete
sleep 3

echo ""
echo "--- Running tests ---"
echo ""

# Test 1: Agent can reach external HTTPS endpoint
echo "Test 1: Agent can reach external HTTPS endpoint"
if docker exec "$AGENT_CONTAINER" curl -sf --max-time 30 https://httpbin.org/get >/dev/null 2>&1; then
  pass "Agent reached https://httpbin.org/get through gateway"
else
  # Retry once — first request may be slow due to cold DNS + upstream TLS
  if docker exec "$AGENT_CONTAINER" curl -sf --max-time 30 https://httpbin.org/get >/dev/null 2>&1; then
    pass "Agent reached https://httpbin.org/get through gateway (retry)"
  else
    fail "Agent cannot reach external HTTPS" "curl to httpbin.org failed or timed out"
  fi
fi

# Test 2: Agent env does not contain real secrets
echo "Test 2: Agent env contains no real secrets"
AGENT_ENV=$(docker exec "$AGENT_CONTAINER" env 2>&1)
if echo "$AGENT_ENV" | grep -q "super-secret-token-12345"; then
  fail "Agent env leaks secrets" "TEST_SECRET value found in agent environment"
else
  pass "Agent env does not contain real secret values"
fi

# Test 3: Outbound request has injected auth header
echo "Test 3: Gateway injects auth header into outbound requests"
RESPONSE=$(docker exec "$AGENT_CONTAINER" curl -sf --max-time 30 https://httpbin.org/headers 2>&1 || true)
if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then
  pass "Gateway injected Authorization header into outbound request"
else
  fail "Gateway did not inject auth header" "Response: $RESPONSE"
fi

# Test 4: DNS resolves through gateway
echo "Test 4: DNS resolves through gateway"
RESOLV_NS=$(docker exec "$AGENT_CONTAINER" cat /etc/resolv.conf | awk '/^nameserver/{print $2}')
DEFAULT_GW=$(docker exec "$AGENT_CONTAINER" ip route show default | awk '{print $3}')
if [ "$RESOLV_NS" = "$DEFAULT_GW" ]; then
  pass "DNS configured to resolve through gateway ($RESOLV_NS)"
else
  fail "DNS not pointing to gateway" "nameserver=$RESOLV_NS, default_gw=$DEFAULT_GW"
fi

# Test 5: Gateway CA is trusted by agent
echo "Test 5: Gateway CA certificate is trusted"
if docker exec "$AGENT_CONTAINER" test -f /usr/local/share/ca-certificates/ca.crt; then
  pass "Gateway CA certificate present in agent trust store"
else
  fail "Gateway CA certificate missing" "/usr/local/share/ca-certificates/ca.crt not found"
fi

# Test 6: Agent traffic routes through gateway (OUTPUT DNAT rules in place)
echo "Test 6: Agent has OUTPUT DNAT rules for traffic interception"
IPTABLES=$(docker exec "$AGENT_CONTAINER" iptables -t nat -L OUTPUT -n 2>&1)
if echo "$IPTABLES" | grep -q "DNAT.*tcp dpt:443"; then
  pass "OUTPUT DNAT rule for port 443 is active"
else
  fail "Missing OUTPUT DNAT rule" "iptables OUTPUT chain: $IPTABLES"
fi

# Test 7: Agent default route goes through gateway
echo "Test 7: Agent default route goes through gateway"
DEFAULT_ROUTE=$(docker exec "$AGENT_CONTAINER" ip route show default 2>&1)
RESOLV_NS=$(docker exec "$AGENT_CONTAINER" awk '/^nameserver/{print $2}' /etc/resolv.conf)
if echo "$DEFAULT_ROUTE" | grep -q "$RESOLV_NS"; then
  pass "Default route points to gateway ($RESOLV_NS)"
else
  fail "Default route does not point to gateway" "Route: $DEFAULT_ROUTE, expected: $RESOLV_NS"
fi

# --- Summary ---
echo ""
echo "=== Results: $PASSED passed, $FAILED failed ==="

if [ "$FAILED" -gt 0 ]; then
  echo ""
  echo "--- Container logs (agent) ---"
  docker logs "$AGENT_CONTAINER" 2>&1 | tail -30
  echo ""
  echo "--- Container logs (gateway) ---"
  docker logs "$GATEWAY_CONTAINER" 2>&1 | tail -30
  exit 1
fi
