# agent-sandbox

Opinionated agent sandbox orchestrator. Deploy AI coding agents inside Docker containers with transparent egress proxy, credential injection, and messaging channels.

## Status

Under active development. Core features work (Phases 0–4 complete). See [Roadmap](docs/roadmap.md) for what's done and what's next.

**What works today:** generate build artifacts, run agents in containers, transparent proxy with credential injection, Telegram channel, custom runtime packages/volumes/hooks.

## Quick Start

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/install.sh | bash

# Create a project directory
mkdir my-agent && cd my-agent

# Write your config
cat > agent.yaml << 'EOF'
name: coder
runtime: codex
EOF

# Generate build artifacts and start
agent-sandbox generate
agent-sandbox compose up --build -d

# View logs
agent-sandbox compose logs -f
```

## Configuration

```yaml
# agent.yaml
name: coder
runtime: codex

features:
  - plugin: github-pat
    token: "${GITHUB_PAT}"
  - plugin: telegram
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    allowed_users: ["@donbader"]
  - plugin: custom-runtime
    commands:
      - "apt-get install -y ripgrep fd-find"
    runtime_volumes:
      - "agent-home:/home/agent"
```

See [examples/](examples/) for working setups.

## Architecture

```
┌────────────────────────────────┐
│  agent-sandbox CLI             │
│  Reads agent.yaml → generates  │
│  Dockerfile + docker-compose   │
└────────────────────────────────┘

         docker compose up
              │
    ┌─────────┴──────────┐
    ▼                    ▼
┌──────────┐     ┌─────────────────────┐
│ Gateway  │     │ Agent Container      │
│ container│◄────│                      │
│          │     │  Channel Manager     │
│ - proxy  │     │  (spawns agent)      │
│ - DNS    │     │                      │
│ - MITM   │     │  Agent Runtime       │
│ - creds  │     │  (codex/claude/pi)   │
└──────────┘     └─────────────────────┘
```

- **Runtime plugins** — set base image + agent CLI (one per agent): codex, claude-code, pi
- **Feature plugins** — additive capabilities (multiple per agent): custom-runtime, telegram, github-pat, static-header
- **Gateway** — separate container, transparent proxy (default route enforced), MITM for credential injection
- **Channel Manager** — TypeScript process that spawns agent as child, loads channel plugins (Telegram, etc.)

## Commands

```bash
agent-sandbox init              # interactive project scaffold
agent-sandbox generate          # read config → write .build/ artifacts
agent-sandbox validate          # check config without generating
agent-sandbox plugins           # list available plugins
agent-sandbox compose ...       # docker compose passthrough (up, down, logs, etc.)
agent-sandbox upgrade           # self-update to latest release
```

## Current Limitations

These features are planned but not yet available:

- Docker API proxy (let agent spin up containers)
- MCP OAuth credential flow
- Streaming reply (edit Telegram message as agent streams)
- Security hardening (cap_drop, no-new-privileges)

See [Roadmap](docs/roadmap.md) for the full plan.

## Docs

- [Configuration](docs/configuration.md)
- [Plugins](docs/plugins.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Security](docs/security.md)
- [Plugin System](docs/plugin-system.md) (for developers)
- [Build & Deploy](docs/build-and-deploy.md) (for developers)
- [Decisions](docs/decisions.md)
- [Roadmap](docs/roadmap.md)

## License

MIT
