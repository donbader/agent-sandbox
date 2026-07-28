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
| VPN routing | `vpn` | Route traffic through a named VPN profile (`socks5` or `openvpn`) |

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
| `type` | `string` | **Required.** VPN type: `socks5` or `openvpn`. |
| `address` | `string` | **Required for `socks5`.** Proxy address (`host:port`, e.g. `"vpn-container:1080"`). |
| `config_b64` | `string` | **Required for `openvpn`.** Base64-encoded `.ovpn` client config file. Use a `${VAR}` reference to keep secrets out of the config file (e.g. `config_b64: ${CORP_VPN_OVPN_B64}`). Generate with: `base64 -w0 < client.ovpn`. |

### Type: socks5

Routes traffic through a SOCKS5 proxy. The proxy must already be running and reachable from the gateway container.

- Traffic is dialled through the SOCKS5 proxy instead of directly.
- The proxy resolves the destination hostname — preserving split-tunnel DNS semantics.
- Only no-authentication SOCKS5 is supported.
- Any VPN client that exposes a SOCKS5 listener works (gluetun, redsocks, 3proxy, Dante, etc.).

### Type: openvpn

Starts an OpenVPN daemon **inside the gateway container** at startup and routes matching traffic through the tunnel via Linux `SO_BINDTODEVICE`.

- The gateway image is automatically built with `openvpn` and `iproute2` when any `openvpn` profile is configured.
- The Docker Compose gateway service automatically gains `devices: [/dev/net/tun:/dev/net/tun]`.
- Each profile is assigned a deterministic tun interface (`tun0` for the first alphabetical profile, `tun1` for the second, etc.).
- Tunnel startup is **non-blocking** — the gateway starts immediately and connects tunnels asynchronously with up to 3 retry attempts. If a tunnel fails, the gateway stays healthy and traffic to VPN-routed hosts falls back to direct connection (with a warning log). Use the `/vpn/reconnect` endpoint to retry.
- Works with Pritunl, AWS Client VPN, and any standard OpenVPN server.

#### VPN Management Endpoints

The gateway exposes HTTP endpoints on `:8080` for monitoring and controlling VPN tunnels. These are accessible from the sandbox network so the agent can self-heal when VPN connections drop.

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/vpn/status` | `GET` | Returns JSON array of all tunnel statuses |
| `/vpn/reconnect` | `POST` | Reconnect all tunnels |
| `/vpn/reconnect?profile=<name>` | `POST` | Reconnect a specific tunnel |

**Status response:**

```json
[
  {
    "profile": "corp",
    "interface": "tun0",
    "state": "connected",
    "since": "2026-07-28T13:28:51Z",
    "attempts": 1
  }
]
```

Possible `state` values: `disconnected`, `connecting`, `connected`, `failed`.

**Reconnect from the host:**

```bash
# Check status
docker exec <gateway-container> wget -qO- http://localhost:8080/vpn/status

# Reconnect all tunnels
docker exec <gateway-container> wget -qO- --post-data='' http://localhost:8080/vpn/reconnect

# Reconnect a specific profile
docker exec <gateway-container> wget -qO- --post-data='' 'http://localhost:8080/vpn/reconnect?profile=corp'
```

**Agent skill (copy-paste):**

If your agent supports skills (e.g. omp, Claude Code, OpenCode), drop this file into your agent's skills directory to enable VPN self-healing. The agent will automatically check VPN status and reconnect when VPN-routed requests fail.

<details>
<summary>skills/vpn-ops/SKILL.md</summary>

````markdown
---
name: vpn-ops
description: Check VPN tunnel status and reconnect when VPN-routed requests fail. Use when seeing connection timeouts to VPN-routed hosts, when asked about VPN status, or when configuring VPN profiles.
---

# VPN Operations

Manage OpenVPN tunnels through the gateway's VPN management endpoints.

## When to Use

- Requests to VPN-routed hosts timeout or return connection errors
- User asks about VPN status or connectivity
- After container restart, to verify tunnels are healthy
- Setting up or modifying VPN configuration

## Configuration

### agent.yaml / fleet.yaml

```yaml
gateway:
  vpn_profiles:
    corp:
      type: openvpn
      config_b64: ${CORP_VPN_OVPN_B64}      # base64-encoded .ovpn file
      username: ${CORP_VPN_USERNAME}          # optional: OpenVPN auth username
      totp_secret: ${CORP_VPN_TOTP_SECRET}   # optional: base32 TOTP secret for 2FA

  egress:
    - hosts: ["internal-api.example.com"]
      vpn: corp                               # route through "corp" profile
      headers:
        Authorization: "Bearer ${API_TOKEN}"
```

### .env

```bash
# Encode your .ovpn config file:
#   Linux:  base64 -w0 < client.ovpn
#   macOS:  base64 -i client.ovpn
CORP_VPN_OVPN_B64=<base64-encoded .ovpn content>

# Optional: for servers requiring username/password + TOTP
CORP_VPN_USERNAME=myuser
CORP_VPN_TOTP_SECRET=JBSWY3DPEHPK3PXP   # base32-encoded, no padding needed
```

### Key Points

- `type` must be `openvpn` (or `socks5` for SOCKS5 proxy)
- `config_b64` is the ONLY required field for basic OpenVPN
- `username` + `totp_secret` enable auto-auth via OpenVPN management interface (fresh TOTP generated on each connect/reconnect)
- Each profile gets a deterministic tun interface: first alphabetically → `tun0`, second → `tun1`
- Reference profiles from egress rules with `vpn: <profile-name>`
- VPN profiles must be defined BEFORE they are referenced in egress rules

### After Configuration

```bash
agent-sandbox generate            # regenerate .build/
agent-sandbox compose up --build -d
```

The gateway will:
1. Start immediately (health endpoint responds right away)
2. Connect VPN tunnels asynchronously in background (3 retries, exponential backoff)
3. Log tunnel status: `vpn tunnel connected` or `vpn tunnel failed after retries`

## Commands

```bash
# Check status of all tunnels
curl -s "http://${GATEWAY_HOST}:8080/vpn/status" | jq .

# Reconnect all tunnels
curl -s -X POST "http://${GATEWAY_HOST}:8080/vpn/reconnect"

# Reconnect a specific profile
curl -s -X POST "http://${GATEWAY_HOST}:8080/vpn/reconnect?profile=<name>"
```

## Status Response

```json
[
  {
    "profile": "corp",
    "interface": "tun0",
    "state": "connected",
    "since": "2026-07-28T13:28:51Z",
    "attempts": 1
  }
]
```

States: `connected`, `connecting`, `failed`, `disconnected`.

## Procedure

### 1. Diagnose

```bash
curl -s "http://${GATEWAY_HOST}:8080/vpn/status" | jq .
```

- `connected` → VPN is fine, problem is elsewhere
- `failed` → trigger reconnect
- `connecting` → wait 30s then check again

### 2. Reconnect

```bash
curl -s -X POST "http://${GATEWAY_HOST}:8080/vpn/reconnect?profile=<name>"
```

Wait 15-30 seconds, then verify status.

### 3. If Still Failing

1. Check error: `curl -s "http://${GATEWAY_HOST}:8080/vpn/status" | jq '.[].error'`
2. Try reconnecting one more time
3. If still failing, inform the user that the VPN server may be unreachable

## Multi-Profile

- `GET /vpn/status` returns all profiles (sorted alphabetically)
- `POST /vpn/reconnect` (no param) reconnects ALL profiles
- `POST /vpn/reconnect?profile=X` reconnects only X

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `tun interface did not come up` | VPN server unreachable or UDP blocked | Check network, retry |
| `openvpn start failed` | Bad config or missing /dev/net/tun | Verify config_b64, check `devices` in compose |
| `decode config_b64` | Invalid base64 encoding | Re-encode: `base64 -w0 < file.ovpn` |
| Status stuck on `connecting` | Slow VPN server, TOTP clock skew | Wait for retries to finish, check system time |
| Connected but requests fail | DNS not routing through VPN | Verify `vpn:` field on the egress rule |

## Notes

- Reconnect is async — always poll `/vpn/status` to confirm
- TOTP codes are generated automatically on each attempt
- Don't spam reconnect — each attempt takes up to 30s (3 retries)
- When VPN is down, traffic falls back to direct (will likely fail for VPN-only hosts)
- The gateway container needs `devices: [/dev/net/tun:/dev/net/tun]` and `cap_add: [NET_ADMIN]` — these are auto-configured when any openvpn profile is present
````

</details>

**Encode your .ovpn file:**
```bash
base64 -w0 < client.ovpn   # Linux
base64 -i client.ovpn      # macOS
```

**Example (Pritunl/OpenVPN):**
```yaml
gateway:
  vpn_profiles:
    corp:
      type: openvpn
      config_b64: ${CORP_VPN_OVPN_B64}  # base64-encoded .ovpn file

  egress:
    - hosts: ["agw.playground.straitsx.ai"]
      vpn: corp
      headers:
        Authorization: "Bearer ${AGW_BEARER_TOKEN}"
    - hosts: ["*"]
```

**.env:**
```bash
CORP_VPN_OVPN_B64=$(base64 -w0 < ~/Downloads/client.ovpn)
AGW_BEARER_TOKEN=eyJhbGc...
```

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
