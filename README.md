# agent-sandbox

Deploy AI coding agents in Docker containers with transparent egress proxy, credential injection, and Telegram messaging.

## Features

- **Data-driven plugins** — runtime (codex, claude-code, pi) and feature plugins configured via YAML
- **Transparent gateway** — all agent traffic routes through a proxy for credential injection and MITM
- **Telegram channel** — chat with your agent via Telegram (access control, session management, commands)
- **Custom runtime** — install packages, mount volumes, run startup hooks
- **Multi-agent** — run multiple agents from a single `fleet.yaml`
- **One command** — `agent-sandbox generate && agent-sandbox compose up --build`

## Quickstart

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/install.sh | bash

# Scaffold a project
mkdir my-agent && cd my-agent
agent-sandbox init

# Or write config manually
cat > agent.yaml << 'EOF'
name: coder
runtime: codex

features:
  - plugin: github-pat
    token: "${GITHUB_PAT}"
  - plugin: telegram
    access_control:
      allowed_users: ["@yourname"]
  - plugin: custom-runtime
    commands:
      - "apt-get update && apt-get install -y --no-install-recommends ripgrep && rm -rf /var/lib/apt/lists/*"
    runtime_volumes:
      - "agent-home:{{ .AGENT_HOME }}"
EOF

# Generate and run
agent-sandbox generate
agent-sandbox compose up --build -d
agent-sandbox compose logs -f
```

## Commands

```bash
agent-sandbox init              # interactive project scaffold
agent-sandbox generate          # agent.yaml → .build/ (Dockerfile, docker-compose.yml)
agent-sandbox validate          # check config without generating
agent-sandbox plugins           # list available plugins
agent-sandbox compose ...       # docker compose passthrough
agent-sandbox upgrade           # self-update
```

## Architecture

```
┌─────────────────┐         ┌──────────────────────┐
│ Gateway         │◄────────│ Agent Container       │
│  - proxy/DNS    │         │  Channel Manager      │
│  - MITM         │         │  (spawns agent)       │
│  - cred inject  │         │  Agent Runtime        │
└─────────────────┘         └──────────────────────┘
```

All agent traffic flows through the gateway container via iptables DNAT. The gateway injects credentials (GitHub PAT, API keys) without exposing them to the agent environment.

## Documentation

- [Configuration](docs/configuration.md) — agent.yaml reference
- [Plugins](docs/plugins.md) — available runtime and feature plugins
- [Troubleshooting](docs/troubleshooting.md)
- [Security](docs/security.md)
- [Roadmap](docs/roadmap.md) — what's done, what's next

See [examples/](examples/) for working setups.

## License

MIT
