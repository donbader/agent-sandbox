# Unified Plugin Gateway Interface

**Date:** 2026-06-19
**Status:** Draft
**Scope:** Refactor plugin `contributes.gateway` to use the same egress rule format as user config

## Problem

The plugin gateway API uses a different schema from the user-facing `gateway.egress` config:

- **User config:** `gateway.egress` with `hosts`, `headers`, `deny_paths`, `target`, `network`
- **Plugin config:** `contributes.gateway.services` (url-based) + separate `contributes.gateway.middlewares` (with `domains` filter)

This split causes:
1. Bugs — middleware domains weren't auto-added to `mitm_domains` because they're in a different data path
2. Redundancy — plugins declare the same domain in `services[].url` AND `middlewares[].domains`
3. Cognitive load — two schemas for the same concept (gateway routing)

## Solution

Unify both interfaces under a single `egress` rule format. Plugins adopt the same schema users already use, with `middlewares` added as a field on the egress rule.

## Unified Schema

```yaml
# Identical format for user config (agent.yaml) and plugin contributions (plugin.yaml)
gateway:
  egress:
    - hosts: ["api.telegram.org"]
      headers:                          # optional — static header injection (implies MITM)
        Authorization: "Bearer ${TOKEN}"
      deny_paths:                       # optional — block specific paths (implies MITM)
        - "DELETE /dangerous/*"
      middlewares:                       # optional — TS middleware scripts (implies MITM)
        - script: "./src/token-rewrite.ts"
      target: "host:port"               # optional — forwarding destination
      network: "my-network"             # optional — compose network attachment
      deny: false                       # optional — block matching traffic
    - hosts: ["*"]                      # catch-all allow
```

### MITM Trigger

A rule triggers MITM if any of these fields are present:
- `headers`
- `deny_paths`
- `middlewares`

This is expressed in `NeedsMITM()`:
```go
func (r *EgressRule) NeedsMITM() bool {
    return len(r.Headers) > 0 || len(r.DenyPaths) > 0 || len(r.Middlewares) > 0
}
```

### Middleware Scope

Middlewares on a rule apply only to that rule's `hosts`. No separate `domains` field needed — it's implicit from the rule context.

## Struct Changes

### `internal/config/egress.go`

```go
type MiddlewareEntry struct {
    Script string `yaml:"script" json:"script" jsonschema:"required,title=script,description=Path to TypeScript middleware file"`
}

type EgressRule struct {
    Hosts       []string          `yaml:"hosts" json:"hosts" jsonschema:"required"`
    Deny        bool              `yaml:"deny,omitempty" json:"deny,omitempty"`
    Headers     map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
    DenyPaths   []string          `yaml:"deny_paths,omitempty" json:"deny_paths,omitempty"`
    Middlewares []MiddlewareEntry `yaml:"middlewares,omitempty" json:"middlewares,omitempty"`
    Network     string            `yaml:"network,omitempty" json:"network,omitempty"`
    Target      string            `yaml:"target,omitempty" json:"target,omitempty"`
}

func (r *EgressRule) NeedsMITM() bool {
    return len(r.Headers) > 0 || len(r.DenyPaths) > 0 || len(r.Middlewares) > 0
}
```

### `internal/plugin/types.go`

```go
// Before
type GatewayContrib struct {
    Services          []GatewayService    `yaml:"services"`
    NamespacedVolumes []string            `yaml:"namespaced_volumes"`
    RawVolumes        []string            `yaml:"raw_volumes"`
    Routes            []RouteEntry        `yaml:"routes"`
    Middlewares       []GatewayMiddleware `yaml:"middlewares"`
}

// After
type GatewayContrib struct {
    Egress            []config.EgressRule `yaml:"egress"`
    NamespacedVolumes []string            `yaml:"namespaced_volumes"`
    RawVolumes        []string            `yaml:"raw_volumes"`
    Routes            []RouteEntry        `yaml:"routes"`
}
```

Remove `GatewayService`, `GatewayMiddleware` types entirely.

### `internal/generate/v1/gateway_config.go`

- Remove `GatewayConfigOutput.MiddlewareDomains` field
- Remove the plugin services loop and separate middleware domain collection
- `BuildGatewayConfig` merges user egress rules + plugin egress rules (plugin rules inserted before catch-all)
- `WriteGatewayRuntimeConfig` relies on `NeedsMITM()` which now covers middlewares

## Plugin Migration

### Before (`github-pat/plugin.yaml`)

```yaml
contributes:
  gateway:
    services:
      - url: "https://api.github.com"
      - url: "https://github.com"
    middlewares:
      - script: "./src/github-auth.ts"
        domains: ["api.github.com", "github.com"]
```

### After

```yaml
contributes:
  gateway:
    egress:
      - hosts: ["api.github.com", "github.com"]
        middlewares:
          - script: "./src/github-auth.ts"
```

### Before (`mcp-oauth/plugin.yaml`)

```yaml
contributes:
  gateway:
    services:
{{- range $name, $cfg := .plugin.options.providers }}
      - url: "{{ index $cfg "mcp_url" }}"
{{- end }}
    middlewares:
      - script: "./src/oauth.ts"
```

### After

```yaml
contributes:
  gateway:
    egress:
{{- range $name, $cfg := .plugin.options.providers }}
      - hosts: ["{{ index $cfg "mcp_url" }}"]
        middlewares:
          - script: "./src/oauth.ts"
{{- end }}
```

The `hosts` field accepts both bare hostnames and full URLs. The generator normalizes URLs to hostnames at parse time using the existing `extractDomain()` utility (e.g. `https://mcp.notion.com/mcp` becomes `mcp.notion.com`). This avoids needing a template function for hostname extraction.

### Local plugin (`telegram-v2/plugin.yaml`)

```yaml
# Before
contributes:
  gateway:
    services:
      - url: https://api.telegram.org
    middlewares:
      - script: "./src/telegram-token-rewrite.ts"
        domains:
          - api.telegram.org

# After
contributes:
  gateway:
    egress:
      - hosts: ["api.telegram.org"]
        middlewares:
          - script: "./src/telegram-token-rewrite.ts"
```

## Migration Command

```bash
agent-sandbox migrate
```

Behavior:
1. Scan all reachable plugin.yaml files (built-in from core cache + local paths from installations)
2. Detect legacy `contributes.gateway.services` and/or top-level `contributes.gateway.middlewares`
3. Display the proposed transformation as a diff
4. Prompt for confirmation (y/n)
5. Rewrite files in place

The `generate` command itself will reject the old format with a clear error:

```
Error: plugin "telegram-v2" uses deprecated contributes.gateway.services format.
Run `agent-sandbox migrate` to automatically convert to the egress format.
```

## Gateway Runtime Impact

The runtime `plugins.yaml` format changes. Currently:

```yaml
plugins:
  - name: telegram-v2
    dir: /etc/gateway/plugins/telegram-v2
    options: { ... }
    middlewares:
      - script: ./src/telegram-token-rewrite.ts
        domains: ["api.telegram.org"]
```

After: the gateway binary needs to resolve middleware-to-domain mapping from `config.yaml` (which now has the association via egress rules) rather than from `plugins.yaml` domain filters.

### Gateway runtime `config.yaml` additions

```yaml
middlewares:
  - plugin: telegram-v2
    script: ./src/telegram-token-rewrite.ts
    hosts: ["api.telegram.org"]
```

The gateway binary already loads TypeScript middlewares and matches by domain — this just changes where the domain binding comes from (rule attachment vs. separate `domains` field in plugins.yaml).

## Scope

### In scope
- Add `Middlewares []MiddlewareEntry` to `EgressRule`
- Replace `GatewayContrib.Services` and `GatewayContrib.Middlewares` with `GatewayContrib.Egress`
- Update `BuildGatewayConfig` to merge plugin egress rules into user egress rules
- Update `NeedsMITM()` to include middlewares
- Update `WriteGatewayRuntimeConfig` (simplified — no separate middleware domain tracking)
- Update gateway runtime to accept middleware config from egress rules
- Update built-in plugins: `github-pat`, `mcp-oauth`
- Add `agent-sandbox migrate` command for local plugins
- Update `generate` to reject old format with helpful error
- Update documentation: plugins.md, gateway-egress.md, build-pipeline.md

### Out of scope
- Changes to non-gateway plugin contributions (runtime, sidecar)
- Changes to routes (remain separate — different concern)
- Changes to the gateway binary's core proxy logic (only config loading changes)

## Testing

- Unit tests for the new `NeedsMITM()` with middlewares
- Unit tests for merging plugin egress rules into user egress rules
- Unit tests for migration detection and transformation
- Integration test: generate with new plugin format → verify `config.yaml` has correct `mitm_domains`
- Integration test: generate with old plugin format → verify rejection with migration message

## Risks

- **Gateway runtime change** — the gateway binary needs to change how it loads middleware domain bindings. This is a coordinated change across generator and runtime.
- **Third-party plugins** — any external plugins using the old format will break. The `migrate` command and clear error message mitigate this.
