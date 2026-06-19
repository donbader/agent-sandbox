#!/bin/sh
# gateway-route.sh — Shared gateway routing setup for agent and sidecar containers.
# Sources: iptables DNAT, DNS, CA cert installation.
# Requires: iptables, curl, ca-certificates, NET_ADMIN capability.
# Env: GATEWAY_HOST (required), set by compose.
set -e

: "${GATEWAY_HOST:=gateway}"

# --- Wait for gateway health ---
echo "[gateway-route] waiting for gateway ($GATEWAY_HOST)..."
until curl -sf http://$GATEWAY_HOST:8080/health >/dev/null 2>&1; do
    sleep 1
done
echo "[gateway-route] gateway ready"

# --- Resolve gateway IP ---
GATEWAY_IP=""
if command -v getent >/dev/null 2>&1; then
    GATEWAY_IP=$(getent hosts $GATEWAY_HOST | awk '{print $1}' | head -1)
fi
if [ -z "$GATEWAY_IP" ]; then
    GATEWAY_IP=$(ping -c1 -W1 $GATEWAY_HOST 2>/dev/null | head -1 | sed -n 's/.*(\([0-9.]*\)).*/\1/p')
fi
if [ -z "$GATEWAY_IP" ]; then
    echo "[gateway-route] ERROR: could not resolve gateway IP" >&2
    exit 1
fi
echo "[gateway-route] gateway IP: $GATEWAY_IP"
export GATEWAY_IP

# --- Determine sandbox CIDR (local traffic excluded from redirect) ---
SANDBOX_CIDR=$(ip route | grep "dev eth0" | grep -v default | awk '{print $1}' | head -1)
if [ -z "$SANDBOX_CIDR" ]; then
    SANDBOX_CIDR="${GATEWAY_IP}/32"
fi

# --- iptables DNAT: redirect outbound TCP to gateway ---
iptables -t nat -A OUTPUT -p tcp ! -d "$SANDBOX_CIDR" -j DNAT --to-destination "${GATEWAY_IP}:8443"
echo "[gateway-route] iptables: outbound TCP (except $SANDBOX_CIDR) → ${GATEWAY_IP}:8443"

# --- Default route (for DNAT reachability on internal networks) ---
if ! ip route show default >/dev/null 2>&1 || [ -z "$(ip route show default 2>/dev/null)" ]; then
    ip route add default via "$GATEWAY_IP" 2>/dev/null || route add default gw "$GATEWAY_IP" 2>/dev/null || true
    echo "[gateway-route] added default route via ${GATEWAY_IP}"
fi

# --- DNS: point at gateway's forwarder ---
echo "nameserver ${GATEWAY_IP}" > /etc/resolv.conf
echo "nameserver 127.0.0.11" >> /etc/resolv.conf
echo "[gateway-route] DNS: ${GATEWAY_IP} (primary), 127.0.0.11 (fallback)"

# --- CA certificate ---
if [ -f /shared/certs/ca.crt ]; then
    cp /shared/certs/ca.crt /usr/local/share/ca-certificates/gateway-ca.crt
    update-ca-certificates --fresh >/dev/null 2>&1 || true
    _cert_id=$(sed -n '2p' /shared/certs/ca.crt)
    if ! grep -qF "$_cert_id" /etc/ssl/certs/ca-certificates.crt 2>/dev/null; then
        cat /shared/certs/ca.crt >> /etc/ssl/certs/ca-certificates.crt
    fi
    export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/gateway-ca.crt
    export NODE_USE_SYSTEM_CA=1
    echo "[gateway-route] CA certificate installed"
else
    echo "[gateway-route] WARNING: CA cert not found at /shared/certs/ca.crt" >&2
fi
