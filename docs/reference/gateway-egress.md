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
| `deny_graphql` | `object` | Block specific GraphQL mutations. Implies MITM. |
| `middlewares` | `[]string` | TypeScript middleware scripts. Implies MITM. |
| `target` | `string` | Forwarding destination (`host:port`) for internal/HTTP services |
| `network` | `string` | Compose network to attach gateway to (for reaching internal services) |
| `vpn` | `string` | VPN profile name. Routes matching traffic through the named proxy. |

### Field Responsibilities

| Concern | Field | Layer |
|---------|-------|-------|
| Matching | `hosts` | Which outbound connections trigger this rule |
| Decision | `deny` | Block at L4 (no TLS termination, cheap) |
| Request modification | `headers`, `deny_paths`, `deny_graphql`, `middlewares` | Inject creds or block paths/mutations at L7 (requires MITM) |
| Routing | `target` | Where to forward traffic (default: passthrough on :443) |
| Infrastructure | `network` | Docker network attachment for compose generation |
| VPN routing | `vpn` | Route traffic through a named VPN proxy (SOCKS5) |

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

## Deny GraphQL

Block specific GraphQL mutations while allowing the host. Useful when `deny_paths` can't distinguish operations — all GraphQL traffic shares a single `POST /graphql` endpoint. Requires MITM (auto-enabled):

```yaml
- hosts: ["api.github.com"]
  headers:
    Authorization: "Bearer ${GITHUB_PAT}"
  deny_graphql:
    mutations:
      - "mergePullRequest"
      - "deleteBranch"
```

The gateway inspects POST requests to paths containing `graphql`, extracts all candidate mutation names from the request body, and returns 403 if any of them match the deny list. Matching is case-insensitive. Candidate names are extracted from:

1. The `operationName` JSON field (if present)
2. The named operation in the `query` string (e.g. `mutation PullRequestMerge(...)` → `PullRequestMerge`)
3. The first field name inside the mutation body (e.g. `{ mergePullRequest(...) }` → `mergePullRequest`)

This ensures mutations are blocked regardless of how the client names the operation. For example, `gh pr merge` uses `mutation PullRequestMerge(...){mergePullRequest(...)}` — the deny rule `mergePullRequest` matches the field name even though the operation name differs.

If the request body cannot be parsed as JSON, the request is passed through (fail open).

`deny_graphql` cannot be combined with `deny: true` — if you want to block the host entirely, use `deny: true` alone.

## Middlewares

Attach TypeScript middleware scripts to a rule. Middlewares fire for requests matching the rule's `hosts` and imply MITM (TLS termination):

```yaml
- hosts: ["api.example.com"]
  middlewares:
    - "./src/auth.ts"
```

Each entry is a path to a TypeScript file (relative to plugin or project root).

Middlewares run in order before the request is forwarded upstream. They can modify headers, abort requests, or perform credential injection. See [Plugin Authoring](../plugins.md) for the middleware handler API.

Plugins use this same format in `contributes.gateway.egress` — there is no separate `middlewares` or `services` section.

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

## VPN Profiles

Route specific egress traffic through a VPN proxy by defining named profiles and referencing them from egress rules.

### Configuration

```yaml
gateway:
  vpn_profiles:
    corp-vpn:
      type: socks5
      address: "vpn-container:1080"

  egress:
    - hosts: ["internal.corp.com", "*.corp.internal"]
      vpn: corp-vpn
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["*"]
```

### VPN Profile Fields

| Field | Type | Purpose |
|-------|------|---------|
| `type` | `string` | **Required.** Proxy protocol. Only `socks5` is supported. |
| `address` | `string` | **Required.** Proxy address (`host:port`, e.g. `"vpn-container:1080"`). |

### How It Works

- Traffic to hosts matched by a rule with `vpn:` set is dialled through the named SOCKS5 proxy instead of directly.
- Works for both passthrough TLS connections and MITM-terminated connections.
- The SOCKS5 proxy resolves the destination hostname — the gateway never resolves it locally, which preserves split-tunnel DNS semantics.
- Only no-authentication SOCKS5 is supported. Configure your VPN container to accept unauthenticated connections from the sandbox network.

### Constraints

- `vpn` cannot be combined with `deny: true`. Use separate rules.
- VPN profile names must be defined in `vpn_profiles` before they can be referenced by egress rules.
- Each profile name must be unique.

### Example: split-tunnel with internal services

```yaml
gateway:
  vpn_profiles:
    office-vpn:
      type: socks5
      address: "gluetun:1080"

  egress:
    - hosts: ["*.internal.example.com"]
      vpn: office-vpn
    - hosts: ["jira.example.com", "confluence.example.com"]
      vpn: office-vpn
      headers:
        Authorization: "Bearer ${JIRA_TOKEN}"
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["*"]
```
