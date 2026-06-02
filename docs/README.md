# agent-sandbox

An opinionated agent sandbox orchestrator that deploys AI coding agents inside secure Docker containers. Prioritizes minimal configuration while maintaining strong security boundaries.

**Philosophy:** One config file, one command. All infrastructure details hidden from the user.

## Requirements

1. User can choose supported runtime agent provider (codex, claude-code, pi)
2. Agent sandbox enforced (transparent proxy, iptables — cannot be bypassed)
3. Minimize user configuration efforts
4. Allow user to customize packages and home directory
5. Allow agent to spin up Docker containers for development

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  agent-sandbox CLI (user's machine)                          │
│  - Reads agent.yaml                                         │
│  - Calls plugin.Contribute() for each enabled plugin        │
│  - Merges contributions → generates build artifacts         │
│  - Runs: docker compose up                                  │
└─────────────────────────────────────────────────────────────┘

┌─ Per-Agent Container ───────────────────────────────────────┐
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  Gateway (universal Go binary, runs as `gateway` user)│    │
│  │  - iptables forces ALL TCP here (kernel enforced)    │    │
│  │  - MITM + credential injection for matched hosts     │    │
│  │  - Passthrough for everything else (allow-all)       │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  Bridge (TypeScript, always the entrypoint)          │    │
│  │  - Spawns agent runtime as child process             │    │
│  │  - Loads channel plugins (telegram, slack, etc.)     │    │
│  │  - No channels → agent runs standalone               │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  Agent Runtime (child of channel manager)                      │    │
│  │  - codex | claude-code | pi                          │    │
│  │  - Unaware of proxy, channel manager, or channels             │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Documents

| Doc | Description |
|-----|-------------|
| [Plugin System](plugin-system.md) | SDK interface, Contributions struct, module structure |
| [Plugins](plugins.md) | Runtime, credential, channel, and feature plugins |
| [Configuration](configuration.md) | User config, home directory, packages |
| [Security](security.md) | Network model, hardening, Docker access |
| [Build & Deploy](build-and-deploy.md) | Build flow, Dockerfile, distribution, multi-agent |
| [CLI & UX](cli-and-ux.md) | Commands, UX design, DX for plugin authors |
| [Decisions](decisions.md) | Key decisions, comparison with agent-fleet, maintainability |
| [Migration Plan](migration-plan.md) | Phased plan to migrate from agent-fleet to agent-sandbox |
