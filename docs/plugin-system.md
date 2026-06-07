# Plugin System

## Design

Two plugin types with clear separation:

- **RuntimePlugin** — data-driven (YAML + optional Dockerfile template). Sets base image + installs agent CLI. One per agent.
- **FeaturePlugin** — hybrid (YAML metadata + Go code for gateway + TypeScript for channel manager). Additive capabilities. Multiple per agent.

**Key principle:** Core plugins ship with the CLI. Gateway and channel code recompiles during Docker build, so handler fixes only require a container rebuild.

## Plugin Directory Structure

### Runtime Plugins (Pure Data)

```
internal/plugins/
  codex/
    runtime.yaml            ← base image, install commands, default CMD
```

Runtime plugins are pure data — no Go code, no compilation. The CLI reads `runtime.yaml` and generates a Dockerfile using the built-in generator.

### Feature Plugins (Data + Code)

```
internal/plugins/
  telegram/
    feature.yaml            ← metadata, config schema
    plugin.go               ← typed Config struct + Register[C]() call
    plugin_test.go
    channel/                ← TypeScript: channel implementation (Channel Protocol)
      channel.ts            ← export default class implementing Channel
  github-pat/
    feature.yaml            ← metadata, config schema, hosts
    plugin.go
    plugin_test.go
  external-services/
    feature.yaml
    plugin.go
    plugin_test.go
    README.md
  custom-runtime/
    feature.yaml            ← metadata, config schema
    plugin.go               ← no gateway, no channel — pure config-driven
```

Feature plugins have:
- `feature.yaml` — always present (metadata, config schema, hosts)
- `plugin.go` — typed Config struct with `yaml`/`schema` tags, registered via `init()`
- `channel/` — optional TypeScript, copied into image

## Runtime Plugin Schema (runtime.yaml)

```yaml
name: codex
base_image: node:22-slim
install:
  - apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates
  - npm install -g @openai/codex
cmd: ["sleep", "infinity"]   # default CMD (channel manager replaces this when active)
user: agent
```

Fields:
| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Plugin identifier (matches `runtime:` in agent.yaml) |
| `base_image` | yes | Docker base image |
| `install` | yes | Shell commands to install agent CLI + dependencies |
| `cmd` | yes | Default CMD (overridden by channel manager when channels are active) |
| `user` | no | Runtime user (default: `agent`) |

## Feature Plugin Schema (feature.yaml)

```yaml
name: github
description: "GitHub PAT injection via gateway MITM"

config_schema:
  token:
    type: string
    required: true
    env: true           # value is ${ENV_VAR} reference

gateway:
  hosts:
    - "github.com"
    - "*.github.com"
    - "api.github.com"
  mode: mitm            # mitm | passthrough

channel: false          # no channel plugin

compose: {}             # no extra services
```

```yaml
name: telegram
description: "Telegram bot channel"

config_schema:
  bot_token:
    type: string
    required: true
    env: true
  allowed_users:
    type: array
    items: string
    required: true

gateway:
  hosts:
    - "api.telegram.org"
  mode: mitm

channel: true           # has channel/ directory with TypeScript

compose: {}
```

```yaml
name: custom-runtime
description: "Custom packages, startup hooks, persistent volumes"

config_schema:
  commands:
    type: array
    items: string
  entrypoint_hooks:
    type: array
    items: string
  runtime_volumes:
    type: array
    items: string

gateway: false          # no gateway involvement
channel: false          # no channel plugin

compose:
  volumes_from_config: runtime_volumes   # maps config field → compose volumes
```

## How CLI Uses Plugins

```
agent-sandbox generate
  │
  ├── Read agent.yaml
  ├── Find runtime plugin: internal/plugins/<runtime>/runtime.yaml
  ├── Find feature plugins: registered via init() in internal/plugins/<feature>/plugin.go
  │
  ├── Generate Dockerfile:
  │     ├── FROM <runtime.base_image>
  │     ├── RUN <runtime.install> commands
  │     ├── RUN <home-vc.commands> (if configured)
  │     ├── COPY gateway source (if any feature has gateway/)
   │     ├── COPY channel manager source (if any feature has channel/)

   │     └── CMD <runtime.cmd> (or channel manager entrypoint if channels active)

   ├── Generate channel-manager-config.json (channel plugins + agent cmd)
  ├── Generate docker-compose.yml
  └── Generate .env.example
```

## Plugin Resolution

CLI resolves plugins from core (fetched from GitHub Releases, cached locally):
- Runtime presets: `core/presets/<name>/preset.yaml`
- Feature plugins: `core/plugins/<name>/plugin.yaml`

For custom runtimes not shipped with core, users can define them inline in agent.yaml.

## Gateway Compilation

Gateway handlers are Go code, but they're compiled **during Docker build** (not CLI build):

```dockerfile
# Stage 1: Compile gateway with active handlers
FROM golang:1.24 AS gateway-builder
COPY .build/gateway-src/ /src/
RUN cd /src && CGO_ENABLED=0 go build -o /gateway ./cmd/gateway
```

The CLI extracts gateway source + active feature handlers into `.build/gateway-src/`. Docker multi-stage compiles them. User doesn't need Go installed.

## Channel Manager & Channel Protocol

Channel adapters are **sidecars** — separate Docker containers defined via `contributes.sidecar.services` in the plugin's `plugin.yaml`. They connect to the agent-manager over WebSocket at `ws://agent:<port>/acp`.

### Architecture

```
┌─────────────────────┐       WebSocket        ┌────────────────────┐
│  telegram-adapter   │ ───────────────────────▶│   agent-manager    │
│  (sidecar container)│   ws://agent:3100/acp   │  (main container)  │
└─────────────────────┘                         │  spawns agent CLI  │
                                                └────────────────────┘
```

The `agent-manager-acp` plugin provides the ACP proxy that spawns the agent process and exposes it over HTTP/WebSocket. Channel plugins declare it as a dependency:

```yaml
requires: ["@builtin/agent-manager-acp"]
```

### Adding a New Channel

1. Create a plugin with a sidecar service that speaks ACP over WebSocket to agent-manager
2. Declare `requires: ["@builtin/agent-manager-acp"]` in your `plugin.yaml`
3. Add gateway middleware if the channel needs credential injection (e.g., bot token rewrite)
4. Define the sidecar container via `contributes.sidecar.services`

Example plugin.yaml for a channel adapter:

```yaml
name: telegram
description: "Telegram channel adapter (sidecar)"

requires: ["@builtin/agent-manager-acp"]

config_schema:
  bot_token:
    type: string
    required: true
    env: true
  allowed_users:
    type: array
    items: string
  agent_manager_port:
    type: string
    default: "3100"

gateway:
  hosts:
    - "api.telegram.org"
  mode: mitm

contributes:
  sidecar:
    services:
      telegram-adapter:
        build: .build/telegram-adapter
        environment:
          AGENT_MANAGER_URL: "ws://agent:{{ .agent_manager_port }}/acp"
          BOT_TOKEN: "{{ .bot_token }}"
```

## Template Functions

Templates used in Dockerfiles and compose generation support Go `text/template` syntax with the following custom functions:

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON` | Serializes a value to JSON. Useful for baking structured config (arrays, maps) into Dockerfiles or entrypoint scripts. | `{{ .mcp_servers \| toJSON }}` |

### toJSON

Converts any Go value into its JSON string representation. Particularly useful when you need to embed structured data (arrays, objects) into generated files where YAML or shell expansion wouldn't work.

```dockerfile
# In a Dockerfile template:
ENV MCP_SERVERS='{{ .mcp_servers | toJSON }}'

# Produces:
ENV MCP_SERVERS='[{"url":"https://mcp.notion.com","name":"notion"}]'
```

```yaml
# In an entrypoint hook template:
echo '{{ .allowed_users | toJSON }}' > /etc/agent/allowed-users.json
```

## Plugin Metadata Fields

### requires

Declares plugin dependencies. The generator ensures required plugins are present and ordered before the dependent plugin.

```yaml
requires: ["@builtin/agent-manager-acp"]
```

- Values use `@builtin/<name>` for core plugins
- If a required plugin is missing from the agent's `features:` list, generation fails with a clear error
- Dependency order is respected during compose and Dockerfile generation

### assets

Declares directories containing source files to extract/copy during Docker build. Used by plugins that bundle their own application code (e.g., the agent-manager TypeScript source).

```yaml
assets:
  - dir: agent-manager
    target: /opt/agent-manager
```

| Field | Description |
|-------|-------------|
| `dir` | Directory path relative to the plugin root |
| `target` | Destination path inside the container |

During `agent-sandbox generate`, asset directories are copied to `.build/<plugin-name>/<dir>/` and a corresponding `COPY` instruction is added to the Dockerfile. The asset code is compiled or executed during Docker build (not at CLI time).

## Command Plugins

Feature plugins can register commands into the channel-manager. Commands are available to users from any channel (Telegram, etc.).

### CommandPlugin Interface

```typescript
interface CommandPlugin {
  name: string;
  commands: Record<string, CommandHandler>;
  init?(config: Record<string, unknown>): void;
  onMessage?(text: string, chatId: string, reply: CommandReply): Promise<boolean>;
  destroy?(): void;
}
```

### Adding a Command Plugin

1. Create `internal/plugins/<name>/command/` with TypeScript source
2. Export a default `CommandPlugin` instance from the main file
3. In plugin.go: set `CommandPluginDir: "command"`
4. Pass config via `ChannelConfig: map[string]any{...}`
5. Run `agent-sandbox generate` — command plugin is automatically wired into channel-manager

The generate step copies command plugin TypeScript files into `channel-manager-src/src/command/` and generates a `commands.gen.ts` registry.

### Example: mcp-oauth

```
internal/plugins/mcp-oauth/
  command/
    oauth-command.ts    ← implements CommandPlugin, exports default
    discovery.ts        ← RFC 9728 well-known discovery
    pkce.ts             ← PKCE helpers
    types.ts            ← shared types
  plugin.go             ← Go: sets CommandPluginDir + ChannelConfig
```

## Custom Runtime (Inline)

For runtimes not shipped with the CLI, users can define inline in agent.yaml:

```yaml
name: my-agent
runtime:
  base_image: python:3.12-slim
  install:
    - pip install my-agent-cli
  cmd: ["my-agent-cli", "--headless"]
```

## Why Data-Driven

| Concern | Old (compile-time) | New (data-driven) |
|---------|-------------------|-------------------|
| Runtime plugin fix | CLI upgrade required | Edit yaml, re-generate |
| New runtime | CLI release | Add runtime.yaml locally |
| Gateway handler fix | CLI upgrade + rebuild | Edit Go source, rebuild container |
| Channel plugin fix | CLI upgrade + rebuild | Edit TypeScript, rebuild container |
| CLI role | Contains all plugin logic | Generic template engine |
| Plugin updates | Coupled to CLI releases | Independent of CLI releases |

## Conflict Detection

- Same host claimed by two features → error at generate time
- Two features writing same compose volume → error
- Two features with same entrypoint hook name → error (use priority to order)
