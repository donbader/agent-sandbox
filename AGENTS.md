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
  plugins/              ← core plugins (embedded in CLI)
    codex/              ← runtime.yaml
    custom-runtime/     ← feature.yaml + plugin.go (owns its logic)
ext/
  plugins/              ← external plugins (per-plugin versioning)
gateway/                ← (Phase 3) Gateway core source (embedded in CLI)
bridge/                 ← Bridge TypeScript runtime (grammy + agent process spawning)
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

### Runtime Plugins (Pure Data — embedded in CLI)

```
plugins/runtime/<name>/runtime.yaml     ← base image, install commands, CMD, ports
plugins/runtime/<name>/Dockerfile.tmpl  ← optional custom template
```

No Go code. CLI reads YAML and generates Dockerfile. Runtime plugins are core — they ship with the CLI binary.

### Feature Plugins (Hybrid — Data + Code)

Core feature plugins live in `internal/plugins/<name>/` and are embedded in the CLI:

```
internal/plugins/<name>/feature.yaml   ← metadata, config schema
internal/plugins/<name>/plugin.go      ← Go: implements FeaturePlugin interface (owns its logic)
```

External feature plugins live in `ext/plugins/<name>/` with optional gateway/bridge code:

```
ext/plugins/<name>/feature.yaml        ← metadata, config schema, hosts
ext/plugins/<name>/gateway/            ← optional Go: compiled during Docker build
ext/plugins/<name>/bridge/             ← optional TypeScript: copied into image
```

- Each plugin implements `resolve.FeaturePlugin` interface
- Plugins register themselves via `init()` → `resolve.RegisterFeature()`
- Plugin owns its contribution extraction logic (not hardcoded in resolve)
- `internal/plugins/register.go` imports all core plugins for side-effect registration

### Plugin Resolution Order

**Runtime plugins:**
1. `./ext/plugins/<name>/runtime.yaml` — local project directory (user overrides)
2. Built-in core plugins (embedded in CLI via go:embed from `internal/plugins/`)

**Feature plugins:**
1. `./ext/plugins/<name>/feature.yaml` — local project directory
2. Built-in core plugins (embedded in CLI)
3. (Future: fetched from plugin registry)

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

## Implementation Plan

See [docs/migration-plan.md](docs/migration-plan.md) for the phased implementation plan with checklists, config examples, and scope details.
