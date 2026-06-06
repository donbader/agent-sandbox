# V1 Architecture Redesign

## Summary

Redesign agent-sandbox from a plugin-heavy framework into a generic, self-contained container orchestrator. Users configure their agent setup in their own repos. The current channel-manager, telegram integration, and ACP wrapping move out of core and become an example plugin.

## Core Philosophy

agent-sandbox is a generic container orchestrator with three built-in concerns:

1. **Runtime** — a configurable agent container (base image preset or custom, with layered build steps and user-defined entrypoint)
2. **Gateway** — transparent egress proxy. The single network path out of any container. Supports middleware (credential injection, rewriting, allowlisting).
3. **Plugin system** — plugins contribute to runtime, gateway, and/or sidecars. Users install them declaratively.

## CLI / Core Separation

The project splits into two independently versioned components in the same repo:

- **CLI** (`cmd/agent-sandbox/`) — local orchestration tool. Parses `agent.yaml`, resolves plugins, renders templates, generates `.build/`. Installed on the user's machine.
- **Core** (`core/`) — versioned artifact used at generate-time. Contains: gateway source, runtime preset definitions, middleware SDK, compose/Dockerfile templates.

```
cmd/agent-sandbox/     ← CLI (tagged: cli/v2.1.0)
core/                  ← Core (tagged: core/v1.2.0)
  gateway/             ← gateway Go source
  presets/             ← runtime preset definitions (YAML)
  sdk/                 ← middleware interface (Go)
  templates/           ← compose/Dockerfile Go templates
  plugins/             ← bundled plugins (YAML)
```

**Version tags:** `cli/vX.Y.Z` and `core/vX.Y.Z` — independent release cadences.

**How `core_version` works:**

1. User sets `core_version: v1.2.0` in `agent.yaml`
2. CLI checks local cache for that core version
3. If not cached, fetches the release asset (tarball) from this repo's GitHub releases tagged `core/v1.2.0`
4. Uses that core's gateway source, presets, templates, and SDK to generate `.build/`

This means:
- Users pin to a stable core while upgrading the CLI freely
- Core updates (gateway improvements, new presets, SDK changes) ship independently
- A newer CLI can generate for older cores (backward compat)
- Bundled plugins ship with the core, not the CLI

## Config Shape (`agent.yaml`)

```yaml
name: my-agent-001
log_level: debug
core_version: v1.0.0

runtime:
  image: "@builtin/codex"
  extra_builds:
    - "COPY ./scripts /opt/scripts"
  entrypoint: ["codex-acp", "--listen", ":8080"]
  volumes:
    - "my-volume:/opt/something-to-persist"

gateway:
  services:
    - url: "rkgw:8765"
      network: rkgw-external
      headers:
        x-api-key: ${RKGW_API_KEY}
    - url: https://agent-gateway.stx-ai.net
      headers:
        Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}

installations:
  - plugin: home-override
    options:
      home-directory: "./home"
      volume: true
  - plugin: github-pat
    options:
      token: "${GITHUB_PAT}"
  - plugin: mcp-oauth
    options:
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
  - plugin: telegram-acp
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      acp: "@agentclientprotocol/codex-acp"
```

### Config Fields

| Field | Description |
|-------|-------------|
| `name` | Agent instance name |
| `log_level` | Verbosity: `info`, `debug` |
| `core_version` | Minimum agent-sandbox version required |
| `runtime.image` | Base image. `@builtin/codex`, `@builtin/claude-code`, `@builtin/pi`, or any Docker image |
| `runtime.extra_builds` | Additional Dockerfile instructions layered after the base |
| `runtime.entrypoint` | Container CMD override |
| `runtime.volumes` | Named or bind mount volumes for the agent container |
| `gateway.services` | External services the agent may access. Each gets proxied through the gateway |
| `installations` | Plugins to install, each with typed options |

### Gateway Service Entry

```yaml
- url: https://api.example.com       # HTTPS endpoint, or plain host:port for sidecars
  network: optional-external-network  # Compose network to attach (optional)
  headers:                            # Headers injected by gateway on every request
    Authorization: Bearer ${TOKEN}
```

## Plugin Contribution Model

All plugins are declarative YAML with Go template expressions. A plugin receives its `Options` and declares contributions to any combination of three surfaces:

```yaml
# plugin.yaml structure
name: my-plugin
options:
  # schema definition
contributes:
  runtime:
    extra_builds: []
  gateway:
    services: []
    volumes: []
  sidecar:
    services: {}
```

The equivalent Go types for the CLI's internal model:

```go
type SchemaOptions map[string]any

type Plugin struct {
    Options SchemaOptions
    Runtime RuntimeContrib
    Gateway GatewayContrib
    Sidecar SidecarsContrib
}

type RuntimeContrib struct {
    ExtraBuilds []string // Additional Dockerfile instructions
}

type GatewayContrib struct {
    Services []GatewayService
    Volumes  []string // e.g., mount OAuth secret files into gateway
}

type GatewayService struct {
    URL         string
    Network     string
    Headers     map[string]string
    Middlewares []CustomMiddleware // custom .go files compiled into gateway
}

type CustomMiddleware struct {
    Path string // relative path to .go file (e.g., "./middlewares/my-auth.go")
}

type SidecarsContrib struct {
    Services map[string]ComposeService // Follows docker-compose service spec
}
```

### Contribution Surfaces

| Surface | What it adds | Example |
|---------|-------------|---------|
| **Runtime** | Dockerfile lines on top of base image | Install extra packages, copy config files |
| **Gateway** | Services to proxy + middleware | `github-pat` adds github.com with bearer token injection |
| **Sidecar** | Additional compose services | `telegram-acp` adds a channel-manager container |

### How Contributions Merge

The final generated output is the merge of:
- User's `runtime` config (base)
- User's `gateway.services` (direct declarations)
- All installed plugins' contributions (layered in order)

The CLI generates:
- `Dockerfile` — runtime image + all RuntimeContrib layers
- `docker-compose.yaml` — agent + gateway + all SidecarContrib services
- Gateway config — user services + all GatewayContrib services, with middleware chains

## Network Model

```
┌─────────────────┐     ┌─────────────────┐     ┌──────────────┐
│ Agent Container │────▶│    Gateway      │────▶│  Internet    │
└─────────────────┘     │                 │     └──────────────┘
                        │  - MITM TLS     │
┌─────────────────┐     │  - Middleware   │     ┌──────────────┐
│ Sidecar(s)      │────▶│  - Allowlist    │────▶│  Sidecars    │
└─────────────────┘     └─────────────────┘     └──────────────┘
```

- Agent container has **no direct network access** except to the gateway
- Sidecar containers have **no direct network access** except to the gateway
- All egress (internet, cross-container) goes through the gateway
- Gateway applies middleware (credential injection, rewriting, logging) on every request
- Sidecars expose themselves to the agent via gateway service entries (`<sidecar>:<port>`)

## Runtime Presets

Built-in presets provide the base image + core package installation:

| Preset | What it provides |
|--------|-----------------|
| `@builtin/codex` | node:24-slim + codex CLI |
| `@builtin/claude-code` | node:24-slim + claude-code CLI |
| `@builtin/pi` | node:24-slim + pi CLI |

Users can also specify any Docker image directly:
```yaml
runtime:
  image: python:3.12-slim
```

Presets only provide the base. Users add ACP adapters, tools, and entrypoint via `extra_builds` and `entrypoint`, or via plugins.

## What Leaves Core

| Current Location | Destination |
|-----------------|-------------|
| `channel-manager/` | External plugin / example (`telegram-acp`) |
| `internal/plugins/telegram/` | External plugin / example (`telegram-acp`) |
| `internal/plugins/custom-runtime/` | Replaced by `runtime.extra_builds` and `runtime.entrypoint` |
| `internal/plugins/*.go` registration pattern | Replaced by declarative YAML plugin format |

## What Stays in Core

**In the CLI:**
- Config parsing (`agent.yaml` → typed struct)
- Plugin resolution (bundled / local / remote)
- YAML parsing + Go template rendering
- Contribution merging (runtime + gateway + sidecar surfaces)
- Generate command (produces `.build/`)
- Compose passthrough command
- Core version fetching + caching

**In the Core:**
- Runtime presets (`@builtin/codex`, `@builtin/claude-code`, `@builtin/pi`)
- Gateway engine source (transparent proxy, MITM TLS, middleware registration)
- Middleware SDK (the `gateway.MiddlewareContext` interface)
- Bundled plugins (`github-pat`, `mcp-oauth`, `home-override`)
- Compose/Dockerfile templates

## Built-in Plugins (bundled with Core)

These ship with the core artifact for convenience. Same YAML format as any other plugin.

| Plugin | Contributes | Purpose |
|--------|-------------|---------|
| `home-override` | Runtime | Custom home directory + optional volume persistence |
| `github-pat` | Gateway | GitHub.com with PAT injection middleware |
| `mcp-oauth` | Gateway + Runtime | MCP OAuth providers, secret file mounts |

Users can achieve the same by writing their own `plugin.yaml` — bundled plugins are just pre-packaged common patterns.

## External Plugins (user-land)

External plugins live outside the CLI. Same YAML format as bundled plugins. They can be:
- In the user's own repo (local path)
- In a separate repo (pulled at generate-time)

Example: `telegram-acp` plugin

```
plugins/telegram-acp/
  plugin.yaml         # metadata, options schema, contributions
  middlewares/
    telegram-token-rewrite.go  # custom gateway middleware (optional)
  sidecar/
    Dockerfile
    src/
      index.ts        # channel-manager + telegram channel code
      ...
```

## Example User Repo Structure

```
my-agent-setup/
  agent.yaml                    # config
  home/                         # custom agent home (if home-override with local dir)
  plugins/                      # local external plugins (optional)
    telegram-acp/
      plugin.yaml
      sidecar/
        Dockerfile
        src/...
```

Or using a published plugin:
```yaml
installations:
  - plugin: telegram-acp
    source: github.com/donbader/agent-sandbox-telegram@v1.0.0
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
```

## Migration from V0

- V1 is developed on a separate branch
- V0 config (`features` array, `runtime: codex` shorthand) is not supported in V1
- Migration path: rewrite `agent.yaml` using new schema (straightforward 1:1 mapping)
- Examples are updated to demonstrate new config shape
- Old `channel-manager/` and `internal/plugins/telegram/` code moves into `examples/telegram-acp/` as reference

## Resolved Design Decisions

### Plugin Language

All plugins — built-in or external — use the same declarative YAML format with Go template expressions. No Go compilation for plugin logic. The only difference is where they live:

- **Bundled plugins** — embedded in the CLI binary (convenience, ship out of the box)
- **Local plugins** — in the user's repo (e.g., `./plugins/my-plugin/plugin.yaml`)
- **Remote plugins** — fetched from a git repo at generate-time

A plugin is a `plugin.yaml` that declares its options schema and contributions using templates:

```yaml
name: telegram-acp
options:
  bot_token:
    type: string
    required: true
    description: "Telegram bot token"
  acp:
    type: string
    required: false
    default: "@agentclientprotocol/codex-acp"
    description: "ACP adapter npm package"

contributes:
  runtime:
    extra_builds:
      - "RUN npm install -g {{ .options.acp }}"
  gateway:
    services:
      - url: https://api.telegram.org
        middlewares:
          - custom: "./middlewares/telegram-token-rewrite.go"
  sidecar:
    services:
      telegram:
        build: ./sidecar
        environment:
          TELEGRAM_BOT_TOKEN: "{{ .options.bot_token }}"
          AGENT_ACP_URL: "http://gateway:8080/agent"
```

If a plugin needs logic beyond what templates can express, it ships a sidecar that handles the logic (in any language).

### Plugin Options Schema

Declared inline in `plugin.yaml` using a JSON Schema subset:

```yaml
options:
  bot_token:
    type: string
    required: true
    description: "Telegram bot token"
  access_control:
    type: object
    properties:
      allowed_users:
        type: array
        items: { type: string }
```

The CLI validates user-provided options against this schema at generate-time.

### Sidecar Health Checks

The plugin's compose fragment handles it. Sidecar service definitions follow docker-compose spec, which includes `healthcheck`:

```yaml
sidecar:
  services:
    telegram:
      build: ./sidecar
      healthcheck:
        test: ["CMD", "curl", "-f", "http://localhost:3000/health"]
        interval: 10s
        timeout: 5s
        retries: 3
      depends_on:
        gateway:
          condition: service_healthy
```

agent-sandbox doesn't inject health check logic. The gateway itself always has a built-in health check, so sidecars can depend on it.

### Gateway Middleware

Simple cases (static headers, auth tokens) are handled by `gateway.services[].headers` in user config — no middleware needed.

Middleware is for when you need **logic** (URL rewriting, token rotation, conditional auth, payload transforms). It's always a custom Go file:

```yaml
middlewares:
  - custom: "./middlewares/my-auth.go"
```

The Go file implements a known interface:

```go
package middleware

import "github.com/donbader/agent-sandbox/sdk/gateway"

func init() {
    gateway.RegisterMiddleware("my-auth", func(ctx *gateway.MiddlewareContext) error {
        // Custom logic: rewrite, transform, inject
        token := ctx.Env("MY_SECRET")
        ctx.Request.Header.Set("Authorization", "Bearer " + token)
        return nil
    })
}
```

The `MiddlewareContext` provides:

```go
type MiddlewareContext struct {
    Request  *http.Request
    Service  GatewayService
    Env      func(string) string // resolve env vars at runtime
}
```

At generate-time, the CLI copies custom middleware `.go` files into the gateway build context (`.build/gateway/middlewares/custom/`). The gateway's main package has a blank import of this package:

```go
// gateway/main.go
import _ "github.com/donbader/agent-sandbox/gateway/middlewares/custom"
```

Each custom middleware file uses `init()` to self-register. At docker-build time, `go build` compiles them all together — standard Go compilation, no special tooling. If no custom middleware exists, the package is an empty stub.

## Open Questions

1. **External plugin resolution** — how are remote plugins fetched? Git clone at generate-time? Pre-built images? Both?
