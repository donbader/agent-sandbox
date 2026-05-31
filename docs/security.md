# Security

## Architecture: Container Isolation

Gateway and agent run in **separate containers** connected by a Docker internal network. The agent container has no internet access — all traffic is forced through the gateway via iptables DNAT.

```
┌─ gateway container ─────────────────┐
│  Networks: [internal, default]      │
│  Holds: real credentials, CA key    │──── internet
│  Runs: gateway binary               │
└──────────────┬──────────────────────┘
               │ Docker internal network
┌──────────────┴──────────────────────┐
│  agent container                    │
│  Networks: [internal] ONLY          │
│  No secrets, no internet access     │
│  iptables DNAT → gateway            │
│  Runs: bridge + agent               │
└─────────────────────────────────────┘
```

## Egress Model

**Allow all by default.** MITM only for hosts where credential injection is needed. Everything else passes through with end-to-end TLS preserved.

Rationale: Dev agents need `npm install`, `pip install`, `curl` arbitrary URLs. Default deny creates too much friction.

## Transparent Proxy

Agent container resolves gateway IP via Docker DNS, then sets up iptables DNAT:

```bash
# Resolve gateway container IP (Docker DNS, before iptables)
GATEWAY_IP=$(getent hosts $GATEWAY_HOST | awk '{print $1}')

# HTTPS: DNAT to gateway container (kernel enforced, agent cannot bypass)
iptables -t nat -A OUTPUT -p tcp --dport 443 -j DNAT --to-destination $GATEWAY_IP:8443

# DNS: redirect to gateway's resolver (exclude Docker DNS for service discovery)
iptables -t nat -A OUTPUT -p udp --dport 53 ! -d 127.0.0.11 -j DNAT --to-destination $GATEWAY_IP:5353

# Drop all other UDP (prevent DNS tunneling)
iptables -A OUTPUT -p udp ! --dport 53 -j DROP
```

Even if the agent modifies iptables (requires root), it's still on a Docker internal network with **no route to the internet**. The only reachable host is the gateway container.

## Credential Flow

```
Agent → api.github.com:443
  → iptables DNAT to gateway container:8443
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
2. Gateway intercepts (iptables DNAT, like all TCP)
3. Gateway's `DockerHandler` validates the request (block privileged, host binds)
4. Injects gateway redirect into spawned container config
5. Forwards to real DinD

Docker API is HTTP (not HTTPS), so no MITM/TLS needed — plain HTTP inspection.

Spawned containers:
- Forced onto internal network
- iptables injected to redirect egress → gateway container
- Cannot spawn further containers (no DOCKER_HOST env)

## Hardening

### Attack Surface

| Vector | Mitigation |
|--------|------------|
| Agent reads secrets via env/filesystem | ✓ Secrets only in gateway container. Agent container has no access. |
| Agent reads /proc to find credentials | ✓ Gateway is a different container (different PID namespace). |
| Agent gets root, reads secrets | ✓ Different container — root in agent cannot access gateway filesystem. |
| Agent gets root, bypasses proxy | ✓ Docker internal network has no internet route. Modifying iptables doesn't help. |
| Agent kills gateway | ✓ Different container. Agent cannot signal gateway process. |
| Agent modifies iptables | Possible with root, but useless — no internet route exists. |
| DNS tunneling | DNS redirected to gateway's resolver. No raw UDP allowed. |
| DinD direct access | DinD uses TLS client cert auth. Cert in gateway container only. |
| Resource exhaustion | mem_limit, cpus, pids_limit per container. |

### Container Security

```yaml
services:
  gateway:
    networks: [internal, default]
    # Minimal attack surface — only runs gateway binary
    read_only: true

  agent:
    networks: [internal]          # NO internet
    cap_add: [NET_ADMIN]          # for iptables setup at boot
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
