# Plugins

## Runtime Plugins

Runtime plugins are pure data (YAML). They define the base image, install commands, and default CMD. Selected by the `runtime:` field in agent.yaml. Only one per agent.

```yaml
runtime: codex    # reads plugins/codex/runtime.yaml
```

### Built-in Runtimes

| Runtime | Base Image | Packages | CMD |
|---------|-----------|----------|-----|
| `codex` | node:22-slim | git, curl, @openai/codex | sleep infinity |
| `claude-code` | node:22-slim | git, curl, @anthropic-ai/claude-code | sleep infinity |
| `pi` | node:22-slim | git, curl, pi-coding-agent | sleep infinity |

Default CMD is `sleep infinity` because without a channel manager, there's no way to send prompts. When a channel feature is active, channel manager becomes the entrypoint and spawns the agent CLI (e.g., `codex exec`).

### Custom Runtime (Inline)

For runtimes not shipped with the CLI:

```yaml
name: my-agent
runtime:
  base_image: python:3.12-slim
  install:
    - pip install my-agent-cli
  cmd: ["my-agent-cli", "--headless"]
```

Or create `plugins/my-runtime/runtime.yaml` in your project directory.

### runtime.yaml Format

```yaml
name: codex
base_image: node:22-slim
install:
  - apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates
  - npm install -g @openai/codex
cmd: ["sleep", "infinity"]
user: agent
```

## Feature Plugins

Additive capabilities. Multiple per agent. Listed under `features:` in config.

Feature plugins are hybrid — YAML metadata + optional Go code (gateway) + optional TypeScript (channel).

### Credential Features

| Plugin | Hosts | Injection | Has gateway/ | Status |
|--------|-------|-----------|-------------|--------|
| `github-pat` | github.com, *.github.com | Header: `Authorization: token <PAT>` | yes | available |
| `external-services` | user-defined (host:port or https://) | Static header injection | yes | available |
| `mcp-oauth` | user-defined MCP server URL | OAuth2 token refresh | yes | **planned** |

Note: LLM API credentials (OpenAI, Anthropic) are handled by the runtime itself (codex device flow, claude login). No dedicated plugins needed.

### Agent Manager

| Plugin | What it does | Status |
|--------|-------------|--------|
| `agent-manager-acp` | ACP proxy — spawns agent, exposes HTTP/WebSocket for channel adapters | available (core built-in) |

#### agent-manager-acp

Spawns an ACP-compatible agent process and exposes it over HTTP/WebSocket. Channel adapter sidecars connect to this service to send/receive messages.

- Performs ACP handshake at startup (initialize + authenticate)
- Intercepts client init/auth, injects `mcpServers` into session/new
- Assets: contains the agent-manager TypeScript source (compiled during Docker build)

```yaml
features:
  - plugin: agent-manager-acp
    acp_command: ["codex", "exec", "--headless"]  # required — command to spawn
    port: "3100"                                   # optional, default "3100"
```

| Option | Required | Description |
|--------|----------|-------------|
| `acp_command` | yes | Array — the command to spawn as the agent process |
| `port` | no | HTTP/WebSocket listen port (default: `"3100"`) |

### Channel Features

Channel adapters are **sidecars** — separate Docker containers that connect to agent-manager via WebSocket. Each channel plugin contributes gateway middleware (for credential injection) plus a sidecar service definition.

| Plugin | Gateway | Sidecar | Requires | Status |
|--------|---------|---------|----------|--------|
| `telegram` | MITM api.telegram.org, inject bot token | telegram-adapter (WebSocket → agent-manager) | `@builtin/agent-manager-acp` | available |
| `slack` | MITM slack.com, OAuth token refresh | slack-adapter | `@builtin/agent-manager-acp` | **planned** |

### Infrastructure Features

| Plugin | What it does | Has gateway/ | Has channel/ | Status |
|--------|-------------|-------------|-------------|--------|
| `custom-runtime` | Custom commands, hooks, volumes | no | no | available |
| `external-services` | Connect to Docker/HTTPS services, inject headers | yes | no | available |
| `docker` | DinD sidecar, DOCKER_HOST env, API validation | yes | no | **planned** |

### custom-runtime

Gives users direct control over image build commands, startup hooks, and persistent volumes.

```yaml
features:
  - plugin: custom-runtime
    commands:
      - "apt-get update && apt-get install -y --no-install-recommends ripgrep fd-find && rm -rf /var/lib/apt/lists/*"
      - "npm install -g typescript"
    entrypoint_hooks:
      - ./scripts/sync-dotfiles.sh
      - ./scripts/setup-git.sh
    runtime_volumes:
      - "agent-home:{{ .AGENT_HOME }}"
```

| Field | Effect |
|-------|--------|
| `commands` | RUN during docker build (after base packages) |
| `entrypoint_hooks` | Scripts run on every container start (before agent) |
| `runtime_volumes` | Named volumes mounted at runtime |

The `./home/` override directory (if present) is auto-staged to `/opt/home-override/` and cp'd by a built-in entrypoint hook.

### mcp-oauth

OAuth Bearer token injection for remote MCP servers. Interactive setup via `/oauth` command, automatic token refresh at runtime.

```yaml
features:
  - plugin: mcp-oauth
    providers:
      notion:
        mcp_url: https://mcp.notion.com/mcp  # uses Dynamic Client Registration
      slack:
        mcp_url: https://mcp.slack.com/mcp
        client_id: "pre-registered-id"        # optional — skip if server supports DCR
        client_id: "slack-client-id"
        client_secret: "${SLACK_CLIENT_SECRET}"
    token_dir: /data/oauth-tokens  # optional
```

Handles: RFC 9728 discovery, PKCE authorization, token exchange, auto-refresh. User triggers auth via channel command (`/oauth notion`). See [plugin README](../internal/plugins/mcp-oauth/README.md) for full details.

### telegram

A sidecar-based channel adapter that connects to agent-manager via WebSocket. Contributes gateway middleware (telegram token rewrite) and a sidecar service (telegram-adapter).

Requires `@builtin/agent-manager-acp`.

```yaml
features:
  - plugin: agent-manager-acp
    acp_command: ["codex", "exec", "--headless"]
  - plugin: telegram
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    allowed_users: ["@donbader"]         # optional
    agent_manager_port: "3100"           # optional, default "3100"
```

| Option | Required | Description |
|--------|----------|-------------|
| `bot_token` | yes | Telegram bot token (env var reference) |
| `allowed_users` | no | Array of allowed Telegram usernames |
| `agent_manager_port` | no | Port to connect to agent-manager (default: `"3100"`) |

Gateway: MITM on api.telegram.org, injects bot token via URL rewrite.
Sidecar: telegram-adapter container connects to `ws://agent:<port>/acp`, bridges Telegram messages to the agent via ACP.

### github-pat

```yaml
features:
  - plugin: github-pat
    token: "${GITHUB_PAT}"
```

Gateway: MITM on github.com/api.github.com, injects `Authorization: token <PAT>` header.

### docker (planned)

```yaml
features:
  - plugin: docker
```

Adds DinD sidecar service, installs docker CLI, sets DOCKER_HOST, validates Docker API requests via gateway.
