# Egress Hardening — Full Traffic Enforcement

**Date:** 2025-06-17
**Status:** Draft
**Prerequisite for:** Docker API Proxy spec

## Problem

The current entrypoint only redirects port 443 (HTTPS) to the gateway:

```bash
iptables -t nat -A OUTPUT -p tcp --dport 443 ! -d "$GATEWAY_IP" -j DNAT --to-destination "${GATEWAY_IP}:8443"
```

This means:
- Port 80 (HTTP) bypasses the gateway entirely
- Arbitrary TCP to external hosts is unmonitored
- Spawned containers (from the Docker proxy feature) have no iptables setup at all

## Goals

1. **Agent container**: all outbound TCP goes through gateway (not just 443)
2. **Spawned containers**: cannot reach the internet at all (network-level isolation)
3. **Internal traffic**: containers on the sandbox network can still talk to each other freely

## Architecture

### Two-Network Model

```
                    external network
                           |
                    +------+------+
                    |   Gateway   |
                    +------+------+
                           |
                    sandbox network (internal: true)
                    |      |      |
              +-----+  +---+---+  +--------+
              | Agent|  |Sidecar|  |Spawned |
              +------+  +-------+  +--------+
```

- **sandbox network**: `internal: true` — no direct internet access for any container on this network
- **external network**: only the gateway is attached — it's the sole path to the internet
- Agent, sidecars, and spawned containers can reach each other freely on the sandbox network
- All internet-bound traffic must go through the gateway

### Agent Container (iptables)

The entrypoint redirects **all outbound TCP** (not just 443) to the gateway:

```bash
# Get sandbox network CIDR (e.g., 172.20.0.0/16)
SANDBOX_CIDR=$(ip route | grep "dev eth0" | grep -v default | awk '{print $1}' | head -1)

# Redirect ALL outbound TCP (except sandbox-local) to gateway
iptables -t nat -A OUTPUT -p tcp ! -d "$SANDBOX_CIDR" -j DNAT --to-destination "${GATEWAY_IP}:8443"
```

This replaces the current port-443-only rule. Combined with `internal: true`, the agent physically cannot reach the internet without going through the gateway.

### Spawned Containers (network-level only)

Spawned containers have no entrypoint modifications. They rely purely on the network being `internal: true`:
- They can reach other containers on the sandbox network (agent, sidecars, each other)
- They cannot reach the internet — the network layer blocks it
- No iptables, no init process, no special configuration needed

### Gateway Protocol Handling

The gateway's `:8443` listener already detects TLS vs non-TLS by reading the first byte. Extended to handle all incoming TCP:

| First bytes | Protocol | Action |
|-------------|----------|--------|
| `0x16` (TLS ClientHello) | TLS | Existing: SNI extraction → MITM or passthrough |
| HTTP method (`GET`, `POST`, etc.) | HTTP | Proxy as plain HTTP, apply service/auth rules |
| Anything else | Unknown TCP | Block (connection reset) |

No config section needed. This is the only behavior. If the gateway doesn't recognize the protocol, it blocks it.

## Compose Changes

```yaml
networks:
  sandbox:
    driver: bridge
    internal: true
  external:
    driver: bridge
```

Gateway service:
```yaml
gateway:
  networks:
    - sandbox
    - external
```

Agent + sidecars + spawned containers:
```yaml
agent:
  networks:
    - sandbox
```

## Entrypoint Changes

Replace the port-443-only iptables rule in `entrypoint.sh.tmpl`:

```bash
# Get sandbox network CIDR
SANDBOX_CIDR=$(ip route | grep "dev eth0" | grep -v default | awk '{print $1}' | head -1)

# Redirect ALL outbound TCP (except sandbox-local) to gateway
iptables -t nat -A OUTPUT -p tcp ! -d "$SANDBOX_CIDR" -j DNAT --to-destination "${GATEWAY_IP}:8443"
echo "[entrypoint] iptables: all outbound TCP → ${GATEWAY_IP}:8443"
```

## What This Enables

- Spawned containers (from Docker proxy) are automatically internet-isolated — no additional config
- All agent traffic is observable and controllable through the gateway
- Credential injection works for HTTP too (not just HTTPS)
- Security posture matches user expectation: "gateway controls all egress"

## Testing Strategy

- Unit test: iptables rule generation with various CIDR inputs
- Integration test: agent cannot reach external host on port 80 directly
- Integration test: agent can still reach other sandbox containers on arbitrary ports
- Integration test: spawned container on sandbox network cannot reach internet
- Integration test: gateway proxies HTTP requests correctly
- Integration test: gateway blocks unknown TCP protocols
