# Configuration

## Editor Autocompletion

Running `agent-sandbox generate` produces `.build/schema.json` — a JSON Schema for `agent.yaml`. Add this comment at the top of your config to get autocompletion and validation in VS Code (requires the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)):

```yaml
# yaml-language-server: $schema=.build/schema.json
name: coder
runtime: codex
```

The schema is generated from plugin struct tags, so it always reflects your active plugins — including available fields, types, defaults, and validation patterns (e.g., `@` prefix on usernames).

> Note: You need to run `agent-sandbox generate` at least once before the schema file exists.

## Working Directory

The `workdir` top-level field sets the agent's working directory inside the container.

```yaml
name: coder
runtime: codex
workdir: "{{ .AGENT_HOME }}/workspace"
```

| Field | Default | Description |
|-------|---------|-------------|
| `workdir` | `{{ .AGENT_HOME }}` | The working directory for the agent process |

### Template Variables

The builtin `{{ .AGENT_HOME }}` variable resolves to `/home/<user>` (defaults to `/home/agent`). It can be used in `workdir` and in `runtime_volumes` strings:

```yaml
workdir: "{{ .AGENT_HOME }}/workspace"
features:
  - plugin: custom-runtime
    runtime_volumes:
      - "agent-home:{{ .AGENT_HOME }}"
```

When `workdir` is omitted, it defaults to `{{ .AGENT_HOME }}`.

## Single Agent

```
my-agent/
  agent.yaml          ← only config file
  home/               ← override home directory (optional, auto-staged)
  scripts/            ← entrypoint hooks (optional)
  .env                ← secrets
```

```yaml
# agent.yaml
name: coder
runtime: codex

features:
  - plugin: github
    token: "${GITHUB_PAT}"
  - plugin: docker
  - plugin: telegram
    access_control:
      allowed_users: ["@donbader"]
  - plugin: custom-runtime
    commands:
      - "apt-get update && apt-get install -y --no-install-recommends ripgrep fd-find && rm -rf /var/lib/apt/lists/*"
    entrypoint_hooks:
      - ./scripts/sync-dotfiles.sh
    runtime_volumes:
      - "agent-home:{{ .AGENT_HOME }}"
```

## Multi-Agent (Optional)

```yaml
# fleet.yaml
agents:
  - coder
  - reviewer

shared:
  features:
    - plugin: github
      token: "${GITHUB_PAT}"
```

Per-agent features **override** shared (same name → per-agent wins). Different features merge additively.

## Home & Packages

Managed by the `custom-runtime` plugin. See [plugins.md](plugins.md#custom-runtime-plugin) for details.

| Strategy | Config | Behavior |
|----------|--------|----------|
| Ephemeral (default) | no plugin or no `runtime_volumes` | Home resets on restart. Auth token persists via small named volume. |
| Persistent | `runtime_volumes: ["agent-home:{{ .AGENT_HOME }}"]` | Named volume. Runtime state survives restarts. |
| Override | `./home/` dir exists | Files staged to /opt/home-override/, cp'd on every start. |
| Custom hooks | `entrypoint_hooks: [./scripts/...]` | Scripts run on every start (after override copy). |
| Custom packages | `commands: ["apt-get install ..."]` | RUN during docker build. |

Override mechanism uses `/opt/home-override/` staging (not in volume path). Entrypoint `cp -a` on every start ensures tracked configs always win over runtime state.

## Feature Config

Features are an array of plugin entries. Each entry requires a `plugin` field and optionally a `name` for logging:

```yaml
features:
  - plugin: github
    token: "${GITHUB_PAT}"               # secret reference
  - plugin: docker                        # no extra config needed
  - plugin: telegram
    access_control:
      allowed_users: ["@donbader"]
  - plugin: custom-runtime
    commands: ["apt-get update && apt-get install -y --no-install-recommends ripgrep && rm -rf /var/lib/apt/lists/*"]
    entrypoint_hooks: [./scripts/setup.sh]
    runtime_volumes: ["agent-home:{{ .AGENT_HOME }}"]
  - plugin: external-services
    services:
      - url: "https://agent-gateway.stx-ai.net"
        headers:
          Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}
  - plugin: mcp-oauth
    providers:
      notion:
        mcp_url: https://mcp.notion.com/mcp
```

`true` is shorthand for `{}` (enable with all defaults). CLI validates against each feature's `ConfigSchema()`.
