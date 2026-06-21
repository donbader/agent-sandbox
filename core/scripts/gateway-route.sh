#!/bin/sh
# gateway-route.sh — Container routing setup for agent-sandbox.
#
# At runtime, the gateway writes the authoritative version of this script
# to /shared/certs/gateway-route.sh with its IP baked in.
#
# This file in core/scripts/ serves as:
#   1. Fallback if the volume script doesn't exist (shouldn't happen in production)
#   2. Documentation of the expected script contract
#
# Requirements: ip (BusyBox or iproute2) — no iptables needed.
# The gateway handles traffic interception via PREROUTING on its side.
set -e

# If gateway-authored script exists on volume, use it and return.
if [ -f /shared/certs/gateway-route.sh ] && [ "$(readlink -f "$0" 2>/dev/null)" != "/shared/certs/gateway-route.sh" ]; then
    . /shared/certs/gateway-route.sh
    return 0 2>/dev/null || exit 0
fi

# Fallback: resolve gateway IP from environment (set by compose).
: "${GATEWAY_HOST:=gateway}"
: "${GATEWAY_IP:=}"

if [ -z "$GATEWAY_IP" ]; then
    if command -v getent >/dev/null 2>&1; then
        GATEWAY_IP=$(getent hosts "$GATEWAY_HOST" | awk '{print $1}' | head -1)
    fi
fi
if [ -z "$GATEWAY_IP" ]; then
    GATEWAY_IP=$(ping -c1 -W2 "$GATEWAY_HOST" 2>/dev/null | head -1 | sed -n 's/.*(\([0-9.]*\)).*/\1/p')
fi
if [ -z "$GATEWAY_IP" ]; then
    echo "[gateway-route] ERROR: could not resolve gateway IP" >&2
    exit 1
fi

# Default route — send all traffic to the gateway.
# On internal:true networks there is no pre-existing default route.
ip route add default via "$GATEWAY_IP" 2>/dev/null || ip route replace default via "$GATEWAY_IP" 2>/dev/null || true

# CA certificate
if [ -f /shared/certs/ca.crt ]; then
    if ! grep -qF "$(sed -n '2p' /shared/certs/ca.crt)" /etc/ssl/certs/ca-certificates.crt 2>/dev/null; then
        cat /shared/certs/ca.crt >> /etc/ssl/certs/ca-certificates.crt 2>/dev/null || true
    fi
    export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
fi
