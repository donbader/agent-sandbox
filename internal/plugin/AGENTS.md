# Plugin System — Developer Notes

**Generated:** 2025-06-12 | **Commit:** a67c01e | **Branch:** main

## Overview

Resolves, renders, and merges plugin contributions at `generate` time. Plugins are data-driven (YAML + Go templates) — no Go code per plugin.

## Pipeline

```
installation list (from agent.yaml)
  → Resolver.Resolve(name) → PluginDef
  → RenderContributions(def, options, context) → Contributions
  → MergeContributions([]Contributions) → single Contributions
  → consumed by internal/generate/v1 for Dockerfile, compose, gateway
```

## Key Files

| File | Role |
|------|------|
| `types.go` | `PluginDef`, `Contributions`, `OptionSchema`, `AssetEntry` structs |
| `resolve.go` | `Resolver` — locates plugins by prefix (`@builtin/` or `./`) |
| `render.go` | `RenderContributions()` — template execution + YAML parse |
| `merge.go` | `MergeContributions()` — combines all plugin outputs |

## Resolution Rules

| Prefix | Source | Security |
|--------|--------|----------|
| `@builtin/name` | Bundled FS (from core release tarball) | Trusted |
| `./path` | Local filesystem relative to project dir | Path traversal blocked |
| Bare name | Rejected | — |

## Template Context

Plugin `contributes:` block is a Go template with access to:
- `{{ .plugin.options.KEY }}` — user-provided option values (validated against schema)
- `{{ .agent.name }}`, `{{ .agent.gateway.public_url }}` — agent config fields
- `{{ asset "name" }}` — resolved path to extracted asset directory

## Contributions Structure

Plugins contribute to three areas:
- **Runtime** — `extra_builds`, `pre_entrypoint`, `ports`, `volumes`, `cap_add`, `skip_userns`
- **Gateway** — `services`, `volumes`, `routes`, `middlewares` (TS scripts + domain scoping)
- **Sidecar** — `services` (image, build, env, ports, healthcheck)

## Test Patterns

- `fstest.MapFS` simulates bundled plugin filesystem (no real files needed)
- Inline YAML strings written to `t.TempDir()` for local plugin tests
- `testBundledFS()` helper returns mock FS for resolver tests
- Strict YAML parsing in tests catches unexpected fields early

## Anti-Patterns

- Never bypass the resolver — all plugins must go through prefix-based resolution
- Never access plugin files outside `BaseDir` — path traversal is a security boundary
- Never modify `PluginDef` after resolution — it's immutable once loaded
- Template rendering must use strict YAML parsing (`KnownFields(true)`)
