# Agent Instructions

## Project

agent-sandbox — an opinionated agent sandbox orchestrator. Deploys AI coding agents inside Docker containers with transparent egress proxy, credential injection, and messaging channels.

## Tech Stack

- Language: Go 1.24+
- Build: Go workspace (go.work)
- CLI: cobra
- Config: yaml.v3
- Tests: go test + testify
- Lint: golangci-lint

## Structure

```
cmd/agent-sandbox/      ← CLI entrypoint (generic template engine)
internal/
  config/               ← agent.yaml parsing
  generate/             ← Dockerfile + docker-compose.yml generation
  resolve/              ← plugin resolution (local → embedded)
plugins/
  codex/                ← runtime.yaml (data-driven)
  claude-code/          ← runtime.yaml
  github/               ← feature.yaml + gateway/handler.go
  telegram/             ← feature.yaml + gateway/ + bridge/
  home-version-control/ ← feature.yaml (pure config, no code)
gateway/                ← (Phase 3) Gateway core source (embedded in CLI)
bridge/                 ← (Phase 4) Bridge TypeScript runtime (embedded in CLI)
sdk/                    ← Gateway handler interface (for feature plugins)
docs/                   ← Design documents
templates/              ← Dockerfile.tmpl, entrypoint.sh template
```

## Commands

```bash
# Build
go build ./cmd/agent-sandbox/

# Test
go test ./...

# Lint (when golangci-lint is available)
golangci-lint run ./...

# End-to-end
agent-sandbox generate -d <dir>        # reads agent.yaml → writes .build/
agent-sandbox compose up --build       # docker compose passthrough
```

## Conventions

- Conventional commits: feat:, fix:, docs:, chore:, refactor:, test:
- Tests for all exported functions
- golangci-lint must pass
- Each plugin is self-contained in its own directory
- SDK interfaces are stable — additive changes only

## Plugin Architecture (Data-Driven)

**Key principle:** Plugin updates never require CLI upgrades. CLI is a generic template engine.

### Runtime Plugins (Pure Data)

```
plugins/<name>/runtime.yaml     ← base image, install commands, CMD
plugins/<name>/Dockerfile.tmpl  ← optional custom template
```

No Go code. CLI reads YAML and generates Dockerfile.

### Feature Plugins (Data + Code)

```
plugins/<name>/feature.yaml     ← metadata, config schema, hosts
plugins/<name>/gateway/         ← optional Go: compiled during Docker build
plugins/<name>/bridge/          ← optional TypeScript: copied into image
```

- `feature.yaml` is always present (metadata)
- `gateway/` Go code compiles during Docker build (not CLI build)
- `bridge/` TypeScript is copied into image, loaded at runtime

### Plugin Resolution Order

1. `./plugins/<name>/` — local project directory (user overrides)
2. Inline definition in agent.yaml (custom runtimes only)
3. Built-in plugins (embedded in CLI via go:embed)

## Testing Guidelines

**Write tests that verify behavior, not constants.**

Don't write:
```go
// USELESS — just testing that a hardcoded value equals itself
func TestPlugin_Name(t *testing.T) {
    assert.Equal(t, "codex", New().Name())
}
```

Do write:
```go
// USEFUL — tests that the generated output actually works
func TestGenerator_Run(t *testing.T) {
    g := &Generator{Config: cfg, RuntimeYAML: runtimeData, OutDir: outDir}
    require.NoError(t, g.Run())
    df, _ := os.ReadFile(filepath.Join(outDir, "Dockerfile"))
    assert.Contains(t, string(df), "FROM node:22-slim")
}
```

Rules:
- If a function only returns constants (no logic, no branching), don't unit test it
- Test the integration point where the output is consumed instead
- Use `//go:build integration` for tests that need Docker
- Run integration tests with `go test -tags integration ./...`
- Prefer fewer meaningful tests over many trivial ones

## Design Docs

See docs/ for architecture, plugin system, configuration, and security docs.
Refer to docs/migration-plan.md for the phased implementation plan.

### Reference Docs

- `docs/reference/bridge-protocol.md` — ACP protocol (bridge ↔ agent communication)
- `docs/reference/docker-api-proxy.md` — Docker API validation design
- `docs/reference/adr/` — Architecture Decision Records (why transparent proxy, why Go, etc.)

## Key Principles

- Every phase produces a working `agent-sandbox generate && agent-sandbox compose up --build`
- Plugin updates never require CLI upgrades
- Runtime plugins are pure data (YAML) — no Go code
- Feature plugins are hybrid (YAML + optional Go gateway + optional TypeScript bridge)
- Gateway handlers compile during Docker build, not CLI build
- Bridge spawns agent as child process, loads channel plugins dynamically
- Ephemeral by default — containers start fresh every restart
- All credentials through gateway — real creds never in container env

## History

Evolved from [agent-fleet](https://github.com/donbader/agent-fleet). This repo is self-contained — all design docs and reference material are here. No need to reference agent-fleet.

## Phase Implementation Guide

Each phase builds on the previous. After each phase, `agent-sandbox generate && agent-sandbox compose up --build` must work.

### Phase 1 Remaining: Data-Driven Runtime

Convert the current Go-based codex plugin to data-driven:
- Create `plugins/codex/runtime.yaml` (base_image, install, cmd)
- Update `internal/generate/` to read runtime.yaml instead of calling Go plugin
- Add plugin resolution logic (local → embedded)
- Support inline runtime definition in agent.yaml
- Remove `plugins/codex/plugin.go` (replaced by runtime.yaml)

### Phase 2: home-version-control Feature

Implement `plugins/home-version-control/feature.yaml`:
- CLI reads feature.yaml config schema
- Merges `commands` into Dockerfile as RUN instructions
- Merges `entrypoint_hooks` into entrypoint script
- Merges `runtime_volumes` into docker-compose.yml
- Home override: user's `./home/` dir → COPY to `/opt/home-override/` → cp on start
- Add entrypoint.sh template that runs hooks then starts agent

### Phase 3: Gateway (Network Enforcement)

Implement `gateway/` as a Go module (embedded in CLI, compiled during Docker build):
- TCP listener on port 443 (iptables redirects all outbound traffic)
- SNI extraction from TLS ClientHello
- Passthrough mode: pipe bytes directly to destination
- DNS resolver: intercept UDP port 53, resolve via gateway
- `RequestHandler` interface for feature gateway handlers
- Handler registry generation (CLI writes imports for active features)
- Multi-stage Dockerfile: compile gateway binary + runtime image
- Gateway runs as separate user (agent cannot kill it)
- Read `docs/reference/adr/002-transparent-proxy.md` for design rationale

### Phase 4: Bridge + Telegram

Implement `bridge/` as TypeScript runtime (embedded in CLI):
- Spawns agent CLI as child process
- Loads channel plugins from `/opt/bridge/plugins/<name>/`
- ACP protocol: stdin/stdout JSON messages between bridge and agent
- Read `docs/reference/bridge-protocol.md` for protocol spec

Implement `plugins/telegram/`:
- `feature.yaml`: config schema (bot_token, allowed_users), gateway hosts
- `gateway/handler.go`: MITM on api.telegram.org, inject bot token
- `bridge/src/telegram.ts`: grammy-based channel plugin

### Phase 5: All Remaining Features

- `plugins/github/` — feature.yaml + gateway/handler.go (PAT injection)
- `plugins/docker/` — feature.yaml + gateway/handler.go (API validation) + compose sidecar
- `plugins/mcp-oauth/` — feature.yaml + gateway/handler.go (OAuth2 flow)
- `plugins/static-header/` — feature.yaml + gateway/handler.go (generic header)
- `plugins/claude-code/runtime.yaml` — claude-code runtime
- `plugins/pi/runtime.yaml` — pi runtime

### Phase 6: CLI Polish + Multi-Agent

- `init` command: interactive scaffold (detect gh auth, suggest features)
- `validate` command: config check + helpful errors
- `plugins` command: list available plugins (embedded + local)
- `upgrade` command: self-update (check GitHub releases, download, replace binary)
- `fleet.yaml` support: multiple agents, shared features with per-agent overrides

### Phase 7: CI + Polish

- GitHub Actions CI: lint (golangci-lint), test, build on PR
- README with quickstart
- Migration guide for agent-fleet users
- GoReleaser release pipeline already in place (Phase 1)
