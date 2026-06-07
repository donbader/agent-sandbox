# Roadmap

## Strategy

New repo (`donbader/agent-sandbox`). agent-fleet stays in maintenance mode (security fixes only). No in-place migration — clean break.

**Principle:** Every phase produces a working `agent-sandbox generate && agent-sandbox compose up --build`. Each phase adds capabilities, never breaks what's already working.

**Key design:** Plugins are data-driven. Runtime plugins are pure YAML. Feature plugins are YAML + optional code (Go gateway handlers compiled during Docker build, TypeScript channel plugins). CLI is a generic template engine — plugin updates never require CLI upgrades.

## Phases

### Phase 0: Repo Setup ✅

**What works after this phase:** Repo exists, agent can work on it.

- [x] Create `donbader/agent-sandbox` repo
- [x] Go workspace (`go.work`)
- [x] AGENTS.md (instructions for coding agents)
- [x] README.md (project overview, phase roadmap)
- [x] Makefile (build, test, lint targets)
- [x] .gitignore
- [x] .golangci.yml
- [x] SDK module stub (`sdk/go.mod`, `sdk/plugin.go` with interfaces)
- [x] docs/ (design docs)

---

### Phase 1: Bare Container ✅

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent running in a container (direct entrypoint, no proxy, no channel manager)
```

- [x] `internal/plugins/codex/runtime.yaml` (base image, install commands, CMD)
- [x] `generate` command (reads agent.yaml + runtime.yaml → writes .build/)
- [x] `compose` passthrough command
- [x] Dockerfile generation (single stage, no gateway)
- [x] docker-compose.yml generation
- [x] .env.example generation (scan ${VAR} patterns)
- [x] Integration test (`//go:build integration` docker build test)
- [x] Testing guidelines in AGENTS.md
- [x] Reference docs (ADRs, channel-manager protocol, docker-api-proxy)
- [x] Phase implementation guide in AGENTS.md
- [x] GoReleaser release pipeline (`.github/workflows/release.yml`)
- [x] `examples/local-coding/` for local machine coding
- [x] `examples/telegram-vibe/` for Telegram-based coding
- [x] `install.sh` one-liner
- [x] Convert codex plugin from Go code to `runtime.yaml` (data-driven)
- [x] Plugin resolution (core plugins via GitHub Releases)
- [x] Inline runtime definition support in agent.yaml

**Config:**
```yaml
name: coder
runtime: codex
```

---

### Phase 2: custom-runtime Feature

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent with custom packages, startup hooks, persistent home
```

- [x] `internal/plugins/custom-runtime/feature.yaml`
- [x] Update `internal/generate/` to read feature.yaml and merge into Dockerfile
- [x] Image commands wiring (RUN in Dockerfile from config)
- [x] Entrypoint hooks wiring (scripts run on container start)
- [x] Compose volumes wiring (named volumes from config)
- [x] Entrypoint script template (runs hooks → starts agent)
- [x] Home override directory (./home/ → /opt/home-override/ → cp on start)
- [x] `examples/local-coding/` example

**Config:**
```yaml
name: coder
runtime: codex
features:
  - plugin: custom-runtime
    commands:
      - "apt-get update && apt-get install -y --no-install-recommends ripgrep fd-find && rm -rf /var/lib/apt/lists/*"
    entrypoint_hooks:
      - ./scripts/sync-dotfiles.sh
    runtime_volumes:
      - "agent-home:/home/agent"
```

- [x] Plugin hybrid architecture: feature.yaml points to implementation code (plugin owns its logic)

---

### Phase 3: Gateway (Network Enforcement) ✅

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent with transparent proxy (all traffic via default route → gateway)
```

- [x] Gateway Go module (`gateway/`) — core proxy logic
- [x] TCP listener + SNI extraction
- [x] Passthrough mode (pipe bytes to destination)
- [x] DNS resolver (gateway:53, agent resolv.conf points to gateway)
- [x] Gateway source packaged via core-release.yml, fetched by CLI
- [x] Separate gateway container (security isolation — agent can't read secrets)
- [x] Default route proxy (IP forwarding + iptables DNAT in agent → gateway container)
- [x] `RequestHandler` interface in gateway (for feature handlers)
- [x] Structured logging (slog for Go, pino for TypeScript)
- [x] Handler registry (config-driven — rewriter types instantiated from gateway-config.yaml)
- [x] Integration test (verify traffic routes through gateway)

---

### Phase 4: Channel Manager + Telegram ✅

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → codex agent reachable via Telegram (send message → agent responds)
```

- [x] Channel manager TypeScript (`channel-manager/`)
- [x] ACP client (ClientSideConnection, auto-approve permissions)
- [x] Channel plugin loader (generated `channels.gen.ts` registry)
- [x] `internal/plugins/telegram/feature.yaml`
- [x] `gateway/internal/mitm/telegram.go` — MITM on api.telegram.org
- [x] `internal/plugins/telegram/channel/` — grammy channel plugin (ack emoji, typing, formatter, rate limiter)
- [x] MITM logic in gateway core (TLS termination, HTTP interception)
- [x] Sandbox CA generation
- [x] Channel manager config generation (channel-manager-config.json)
- [x] `examples/telegram-vibe/` example
- [x] Per-chat session isolation (SessionManager + SessionStore)
- [x] Session persistence + loadSession resume
- [x] Startup buffer (buffer messages during agent startup)
- [x] Extension system (BridgeExtension, ExtensionRegistry)
- [x] Core commands (/new, /stop, /resume, /label, /version, /sh, /diagnose)
- [x] claude-code + pi runtime plugins
- [x] github-pat + static-header gateway plugins

---

### Phase 5: CLI Polish + Multi-Agent

**What works after this phase:**
```bash
agent-sandbox init                      # interactive scaffold
agent-sandbox validate                  # config check
agent-sandbox generate && agent-sandbox compose up --build
agent-sandbox upgrade                   # self-update
```

- [x] `init` command (interactive, detect gh auth, suggest features)
- [x] `validate` command (config check + helpful errors)
- [x] `plugins` command (list/info)
- [x] `upgrade` command (self-update)
- [x] fleet.yaml support (multi-agent, shared features)
- [x] `examples/multi-agent/` example

---

### Phase 6: Integrations & Hardening

**What works after this phase:**
```bash
agent-sandbox generate && agent-sandbox compose up --build
# → Full-featured agent: Docker API proxy, mcp-oauth, streaming
```

- [x] `plugins/mcp-oauth/feature.yaml` + `gateway/handler.go`
- [x] Streaming reply (edit Telegram message as agent streams)
- [x] Agent-provided commands (declared via ACP initialize response)
- [x] Telegram `setMyCommands` registration (bot menu)
- [ ] `plugins/docker/feature.yaml` + `gateway/handler.go` + compose sidecar
- [ ] Context buffer (multi-message batching before sending to agent)
- [ ] Security hardening (cap_drop, no-new-privileges, hidepid, file permissions)

---

### Phase 7: CI + Polish ✅

- [x] GitHub Actions CI (lint, test, build on PR)
- [x] README with quickstart (update)

**Note:** GoReleaser release pipeline and install.sh were added in Phase 1. Migration guide dropped (no agent-fleet users).

---

## Code Reuse Summary

| agent-fleet source | agent-sandbox destination | Phase | Reuse % |
|-------------------|--------------------------|-------|---------|
| `pkg/gateway/` (proxy, sni) | `gateway/` | 3 | 80% |
| `pkg/gateway/mitm.go` | `gateway/internal/mitm/` | 4 | 80% |
| `runtimes/channels-bridge/src/` | `channel-manager/src/` | 4 | 70% |
| `runtimes/codex/` | `internal/plugins/codex/runtime.yaml` | 1 | 30% |
| `runtimes/codex/entrypoint.sh` | `internal/generate/generate.go` (inline) | 2 | 50% |
| `pkg/selfupdate/` | `cmd/agent-sandbox/main.go` (upgradeCmd) | 5 | 90% |
| `pkg/config/` | `internal/config/` | 1 | 20% |

## What Gets Dropped

- `runtimes/*/render.sh` — replaced by runtime.yaml + template engine
- `pkg/provider/resolver.go` — plugins fetched from GitHub Releases or local override
- `images/gateway/` — gateway source packaged by core-release.yml, fetched by CLI, compiled during Docker build
- `agent-fleet tools ctx` — no render scripts to support
- Template injection / user_base — replaced by custom-runtime feature
- Default-deny egress model — replaced by allow-all + MITM where needed
- Compile-time plugin Go interfaces — replaced by data-driven YAML

## agent-fleet Disposition

No active users — no migration needed. Repo kept for historical reference only.
