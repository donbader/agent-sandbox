#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

echo "--- Credential injection ---"
RESPONSE=""
for attempt in 1 2 3 4 5; do
  RESPONSE=$(exec_in sandbox-test curl -so- --max-time 10 https://httpbin.org/headers || true)
  if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then break; fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done

if echo "$RESPONSE" | grep -q "super-secret-token-12345"; then
  pass "Gateway injects credentials into outbound requests"
else
  fail "Gateway did not inject credentials" "Response: $RESPONSE"
fi
