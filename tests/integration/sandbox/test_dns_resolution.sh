#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

echo "--- DNS resolution ---"

# Verify GATEWAY_HOST resolves to an IP on the sandbox subnet (172.30.0.x)
RESOLVED_IP=$(exec_in sandbox-test sh -c 'getent hosts $GATEWAY_HOST | awk "{print \$1}" | head -1' || true)
if [[ "$RESOLVED_IP" == 172.30.0.* ]]; then
  pass "GATEWAY_HOST resolves to sandbox subnet ($RESOLVED_IP)"
else
  fail "GATEWAY_HOST resolved to wrong IP" "Got: $RESOLVED_IP (expected 172.30.0.x)"
fi

# Verify agent can reach gateway health endpoint
HEALTH=$(exec_in sandbox-test sh -c 'curl -sf http://$GATEWAY_HOST:8080/health' || true)
if [ -n "$HEALTH" ]; then
  pass "Agent can reach gateway health endpoint via GATEWAY_HOST"
else
  fail "Agent cannot reach gateway via GATEWAY_HOST" "curl to \$GATEWAY_HOST:8080/health failed"
fi
