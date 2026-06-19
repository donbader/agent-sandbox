# Gateway Egress Rules

Control which hosts the agent can reach, inject credentials, and block specific paths — all through ordered rules evaluated at the gateway.

```yaml
gateway:
  egress:
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["registry.npmjs.org", "pypi.org"]
    - hosts: ["*"]
```

## Rule Evaluation

Rules are evaluated **in order**. First match wins. No match = **implicit deny**.

- End with `hosts: ["*"]` for permissive mode (allow-all, only explicit `deny` blocks)
- Omit catch-all for strict mode (only listed hosts are reachable)

## Field Reference

| Field | Type | Purpose |
|-------|------|---------|
| `hosts` | `[]string` | **Required.** Host patterns to match (domain globs, CIDRs, `"*"`) |
| `deny` | `bool` | Block matching traffic at TCP layer (connection reset) |
| `headers` | `map[string]string` | Inject headers via MITM. Implies allow. |
| `deny_paths` | `[]string` | Block specific URL paths. Implies MITM. |
| `target` | `string` | Forwarding destination (`host:port`) for internal/HTTP services |
| `network` | `string` | Compose network to attach gateway to (for reaching internal services) |

### Field Responsibilities

| Concern | Field | Layer |
|---------|-------|-------|
| Matching | `hosts` | Which outbound connections trigger this rule |
| Decision | `deny` | Block at L4 (no TLS termination, cheap) |
| Request modification | `headers`, `deny_paths` | Inject creds or block paths at L7 (requires MITM) |
| Routing | `target` | Where to forward traffic (default: passthrough on :443) |
| Infrastructure | `network` | Docker network attachment for compose generation |

## Host Patterns

| Pattern | Matches |
|---------|---------|
| `"api.github.com"` | Exact domain |
| `"*.github.com"` | Any subdomain + bare domain |
| `"10.0.0.0/8"` | IP addresses in CIDR range |
| `"*"` | Everything (catch-all) |

## Headers

Inject credentials into requests via TLS MITM:

```yaml
- hosts: ["api.anthropic.com"]
  headers:
    x-api-key: "${ANTHROPIC_API_KEY}"
    anthropic-version: "2024-01-01"
```

`${ENV_VAR}` syntax is resolved at gateway runtime — secrets never baked into images.

## Deny Paths

Block specific URL paths while allowing the host. Requires MITM (auto-enabled):

```yaml
- hosts: ["api.github.com"]
  headers:
    Authorization: "Bearer ${GITHUB_PAT}"
  deny_paths:
    - "DELETE /repos/*"
    - "/orgs/*/members"
    - "/admin/*"
```

Formats:
- `"/path/glob"` — any method
- `"METHOD /path/glob"` — specific method only

## Internal Services (target + network)

For services on non-standard ports or separate Docker networks:

```yaml
- hosts: ["rkgw"]
  target: "rkgw:8765"
  network: rkgw-external
  headers:
    x-api-key: "${RKGW_API_KEY}"
```

- `target` — tells gateway where to forward HTTP traffic (omit for standard HTTPS passthrough on :443)
- `network` — attaches gateway container to that Docker network so it can reach the target

Services already on the sandbox network don't need `network`.

## Fleet Configuration

Shared egress in `fleet.yaml`:

```yaml
shared:
  gateway:
    egress:
      - hosts: ["*.github.com"]
        headers:
          Authorization: "Bearer ${GITHUB_PAT}"
      - hosts: ["*"]
```

Per-agent `gateway.egress` **fully replaces** shared rules (not merged). Rule order matters — additive merging would produce surprising first-match-wins behavior.

## Migration from `gateway.services`

`gateway.services` is deprecated. Run `agent-sandbox generate` to be prompted, or use `--migrate` for automatic conversion.

Before:
```yaml
gateway:
  services:
    - url: https://api.github.com
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - url: rkgw:8765
      network: rkgw-external
      headers:
        x-api-key: "${RKGW_API_KEY}"
```

After:
```yaml
gateway:
  egress:
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["rkgw"]
      target: "rkgw:8765"
      network: rkgw-external
      headers:
        x-api-key: "${RKGW_API_KEY}"
    - hosts: ["*"]   # preserves old default-allow behavior
```

## Examples

### Strict whitelist

```yaml
gateway:
  egress:
    - hosts: ["api.github.com", "github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["api.anthropic.com"]
      headers:
        x-api-key: "${ANTHROPIC_KEY}"
    - hosts: ["registry.npmjs.org", "*.cloudfront.net"]
    - hosts: ["pypi.org", "files.pythonhosted.org"]
```

No catch-all → only listed hosts are reachable.

### Permissive with blocklist

```yaml
gateway:
  egress:
    - hosts: ["*.malware.net", "crypto-miner.io"]
      deny: true
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["*"]
```

### Path restrictions

```yaml
gateway:
  egress:
    - hosts: ["api.openai.com"]
      headers:
        Authorization: "Bearer ${OPENAI_KEY}"
      deny_paths:
        - "/v1/fine_tuning/*"
        - "/v1/files/*"
        - "DELETE /v1/models/*"
    - hosts: ["*"]
```

### Internal service with network

```yaml
gateway:
  egress:
    - hosts: ["agent-gateway.stx-ai.net"]
      headers:
        Authorization: "Bearer ${STX_KEY}"
    - hosts: ["rkgw"]
      target: "rkgw:8765"
      network: rkgw-external
      headers:
        x-api-key: "${RKGW_API_KEY}"
    - hosts: ["*"]
```
