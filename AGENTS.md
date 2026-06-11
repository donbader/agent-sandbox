# Agent Instructions

**Generated:** 2025-06-12 | **Commit:** a67c01e | **Branch:** main

## Project

agent-sandbox — an opinionated agent sandbox orchestrator. Deploys AI coding agents inside Docker containers with transparent egress proxy, credential injection, and messaging channels.

## Tech Stack

- Language: Go 1.24+ (workspace uses Go 1.26)
- Build: Go workspace (go.work, single module)
- CLI: cobra
- Config: yaml.v3
- Tests: go test + testify
- Lint: golangci-lint (v2 config)
- Dev env: Flox (Nix-based, see `.flox/env/manifest.toml`)
- JS runtime: goja (embedded V8-like) + esbuild (TS bundling)

## Structure

```
scripts/
  shim.sh                 ← POSIX shell shim (installed as `agent-sandbox`)
  install.sh              ← Installer (places shim at ~/.agent-sandbox/bin/)
cmd/agent-sandbox-core/   ← Core CLI binary (generate, compose, audit, init, gateway-url)
cmd/agent-sandbox/        ← Legacy CLI entrypoint (being retired after v1.27.0)
cmd/gen-gateway-types/    ← Generates gateway.d.ts from Go annotations
cmd/lint-ts-annotations/  ← CI linter: ensures all .Set() calls have @ts-* annotations
internal/
  config/               ← fleet.yaml + agent.yaml parsing → Project model
  dotenv/               ← .env file loading
  envvar/               ← environment variable resolution
  generate/             ← Build artifact generation (builder structs + Go templates)
    v1/                 ← v1 generator (compose, dockerfile, gateway config)
    templates/          ← Go text/template files (.tmpl) for Dockerfiles, compose, entrypoints
  plugin/               ← plugin resolution, merging, rendering, types
  release/              ← core version fetching and caching from GitHub Releases
core/
  gateway/              ← Gateway binary source (transparent proxy + TS middleware)
  sdk/                  ← Gateway middleware interfaces
  presets/              ← Runtime presets (codex, claude-code, pi) — pure YAML
  plugins/              ← Feature plugins (github-pat, mcp-oauth, agent-manager-acp) — YAML + TS
examples/               ← Working example configurations
tests/                  ← Integration tests (shell-based E2E)
docs/                   ← Design documents, guides, reference, ADRs
```

## WHERE TO LOOK

| Task | Location | Notes |
|------|----------|-------|
| Add CLI command | `cmd/agent-sandbox-core/` | Cobra command files, one per subcommand |
| Change Dockerfile generation | `internal/generate/v1/dockerfile.go` + `templates/` | Uses Go text/template |
| Change compose output | `internal/generate/v1/compose.go` | Renders docker-compose.yml |
| Add/modify plugin loading | `internal/plugin/` | Resolver, renderer, merger |
| Add gateway JS API | `core/gateway/internal/jsruntime/` | Add `@ts-method` annotation, then `go generate` |
| Add runtime preset | `core/presets/<name>/runtime.yaml` | Pure data, no Go code |
| Add feature plugin | `core/plugins/<name>/` | plugin.yaml + src/*.ts |
| Modify proxy behavior | `core/gateway/internal/proxy/` | TCP/SNI/HTTP routing |
| Modify TLS MITM | `core/gateway/internal/mitm/` | Domain matching, cert generation |
| Config schema changes | `internal/config/config.go` | Structs with yaml tags |
| Run integration tests | `tests/integration/sandbox/run.sh` | Needs Docker, uses local httpbin |
| Update shim behavior | `scripts/shim.sh` | POSIX shell — tested in `tests/shim/` |

## CODE MAP

| Symbol | Type | Location | Role |
|--------|------|----------|------|
| `Project` | struct | `internal/config/project.go` | Unified fleet model (all agents + shared config) |
| `Config` | struct | `internal/config/config.go` | Parsed agent.yaml |
| `Generator` | struct | `internal/generate/v1/generator.go` | Orchestrates full generation pipeline |
| `Resolver` | struct | `internal/plugin/resolve.go` | Locates plugins by prefix (@builtin/ or ./) |
| `PluginDef` | struct | `internal/plugin/types.go` | Parsed plugin.yaml with contributes template |
| `Contributions` | struct | `internal/plugin/types.go` | What a plugin adds (runtime/gateway/sidecar) |
| `Preset` | struct | `internal/generate/v1/preset.go` | Runtime preset (base image, install, cmd) |
| `ComposeAgentEntry` | struct | `internal/generate/v1/compose.go` | Per-agent data for compose generation |

## Commands

```bash
# Dev environment (provides go, golangci-lint)
flox activate -- go build ./cmd/agent-sandbox-core/

# Build / Test / Lint
flox activate -- go build ./...
flox activate -- go test ./...
flox activate -- golangci-lint run ./...

# End-to-end (using shim)
agent-sandbox generate -C <dir>        # reads fleet.yaml → writes .build/<agent-name>/...
agent-sandbox compose up --build       # docker compose passthrough

# Local development (build from source)
agent-sandbox --dev -C examples/local-coding generate
```

## Conventions

- Conventional commits: feat:, fix:, docs:, chore:, refactor:, test:
- Tests for all exported functions
- golangci-lint must pass
- Each plugin is self-contained in its own directory
- SDK interfaces are stable — additive changes only
- `go generate` freshness enforced in CI (gateway.d.ts must match annotations)
- No Makefile — all build commands via `go build` / `flox activate`

## Plugin Architecture (Data-Driven)

**Key principle:** Plugin updates never require CLI upgrades. Plugins are TypeScript loaded at gateway runtime.

- **Presets:** `core/presets/<name>/runtime.yaml` — pure data (base image, install, CMD). Fetched from GitHub Releases, cached locally.
- **Plugins:** `core/plugins/<name>/plugin.yaml` + `src/*.ts` — MITM domain rules + TypeScript middleware. Fetched from Releases.

Plugins declare gateway rewriter rules (MITM domains, header injection) via `plugin.yaml` and implement request/response modification logic in TypeScript. The gateway loads and executes plugin TS at startup — no compilation step required.

## Testing Guidelines

**Write tests that verify behavior, not constants.**

- If a function only returns constants (no logic, no branching), don't unit test it
- Test the integration point where the output is consumed instead
- Use `//go:build integration` for tests that need Docker
- Run integration tests with `go test -tags integration ./...`
- Prefer fewer meaningful tests over many trivial ones
- Use `t.TempDir()` for isolated filesystem tests

## Design Docs

See `docs/` for architecture, plugin system, configuration, and security docs. Key references:
- `docs/roadmap.md` — phased implementation plan
- `docs/reference/channel-manager-protocol.md` — ACP protocol
- `docs/reference/docker-api-proxy.md` — Docker API validation
- `docs/reference/adr/` — Architecture Decision Records

## Key Principles

- Every phase produces a working `agent-sandbox generate && agent-sandbox compose up --build`
- Transparent proxy via iptables DNAT — agent doesn't know it's proxied
- Ephemeral by default — containers start fresh every restart
- All credentials through gateway — real creds never in container env

## History

Evolved from [agent-fleet](https://github.com/donbader/agent-fleet). This repo is self-contained — all design docs and reference material are here. No need to reference agent-fleet.

## Implementation Plan

See [docs/roadmap.md](docs/roadmap.md) for the phased implementation plan with checklists, config examples, and scope details.
