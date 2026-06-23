#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

echo "--- Node.js CA trust ---"
RESPONSE=""
for attempt in 1 2 3 4 5; do
  RESPONSE=$(exec_in sandbox-test node -e "fetch('https://httpbin.org/get').then(r=>r.text()).then(t=>{console.log(t);process.exit(0)}).catch(e=>{console.error(e.message);process.exit(1)})" || true)
  if echo "$RESPONSE" | grep -q "Host"; then break; fi
  echo "  attempt $attempt: not ready, retrying in 3s..."
  sleep 3
done

if echo "$RESPONSE" | grep -q "Host"; then
  pass "Node.js: HTTPS with MITM CA trust works"
else
  fail "Node.js: HTTPS with MITM CA trust failed" "Response: $RESPONSE"
fi
