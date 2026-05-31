# Security

## Architecture: Container Isolation

Gateway and agent run in **separate containers** connected by a Docker internal network. The agent container has no internet access — all traffic is forced through the gateway via iptables DNAT.

```
┌─ gateway container ─────────────────┐
│  Networks: [internal, default]      │
│  Holds: real credentials, CA key    │──── internet
│  IP forwarding + iptables PREROUTING│
│  Runs: gateway binary (:8443, :53)  │
└──────────────┬──────────────────────┘
               │ Docker internal network
┌──────────────┴──────────────────────┐
│  agent container                    │
│  Networks: [internal] ONLY          │
│  No secrets, no internet access     │
│  default route → gateway            │
│  Runs: bridge + agent               │
└─────────────────────────────────────┘
```

## Egress Model

**Allow all by default.** MITM only for hosts where credential injection is needed. Everything else passes through with end-to-end TLS preserved.

Rationale: Dev agents need `npm install`, `pip install`, `curl` arbitrary URLs. Default deny creates too much friction.

## Transparent Proxy

Agent container uses a default route via gateway. All outbound traffic flows naturally to the gateway:

```bash
# Resolve gateway container IP (Docker DNS, before switching resolv.conf)
GATEWAY_IP=$(getent hosts $GATEWAY_HOST | awk '{print $1}')

# Switch DNS to gateway resolver
echo "nameserver $GATEWAY_IP" > /etc/resolv.conf

# Set default route via gateway (all outbound traffic flows to gateway)
ip route replace default via $GATEWAY_IP
```

Gateway enables IP forwarding and redirects port 443 to its proxy:

```bash
# Gateway entrypoint
echo 1 > /proc/sys/net/ipv4/ip_forward
iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port 8443
```

Agent has no iptables binary installed. Even with root, it cannot create firewall rules. Route changes are useless since the internal network only reaches the gateway.

## Credential Flow

```
Agent → api.github.com:443
  → default route sends to gateway
  → Gateway PREROUTING redirects :443 to proxy :8443
  → Gateway reads SNI: "api.github.com"
  → Matches github plugin rule → MITM mode
  → Terminates TLS (sandbox CA), reads HTTP request
  → Strips dummy token, injects real PAT
  → Opens TLS to real api.github.com, forwards
  → Agent receives response (thinks it talked directly)
```

Agent never sees real credentials. Bridge gets dummy tokens. Real creds exist only in the gateway container.

## Docker Access

When `docker: true`, the docker plugin contributes a DinD sidecar. The gateway itself handles Docker API validation — no separate proxy container needed.

1. Agent runs `docker run ...` → connects to `dind:2375`
2. Gateway intercepts (default route, like all TCP)
3. Gateway's `DockerHandler` validates the request (block privileged, host binds)
4. Injects gateway redirect into spawned container config
5. Forwards to real DinD

Docker API is HTTP (not HTTPS), so no MITM/TLS needed — plain HTTP inspection.

Spawned containers:
- Forced onto internal network
- Default route points to gateway
- Cannot spawn further containers (no DOCKER_HOST env)

## Hardening

### Attack Surface

| Vector | Mitigation |
|--------|------------|
| Agent reads secrets via env/filesystem | ✓ Secrets only in gateway container. Agent container has no access. |
| Agent reads /proc to find credentials | ✓ Gateway is a different container (different PID namespace). |
| Agent gets root, reads secrets | ✓ Different container — root in agent cannot access gateway filesystem. |
| Agent gets root, bypasses proxy | ✓ Docker internal network has no internet route. Changing routes doesn't help. |
| Agent kills gateway | ✓ Different container. Agent cannot signal gateway process. |
| Agent modifies routes | Possible with root, but useless — no internet route exists. |
| DNS tunneling | DNS goes to gateway's resolver. No raw UDP to internet. |
| DinD direct access | DinD uses TLS client cert auth. Cert in gateway container only. |
| Resource exhaustion | mem_limit, cpus, pids_limit per container. |

### Container Security

```yaml
services:
  gateway:
    networks: [internal, default]
    cap_add: [NET_ADMIN]          # for IP forwarding + iptables PREROUTING
    read_only: true

  agent:
    networks: [internal]          # NO internet
    cap_add: [NET_ADMIN]          # for ip route setup at boot
    security_opt: [no-new-privileges:true]
    read_only: true
    tmpfs: [/tmp, /run]
    mem_limit: 4g
    cpus: 2
    pids_limit: 256
```

### Secrets Isolation

```
Gateway container:
  /etc/gateway/config.yaml    root:root       0444  (has credential mappings)
  /etc/gateway/ca.key         root:root       0400  (MITM signing key)
  Environment: TELEGRAM_BOT_TOKEN, GITHUB_PAT, etc.

Agent container:
  /home/agent/                agent:agent     0750  (writable, volume)
  Environment: GATEWAY_HOST (non-secret, for DNS resolution)
  NO credentials, NO CA key, NO gateway config
```

### Failure Modes

| Failure | Behavior |
|---------|----------|
| Gateway crashes | All TCP from agent fails (safe default). Docker restart policy recovers. |
| Agent can't resolve gateway | Entrypoint fails fast with clear error. Container won't start. |
| Bridge crashes | Agent dies (child). Docker restart policy recovers. |
| DinD crashes | Docker commands fail. Agent retries. |

## Security Comparison

| Threat | Single Container (old) | Separate Containers (current) |
|--------|----------------------|-------------------------------|
| Agent user reads secrets | ✗ Possible via /proc | ✓ Blocked (different container) |
| Agent root reads secrets | ✗ Possible | ✓ Blocked (different container) |
| Agent root bypasses proxy | ✗ Can modify iptables + reach internet | ✓ No internet route exists |
| Agent root kills gateway | ✗ Can signal gateway process | ✓ Different PID namespace |
