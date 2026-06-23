#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

echo "--- Sidecar network connectivity (Alpine/BusyBox) ---"
RESPONSE=""
for attempt in 1 2 3 4 5; do
  RESPONSE=$(exec_in sandbox-test-agent-docker-buildkit wget -qO- --timeout=10 https://httpbin.org/get || true)
  if echo "$RESPONSE" | grep -q "Host"; then break; fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done

if echo "$RESPONSE" | grep -q "Host"; then
  pass "Alpine sidecar: HTTPS connectivity works"
else
  fail "Alpine sidecar: HTTPS connectivity failed" "Response: $RESPONSE"
fi
