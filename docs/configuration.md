# Configuration

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
  github:
    token: "${GITHUB_PAT}"
  docker: true
  telegram:
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    allowed_users: ["donbader"]
  custom-runtime:
    commands:
      - "apt-get install -y ripgrep fd-find"
    entrypoint_hooks:
      - ./scripts/sync-dotfiles.sh
    runtime_volumes:
      - "agent-home:/home/agent"
```

## Multi-Agent (Optional)

```yaml
# fleet.yaml
agents:
  - coder
  - reviewer

shared:
  features:
    github:
      token: "${GITHUB_PAT}"
```

Per-agent features **override** shared (same name → per-agent wins). Different features merge additively.

## Home & Packages

Managed by the `custom-runtime` plugin. See [plugins.md](plugins.md#custom-runtime-plugin) for details.

| Strategy | Config | Behavior |
|----------|--------|----------|
| Ephemeral (default) | no plugin or no `runtime_volumes` | Home resets on restart. Auth token persists via small named volume. |
| Persistent | `runtime_volumes: ["agent-home:/home/agent"]` | Named volume. Runtime state survives restarts. |
| Override | `./home/` dir exists | Files staged to /opt/home-override/, cp'd on every start. |
| Custom hooks | `entrypoint_hooks: [./scripts/...]` | Scripts run on every start (after override copy). |
| Custom packages | `commands: ["apt-get install ..."]` | RUN during docker build. |

Override mechanism uses `/opt/home-override/` staging (not in volume path). Entrypoint `cp -a` on every start ensures tracked configs always win over runtime state.

## Feature Config

Features accept config via the `features:` map:

```yaml
features:
  github:
    token: "${GITHUB_PAT}"       # secret reference
  docker: true                    # shorthand for {} (all defaults)
  telegram:
    bot_token: "${BOT_TOKEN}"
    allowed_users: ["donbader"]
  custom-runtime:
    commands: ["apt-get install -y ripgrep"]
    entrypoint_hooks: [./scripts/setup.sh]
    runtime_volumes: ["agent-home:/home/agent"]
```

`true` is shorthand for `{}` (enable with all defaults). CLI validates against each feature's `ConfigSchema()`.
