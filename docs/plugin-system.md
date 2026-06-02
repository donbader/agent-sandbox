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
  static-header/
    feature.yaml
    plugin.go
    plugin_test.go
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

CLI resolves plugins from built-in core plugins (embedded in CLI via go:embed):
- Runtime plugins: `internal/plugins/<name>/runtime.yaml`
- Feature plugins: registered via `init()` in `internal/plugins/<name>/plugin.go`

For custom runtimes not shipped with the CLI, users can define them inline in agent.yaml.

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

The channel manager is a generic TypeScript runtime that spawns the agent process and routes messages. Channel implementations are owned by plugins.

### Protocol

1. Plugin provides `channel/channel.ts` — exports default a class implementing `Channel`
2. Constructor signature: `constructor(config: Record<string, unknown>)` — receives the full channel manager config
3. Plugin's Go code declares `ChannelName: "telegram"` in FeatureContributions
4. Plugin's Go code populates `ChannelConfig` with channel-specific config

### Generator Assembly

During `agent-sandbox generate`, the generator:

1. Copies channel manager core (`channel-manager/`) to `.build/channel-manager-src/`
2. For each plugin with `ChannelName` set, copies `channel/channel.ts` → `.build/channel-manager-src/src/channel/<name>.ts`
3. Generates `.build/channel-manager-src/src/channel/channels.gen.ts` — import map of all channels

```
.build/channel-manager-src/
  src/
    index.ts              ← channel manager core (generic, never modified)
    acp-client.ts         ← ACP client (spawns agent adapter via @agentclientprotocol/sdk)
    channel/
      types.ts            ← Channel interface
      telegram.ts         ← copied from internal/plugins/telegram/channel/channel.ts
      channels.gen.ts     ← auto-generated registry
```

### Adding a New Channel

1. Create `internal/plugins/<name>/channel/channel.ts` implementing Channel
2. In plugin.go: `ChannelName: "<name>"` + `ChannelConfig: map[string]any{...}`
3. Run `agent-sandbox generate` — channel is automatically assembled

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
