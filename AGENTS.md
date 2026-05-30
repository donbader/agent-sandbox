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
sdk/                    ← Plugin interfaces (RuntimePlugin, FeaturePlugin)
cmd/agent-sandbox/      ← CLI entrypoint (main.go)
internal/
  config/               ← agent.yaml parsing
  generate/             ← Dockerfile + docker-compose.yml generation
plugins/
  codex/                ← RuntimePlugin: codex
gateway/                ← (Phase 3) Transparent proxy
bridge/                 ← (Phase 4) TypeScript bridge runtime
docs/                   ← Design documents
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
    g := &Generator{Config: cfg, Runtime: codex.New(), OutDir: outDir}
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
- RuntimePlugin: one per agent, sets base image
- FeaturePlugin: multiple per agent, additive capabilities
- Gateway handles all credential injection (MITM where needed, passthrough otherwise)
- Bridge spawns agent as child process, loads channel plugins dynamically
- Ephemeral by default — containers start fresh every restart

## History

Evolved from [agent-fleet](https://github.com/donbader/agent-fleet). This repo is self-contained — all design docs and reference material are here. No need to reference agent-fleet.

## Phase Implementation Guide

Each phase builds on the previous. After each phase, `agent-sandbox generate && agent-sandbox compose up --build` must work.

### Phase 2: home-version-control Feature

Implement `plugins/home-version-control/plugin.go` as a FeaturePlugin:
- `ImageContribution.Commands` → RUN instructions in Dockerfile
- `EntrypointContribution.Hooks` → scripts that run on container start
- `ComposeContribution.Volumes` → named volumes in docker-compose.yml
- Update `internal/generate/` to merge FeatureContributions into Dockerfile/compose
- Add entrypoint.sh that runs hooks then starts agent
- Home override: user's `./home/` dir → COPY to `/opt/home-override/` → cp on start

### Phase 3: Gateway (Network Enforcement)

Implement `gateway/` as a separate Go module (compiled into container image):
- TCP listener on port 443 (iptables redirects all outbound traffic)
- SNI extraction from TLS ClientHello
- Passthrough mode: pipe bytes directly to destination (no MITM)
- DNS resolver: intercept UDP port 53, resolve via gateway
- Entrypoint: iptables setup → start gateway → start agent
- Multi-stage Dockerfile: compile gateway binary + runtime image
- Gateway runs as separate user (agent cannot kill it)
- Read `docs/reference/adr/002-transparent-proxy.md` for design rationale

### Phase 4: Bridge + Telegram

Implement `bridge/` as TypeScript runtime:
- Spawns agent CLI as child process
- Loads channel plugins from `/opt/bridge/plugins/<name>/`
- ACP protocol: stdin/stdout JSON messages between bridge and agent
- Read `docs/reference/bridge-protocol.md` for protocol spec

Implement `plugins/telegram/plugin.go` as FeaturePlugin:
- GatewayContribution: MITM on `api.telegram.org`, inject bot token
- BridgeContribution: embed TypeScript channel plugin (grammy library)
- Config: `bot_token`, `allowed_users`

### Phase 5: All Remaining Features

- `plugins/github/` — GatewayContribution: MITM on `api.github.com`, inject PAT as Authorization header
- `plugins/docker/` — ComposeContribution: DinD sidecar service. GatewayContribution: DockerHandler validates API requests. Read `docs/reference/docker-api-proxy.md`
- `plugins/mcp-oauth/` — GatewayContribution: OAuth2 dynamic client registration + token refresh
- `plugins/static-header/` — GatewayContribution: generic header injection for any endpoint
- Additional runtimes: `plugins/claude-code/`, `plugins/pi/`

### Phase 6: CLI Polish + Multi-Agent

- `init` command: interactive scaffold (detect gh auth, suggest features)
- `validate` command: config check + helpful errors
- `plugins` command: list/info
- `upgrade` command: self-update (check GitHub releases, download, replace binary)
- `fleet.yaml` support: multiple agents, shared features with per-agent overrides

### Phase 7: CI + Release

- GitHub Actions: lint (golangci-lint), test, build
- GoReleaser: multi-arch binaries
- install.sh one-liner
- README with quickstart
