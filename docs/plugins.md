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
| `static-header` | user-defined endpoint | Static header injection | yes | available |
| `mcp-oauth` | user-defined MCP server URL | OAuth2 token refresh | yes | **planned** |

Note: LLM API credentials (OpenAI, Anthropic) are handled by the runtime itself (codex device flow, claude login). No dedicated plugins needed.

### Channel Features

Contribute both gateway rules AND channel TypeScript. One plugin, two directories.

| Plugin | Gateway | Channel | Status |
|--------|---------|---------|--------|
| `telegram` | MITM api.telegram.org, inject bot token | grammy bot, long-poll | available |
| `slack` | MITM slack.com, OAuth token refresh | Slack socket mode | **planned** |

### Infrastructure Features

| Plugin | What it does | Has gateway/ | Has channel/ | Status |
|--------|-------------|-------------|-------------|--------|
| `custom-runtime` | Custom commands, hooks, volumes | no | no | available |
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
      - "agent-home:/home/agent"
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

```yaml
features:
  - plugin: telegram
    access_control:
      allowed_users: ["@donbader"]
```

Gateway: MITM on api.telegram.org, injects bot token via URL rewrite.
Channel Manager: grammy-based long-poll bot, filters by allowed_users.

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
