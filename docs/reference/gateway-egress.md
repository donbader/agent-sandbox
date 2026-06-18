# Gateway Egress Rules

Egress rules control which external hosts the agent container can reach through the gateway. All outbound traffic is intercepted via iptables DNAT and routed through the gateway — egress rules determine what gets allowed, blocked, or modified.

## Configuration

Add `gateway.egress` to your `agent.yaml`:

```yaml
gateway:
  egress:
    - hosts: ["evil.com", "*.malware.net"]
      deny: true

    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
      deny_paths:
        - "DELETE /repos/*"
        - "DELETE /orgs/*"

    - hosts: ["registry.npmjs.org", "*.cloudfront.net", "pypi.org"]

    - hosts: ["*"]   # catch-all allow — remove this for deny-all-by-default
```

## How It Works

Rules are evaluated **in order**. First match wins.

| Rule type | Meaning |
|-----------|---------|
| `hosts: [x]` | Allow traffic, passthrough |
| `hosts: [x], headers: {...}` | Allow + MITM + inject credentials |
| `hosts: [x], deny: true` | Block traffic (connection reset) |
| `hosts: [x], deny_paths: [...]` | Allow host but block specific URL paths |
| No match | **Implicit deny** (connection dropped) |

### Catch-All Behavior

- **With** `hosts: ["*"]` at the end → permissive mode (only explicit `deny` rules block)
- **Without** catch-all → strict mode (only listed hosts are allowed)

## Host Patterns

| Pattern | Matches |
|---------|---------|
| `"api.github.com"` | Exact domain |
| `"*.github.com"` | Any subdomain + bare domain |
| `"10.0.0.0/8"` | IP addresses in CIDR range |
| `"*"` | Everything (catch-all) |

## Headers (Credential Injection)

When a rule has `headers`, the gateway performs TLS MITM on that domain to inject the specified headers into every request:

```yaml
- hosts: ["api.anthropic.com"]
  headers:
    x-api-key: "${ANTHROPIC_API_KEY}"
    anthropic-version: "2024-01-01"
```

Headers support `${ENV_VAR}` syntax — the env var is resolved at gateway runtime, never baked into images.

## Deny Paths

Block specific URL paths while allowing the host generally. Requires MITM (automatically enabled):

```yaml
- hosts: ["api.github.com"]
  headers:
    Authorization: "Bearer ${GITHUB_PAT}"
  deny_paths:
    - "DELETE /repos/*"        # Block DELETE to /repos/<anything>
    - "/orgs/*/members"        # Block any method to this path
    - "/admin/*"               # Block entire /admin/ subtree
```

Path pattern formats:
- `"/path/glob"` — matches any HTTP method
- `"METHOD /path/glob"` — matches only the specified method

Path matching uses glob syntax (`*` matches one path segment, not `/`).

## Fleet (Shared) Configuration

In `fleet.yaml`, shared egress rules apply to all agents unless the agent defines its own:

```yaml
# fleet.yaml
shared:
  gateway:
    egress:
      - hosts: ["*.github.com"]
        headers:
          Authorization: "Bearer ${GITHUB_PAT}"
      - hosts: ["*"]
```

**Override behavior:** If an agent defines `gateway.egress`, it **fully replaces** the shared rules (not merged). This is because rule order matters and merging could produce surprising first-match-wins behavior.

## Migration from `gateway.services`

The old `gateway.services` format is deprecated. During `agent-sandbox generate`, you'll be prompted to migrate:

```
⚠️  Agent "coder" uses deprecated gateway.services format.
   The new gateway.egress format provides whitelist/blacklist control.

   Migrate agent.yaml? [Y/n]
```

Use `--migrate` flag for automatic migration without prompts.

### Manual Migration

Old format:
```yaml
gateway:
  services:
    - url: https://api.github.com
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - url: https://api.anthropic.com
      headers:
        x-api-key: "${ANTHROPIC_KEY}"
```

New format:
```yaml
gateway:
  egress:
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
    - hosts: ["api.anthropic.com"]
      headers:
        x-api-key: "${ANTHROPIC_KEY}"
    - hosts: ["*"]   # preserves old default-allow behavior
```

## Examples

### Strict whitelist (recommended for production)

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

No catch-all → agent can **only** reach these hosts.

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

Everything allowed except explicitly blocked hosts.

### Mixed: allow with path restrictions

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

Agent can use OpenAI API but can't fine-tune models, upload files, or delete models.
