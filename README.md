# agent-sandbox

Deploy AI coding agents in Docker containers with transparent egress proxy, credential injection, and messaging channels.

**Philosophy:** One config file, one command. All infrastructure details hidden from the user.

## Features

- **Data-driven plugins** — runtime presets (codex, claude-code, pi) and feature plugins configured via YAML
- **Transparent gateway** — all agent traffic routes through a proxy for credential injection and MITM
- **Secret isolation** — real credentials never enter the agent container
- **Fleet-native** — every project uses `fleet.yaml` + per-agent subdirectories (even single-agent)
- **Security audit** — verify the sandbox contract with `agent-sandbox audit`
- **One command** — `agent-sandbox generate && agent-sandbox compose up --build`

## Quickstart

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/install.sh | sh

# Add to PATH (add to your shell profile)
export PATH="$HOME/.agent-sandbox/bin:$PATH"

# Scaffold a project (creates fleet.yaml + agent subdirectory)
mkdir my-project && cd my-project
agent-sandbox init

# Add secrets
echo "OPENAI_API_KEY=sk-..." > .env
echo "GITHUB_PAT=ghp_..." >> .env

# Generate and run
agent-sandbox generate
agent-sandbox compose up --build -d
```

## Commands

```bash
agent-sandbox init              # interactive project scaffold
agent-sandbox generate          # fleet.yaml → .build/<agent-name>/... (Dockerfile, entrypoint, gateway)
agent-sandbox compose ...       # docker compose passthrough (auto-injects -f and --env-file)
agent-sandbox audit             # verify running sandbox meets security contract
agent-sandbox gateway-url       # print gateway's public URL (use --agent for multi-agent)
agent-sandbox upgrade           # update the shim to latest version
agent-sandbox version           # print shim + core versions
```

Use `-C` to target a different project directory without switching to it:

```bash
agent-sandbox -C examples/multi-agent generate
agent-sandbox -C examples/multi-agent compose up --build
```

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│ Host                                                           │
│                                                                │
│  agent-sandbox (shim)                                          │
│  - Resolves core_version from agent.yaml                       │
│  - Downloads/caches core binary                                │
│  - Execs into agent-sandbox-core                               │
│                                                                │
│  agent-sandbox-core                                            │
│  - Reads fleet.yaml + per-agent agent.yaml                     │
│  - Resolves plugins (@builtin/ and local)                      │
│  - Generates .build/<agent-name>/... + docker-compose.yml      │
│  - Runs: docker compose up                                     │
│                                                                │
│  ┌──────────────────┐       ┌────────────────────────────────┐ │
│  │ Gateway          │◄──────│ Agent Container                │ │
│  │  TCP proxy       │ DNAT  │  iptables → gateway:8443       │ │
│  │  DNS (port 53)   │       │  CA cert trusted               │ │
│  │  TLS MITM        │       │  Runs as unprivileged user     │ │
│  │  Cred injection  │       │  Agent runtime (codex, etc.)   │ │
│  │  Log redaction   │       │  Optional: agent-manager + ACP │ │
│  │                  │       │  Optional: channel sidecars    │ │
│  │  Real credentials│       │                                │ │
│  │  stay HERE only  │       │  Dummy tokens only             │ │
│  └──────────────────┘       └────────────────────────────────┘ │
└────────────────────────────────────────────────────────────────┘
```

**Key security property:** The agent container cannot read real credentials. All secrets live in the gateway container. The agent uses dummy tokens; the gateway intercepts requests and swaps in real credentials.

## Documentation

| Doc                                        | Description                                  |
| ------------------------------------------ | -------------------------------------------- |
| [Getting Started](docs/getting-started.md) | Install, configure, and run your first agent |
| [Configuration](docs/configuration.md)     | agent.yaml, fleet.yaml, and .env reference   |
| [Plugins](docs/plugins.md)                 | Available plugins and their options          |
| [Security](docs/security.md)               | Isolation model, threat mitigations          |
| [Troubleshooting](docs/troubleshooting.md) | Common issues and fixes                      |

**Guides:**

| Guide                                               | Description                                                |
| --------------------------------------------------- | ---------------------------------------------------------- |
| [Multi-Agent](docs/guides/fleet-mode.md)            | Adding multiple agents with shared credentials             |
| [Creating Plugins](docs/guides/creating-plugins.md) | Build your own plugin (plugin.yaml, middleware, templates) |

**Reference:**

| Doc                                                        | Description                            |
| ---------------------------------------------------------- | -------------------------------------- |
| [CLI](docs/reference/cli.md)                               | Commands, flags, environment variables |
| [Migration](docs/reference/migration.md)                   | Upgrading from legacy CLI              |
| [Audit](docs/reference/audit.md)                           | Security contract verification checks  |
| [ACP Protocol](docs/reference/channel-manager-protocol.md) | Agent Client Protocol specification    |
| [Docker API Proxy](docs/reference/docker-api-proxy.md)     | Docker API validation design           |
| [ADRs](docs/reference/adr/)                                | Architecture Decision Records          |

**Internals (Contributors):**

| Doc                                                        | Description                                            |
| ---------------------------------------------------------- | ------------------------------------------------------ |
| [CLI/Core Split](docs/internals/cli-core-split.md)         | Shim + core architecture, version resolution, layout   |
| [Build Pipeline](docs/internals/build-pipeline.md)         | Generate flow, Dockerfile templates, core fetching     |
| [Gateway](docs/internals/gateway.md)                       | Proxy architecture, MITM pipeline, DNS, middleware SDK |
| [Plugin System](docs/internals/plugin-system.md)           | Resolution, rendering, compilation, fleet merging      |
| [Logging](docs/internals/logging.md)                       | Structured logging standards (Go + TypeScript)         |
| [Decisions](docs/internals/decisions.md)                   | Key decisions, comparison with agent-fleet             |
| [Roadmap](docs/internals/roadmap.md)                       | Phased implementation plan                             |

See [examples/](examples/) for working setups.

## License

MIT
