# ADR-002: Transparent Proxy (iptables) Over Explicit Proxy (HTTP_PROXY)

## Status
Accepted

## Context
We need to route all agent egress through our proxy for policy enforcement and credential injection. Two approaches:

**Explicit proxy:** Set `HTTP_PROXY` / `HTTPS_PROXY` environment variables. Tools that respect these vars (curl, git, npm, pip) will use the proxy.

**Transparent proxy:** Use iptables to redirect ALL outbound TCP at the kernel level. The agent is completely unaware of the proxy.

## Decision
Use transparent proxy with iptables redirect.

## Consequences

**Positive:**
- Agent cannot bypass the proxy (kernel-enforced, not env-var-based)
- Works with ALL tools, including those that ignore HTTP_PROXY
- Agent code doesn't need proxy awareness
- Catches custom HTTP clients, raw TCP connections, and any tool the agent installs
- No configuration needed inside the agent process

**Negative:**
- Requires `NET_ADMIN` capability in the container (for iptables setup)
- More complex proxy implementation (must handle raw TCP, parse TLS ClientHello for SNI)
- Slightly more complex container initialization (iptables rules at startup)
- Non-HTTP TCP traffic needs special handling (passthrough or block)

**Implementation:**
```bash
# Inside agent container at startup:
iptables -t nat -A OUTPUT -p tcp -m owner ! --uid-owner proxy-uid -j REDIRECT --to-port 8443
```

The proxy runs as a dedicated user (`proxy-uid`). Its own connections are exempt from redirection to prevent loops.
