# agent-sandbox

Opinionated agent sandbox orchestrator. Deploy AI coding agents inside Docker containers with transparent egress proxy, credential injection, and messaging channels.

## Status

🚧 Under active development. See [Migration Plan](docs/migration-plan.md) for roadmap.

## Quick Start

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/install.sh | bash

# Initialize
agent-sandbox init

# Generate build artifacts
agent-sandbox generate

# Start
agent-sandbox compose up --build -d

# Logs
agent-sandbox compose logs -f
```

## Configuration

```yaml
# agent.yaml
name: coder
runtime: codex

features:
  github:
    token: "${GITHUB_PAT}"
  telegram:
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    allowed_users: ["donbader"]
  custom-runtime:
    commands:
      - "apt-get install -y ripgrep fd-find"
    runtime_volumes:
      - "agent-home:/home/agent"
```

## Architecture

- **RuntimePlugin** — sets base image + agent CLI (one per agent)
- **FeaturePlugin** — additive capabilities (multiple per agent)
- **Gateway** — transparent proxy inside each container (iptables enforced, MITM for credential injection)
- **Bridge** — TypeScript runtime that spawns agent as child process, loads channel plugins

## Commands

```bash
agent-sandbox init              # interactive scaffold
agent-sandbox generate          # read config → write .build/ artifacts
agent-sandbox validate          # check config
agent-sandbox plugins           # list available plugins
agent-sandbox upgrade           # self-update
agent-sandbox compose ...       # docker compose passthrough
```

## Docs

- [Plugin System](docs/plugin-system.md)
- [Plugins](docs/plugins.md)
- [Configuration](docs/configuration.md)
- [Security](docs/security.md)
- [Build & Deploy](docs/build-and-deploy.md)
- [CLI & UX](docs/cli-and-ux.md)
- [Decisions](docs/decisions.md)
- [Migration Plan](docs/migration-plan.md)

## License

MIT
