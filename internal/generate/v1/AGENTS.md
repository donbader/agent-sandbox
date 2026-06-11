# Generate v1 — Developer Notes

**Generated:** 2025-06-12 | **Commit:** a67c01e | **Branch:** main

## Overview

Orchestrates all build artifact generation: Dockerfiles, docker-compose.yml, gateway configs, entrypoints, and JSON schemas. Reads a `config.Project` and writes a complete `.build/` directory.

## Pipeline Flow

```
config.Project
  → Generator.RunProject()
    → per agent:
      1. Resolve plugins (via internal/plugin)
      2. Render + merge contributions
      3. Resolve preset (runtime.yaml → Preset struct)
      4. Render Dockerfile (preset + extra_builds from plugins)
      5. Render entrypoint.sh (pre_entrypoint from plugins)
      6. Write gateway config + build context
    → BuildProjectCompose() — unified docker-compose.yml
    → GenerateSchema() — JSON schema for IDE autocompletion
```

## Key Files

| File | Role |
|------|------|
| `generator.go` | `Generator` struct, `RunProject()` orchestration, preset loading |
| `dockerfile.go` | Dockerfile rendering (preset-based or custom image) |
| `compose.go` | docker-compose.yml generation, `ComposeAgentEntry` builder |
| `gateway_config.go` | Gateway `config.yaml` (MITM domains, auth headers, public URL) |
| `gateway_build.go` | Gateway build context (binary, plugins.yaml, TS sources, Dockerfile) |
| `preset.go` | `Preset` struct, YAML parsing of `runtime.yaml` |
| `schema.go` | JSON Schema generation from config structs |
| `entrypoint.go` | Shell entrypoint rendering |

## Conventions

- Uses Go `text/template` via `.tmpl` files in `../templates/`
- Template data structs are defined alongside the rendering code
- `ComposeAgentEntry` is the central per-agent struct passed to compose template
- Presets loaded from `coreDir/presets/<name>/runtime.yaml` — never hardcoded

## Test Patterns

- All tests use `t.TempDir()` for isolated output
- Shared `testPresets` var for deterministic Dockerfile tests
- `mustParseConfig(t, path)` helper parses inline YAML fixtures
- Tests verify generated file contents via `assert.Contains` / YAML unmarshaling
- No Docker needed for unit tests (use `//go:build integration` for E2E)

## Anti-Patterns

- Never hardcode base images — always go through `Preset`
- Never write compose fields that belong to plugins — use `Contributions` merge
- Template logic must stay in `.tmpl` files, not in Go string builders
