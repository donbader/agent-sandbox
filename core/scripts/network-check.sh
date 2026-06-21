#!/bin/sh
# network-check.sh — Verify sidecar networking layer by layer.
# Run inside a sidecar container to diagnose connectivity issues.
# Exit 0 = all good, exit 1 = failure (prints which layer failed).
set -e

PASS=0
FAIL=0
report() { printf "  [%s] %s\n" "$1" "$2"; }
pass() { report "PASS" "$1"; PASS=$((PASS + 1)); }
fail() { report "FAIL" "$1"; FAIL=$((FAIL + 1)); }

echo "=== Sidecar Network Diagnostics ==="
echo ""

# 1. Gateway hostname resolution
echo "--- Layer 1: DNS ---"
: "${GATEWAY_HOST:=gateway}"
GW_IP=$(getent hosts "$GATEWAY_HOST" 2>/dev/null | awk '{print $1}' | head -1)
if [ -n "$GW_IP" ]; then
    pass "Gateway hostname '$GATEWAY_HOST' resolves to $GW_IP"
else
    fail "Cannot resolve gateway hostname '$GATEWAY_HOST'"
fi

# 2. Gateway health
echo "--- Layer 2: Gateway Health ---"
if wget -q --spider --timeout=5 "http://${GATEWAY_HOST}:8080/health" 2>/dev/null || curl -sf --max-time 5 "http://${GATEWAY_HOST}:8080/health" >/dev/null 2>&1; then
    pass "Gateway health endpoint reachable"
else
    fail "Gateway health endpoint unreachable (http://${GATEWAY_HOST}:8080/health)"
fi

# 3. Default route
echo "--- Layer 3: Routing ---"
DEFAULT_ROUTE=$(ip route show default 2>/dev/null | head -1)
if [ -n "$DEFAULT_ROUTE" ]; then
    pass "Default route exists: $DEFAULT_ROUTE"
    if echo "$DEFAULT_ROUTE" | grep -q "$GW_IP"; then
        pass "Default route points to gateway ($GW_IP)"
    else
        fail "Default route does NOT point to gateway ($GW_IP): $DEFAULT_ROUTE"
    fi
else
    fail "No default route (traffic cannot reach gateway)"
fi

# 4. CA certificate
echo "--- Layer 4: TLS CA ---"
if [ -f /shared/certs/ca.crt ]; then
    pass "Gateway CA cert exists at /shared/certs/ca.crt"
    if grep -qF "$(sed -n '2p' /shared/certs/ca.crt)" /etc/ssl/certs/ca-certificates.crt 2>/dev/null; then
        pass "Gateway CA installed in system trust store"
    else
        fail "Gateway CA NOT in system trust store"
    fi
else
    fail "No CA cert at /shared/certs/ca.crt"
fi

# 5. End-to-end HTTPS
echo "--- Layer 5: End-to-End HTTPS ---"
if wget -qO /dev/null --timeout=10 "https://registry-1.docker.io/v2/" 2>/dev/null || curl -sf --max-time 10 "https://registry-1.docker.io/v2/" >/dev/null 2>&1; then
    pass "HTTPS to registry-1.docker.io works (end-to-end OK)"
else
    # Try a simpler target
    if wget -qO /dev/null --timeout=10 "https://github.com" 2>/dev/null || curl -sf --max-time 10 "https://github.com" >/dev/null 2>&1; then
        pass "HTTPS to github.com works (registry might be down)"
    else
        fail "HTTPS completely broken (cannot reach any external host)"
    fi
fi

# Summary
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
