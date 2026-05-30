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

## Key Principles

- Every phase produces a working `agent-sandbox generate && agent-sandbox compose up --build`
- RuntimePlugin: one per agent, sets base image
- FeaturePlugin: multiple per agent, additive capabilities
- Gateway handles all credential injection (MITM where needed, passthrough otherwise)
- Bridge spawns agent as child process, loads channel plugins dynamically
- Ephemeral by default — containers start fresh every restart
