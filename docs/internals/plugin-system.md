# Plugin System (Internals)

How the plugin system works under the hood. For creating plugins, see [Creating Plugins](../guides/creating-plugins.md).

## Architecture

Two plugin types:

- **Runtime presets** — pure YAML defining base image, packages, and CMD. One per agent.
- **Feature plugins** — YAML metadata with optional Go gateway middleware. Additive. Multiple per agent.

Key principle: plugin updates never require CLI upgrades. The CLI is a generic template engine that reads YAML and renders templates.

## Directory Structure

### Runtime Presets

```
core/presets/
  codex/runtime.yaml
  claude-code/runtime.yaml
  pi/runtime.yaml
```

### Feature Plugins

```
core/plugins/
  github-pat/
    plugin.yaml              ← metadata, options schema, contributions
    middlewares/
      github-pat-auth.go     ← Go template rendered at generate-time
    README.md
  home-override/
    plugin.yaml
    README.md
  ssh/
    plugin.yaml
    README.md
  mcp-oauth/
    plugin.yaml
    README.md
  agent-manager-acp/
    plugin.yaml
    agent-manager/           ← asset directory (TypeScript source)
    README.md
```

## Plugin Resolution

Plugin references are resolved by prefix:

| Prefix | Source | Example |
|--------|--------|---------|
| `@builtin/name` | Core tarball (fetched from GitHub Releases) | `@builtin/github-pat` |
| `./path` | Local filesystem relative to project dir | `./plugins/telegram` |
| Bare name | Rejected with error | `github-pat` (invalid) |

Local plugin paths are validated to not escape the project directory (path traversal blocked).

## Core Version Fetching

- CLI queries GitHub Releases API for `v*` tags
- Downloads `agent-sandbox-core-<version>.tar.gz` from release assets
- Caches at `~/.cache/agent-sandbox/core/<version>/` (Linux) or `~/Library/Caches/agent-sandbox/core/<version>/` (macOS)
- `core_version: latest` resolves to the newest tag (cached for 1 hour)

## Generation Pipeline (per agent)

1. Load agent.yaml (or fleet.yaml + per-agent agent.yaml)
2. Load `.env` for secret resolution
3. Fetch core version from GitHub Releases (cached)
4. Resolve plugins (`@builtin/` from core FS, `./` from local FS)
5. Parse `plugin.yaml`, validate options against schema
6. Check `requires` dependencies (fail if missing)
7. Extract plugin assets to `.build/plugins/`
8. Render all `contributes` fields as Go templates (with plugin options + agent context)
9. Merge contributions across all plugins (runtime, gateway, sidecar)
10. Render Dockerfile from template (preset + extra_builds + plugin contributions)
11. Render entrypoint.sh from template
12. Build gateway runtime config (collect MITM domains from service URLs)
13. Generate auth-header middleware from `headers` with `${ENV_VAR}` patterns
14. Copy/render custom middleware `.go` files into gateway build context
15. Extract gateway source into build context
16. Generate JSON Schema
17. Write docker-compose.yml

## Template Rendering

Plugin `contributes` fields are rendered as Go `text/template` with this context:

```go
type RenderContext struct {
    Plugin struct {
        Options map[string]any  // resolved option values
    }
    Agent map[string]any        // full agent.yaml as nested map
}
```

Template functions: `toJSON`, `asset`, `index`.

## Gateway Middleware Compilation

Custom Go middlewares are rendered as Go templates at generate-time. When Go is available, the generator copies the source tree to a temp directory, injects the rendered middleware files + a blank import, compiles the gateway binary, then cleans up. The source tree is never modified.

1. Generate-time: render `.go` template with plugin options (secrets resolved from `.env`)
2. Copy gateway source to a temp directory
3. Inject rendered `.go` files into `core/gateway/middlewares/custom/` within the temp dir
4. Add blank import so Go compiler picks up the custom package
5. Run `go build` in the temp dir to produce the gateway binary
6. Copy compiled binary to `.build/`, remove temp dir

Middleware uses the SDK:

```go
gateway.RegisterMiddleware(gateway.MiddlewareDef{
    Name:    "my-middleware",
    Domains: []string{"api.example.com"},
    Func: func(ctx *gateway.MiddlewareContext) error {
        ctx.Request.Header.Set("Authorization", "Bearer "+secret)
        return nil
    },
})
gateway.RegisterSecret(secret)  // redacted from logs
```

## Fleet Mode Merging

`LoadFleetAgents()` merges `shared` into each per-agent config:

- `shared.installations` prepended to per-agent installations (same plugin name → per-agent wins)
- `shared.gateway.services` prepended to per-agent services (same URL → per-agent wins)
- Each agent gets its own gateway container with independently compiled middleware

## Conflict Detection

- Two plugins declaring the same MITM domain: last one wins (order in `installations` matters)
- Two plugins contributing conflicting environment variables: last one wins
- Volume name collisions: use `{{ .agent.name }}` prefix to avoid fleet conflicts

## Option Validation

Plugin options are validated at generate-time against the `options` schema in `plugin.yaml`:

- Type checking (string, boolean, integer, array, object)
- Required field enforcement
- Default value application
- `${ENV_VAR}` patterns are resolved from `.env` before validation

Path-type options (e.g., `authorized_keys`, `home_directory`) are validated to not escape the project directory.
