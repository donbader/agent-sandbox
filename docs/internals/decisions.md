# Architecture Decisions

Key design decisions and their rationale. For detailed ADRs, see [docs/reference/adr/](../reference/adr/).

## Decision Table

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | POSIX shell shim | Single `curl \| sh` install. Shim is ~220 lines of `/bin/sh`. Handles version resolution, caching, upgrades. No runtime dependency beyond curl. |
| 2 | Separate shim + core binary | Shim never changes (stable install path). Core binary can ship new features without users re-installing. Version pinning in agent.yaml. |
| 3 | Gateway as pre-built binary | Gateway ships from GitHub Releases as a compiled Go binary for linux/amd64 + linux/arm64. No per-project compilation. Plugins are TypeScript loaded at runtime — no recompilation needed for plugin changes. |
| 4 | TypeScript plugins via goja | Plugins need access to crypto, HTTP, filesystem. Go templates are too limited. TypeScript via goja gives a familiar language with sandboxed execution. No node_modules or npm needed — single-file scripts with host APIs. |
| 5 | Transparent proxy via iptables | Agent doesn't know it's proxied. Works with any HTTP client, any language, any tool. No env vars to set, no proxy config. Requires NET_ADMIN capability and DNAT rules. |
| 6 | All credentials through gateway | Real secrets never enter the agent container. Gateway injects auth headers on matching domains. If the agent is compromised, it can't exfiltrate credentials — they only exist in gateway memory. |
| 7 | Ephemeral by default | Containers start fresh every restart. No state accumulates. Volume persistence is opt-in per plugin. This prevents state-related bugs and security drift. |
| 8 | Runtime presets are pure YAML | No Go code for presets. CLI reads `runtime.yaml` and generates Dockerfile. Adding a new runtime (e.g. a new agent) requires only YAML — no Go changes, no CLI release. |
| 9 | Plugin updates don't require CLI upgrades | Plugins ship in the core tarball, separate from the shim. New plugins or plugin changes only need a core version bump. The CLI is a generic template engine. |
| 10 | MITM for HTTPS interception | Gateway acts as a TLS-terminating proxy for configured domains. Per-domain certs signed by an ephemeral CA trusted by the agent. Enables header injection and request inspection for HTTPS traffic. |
| 11 | Shared config in fleet.yaml | `shared` block in fleet.yaml is merged into all agents. Reduces duplication. Per-agent overrides win conflicts. Each agent still gets its own gateway (independent security boundaries). |
| 12 | Core version pinning | `core_version: 1.31.1` in per-agent `agent.yaml` ensures reproducible builds. `latest` is available but discouraged for production. Shim falls back to local cache if download fails. |

## Evolution from agent-fleet

This project evolved from [agent-fleet](https://github.com/donbader/agent-fleet). Key architectural shifts:

| Before (agent-fleet) | After (agent-sandbox) |
|----|-----|
| Monolithic Go binary | Shim + versioned core binary |
| Go middleware compiled per-project | TypeScript loaded at runtime |
| Gateway source in Docker build | Pre-built gateway binary |
| Hardcoded agent types | Data-driven runtime presets |
| Single agent per project | Unified fleet model (single or multi-agent) |

## Principles

- **Every phase produces a working `generate && compose up --build`** — no half-implemented features.
- **Plugin updates never require CLI upgrades** — the CLI is a generic engine.
- **Runtime presets are pure data** — YAML only, no Go code.
- **Feature plugins are TypeScript** — loaded at gateway runtime, no recompilation.
- **Transparent proxy** — agent doesn't know it's proxied.
- **Ephemeral by default** — containers start fresh every restart.
- **All credentials through gateway** — real creds never in container env.
