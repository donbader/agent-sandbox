# Design: Single Source of Truth for `workdir`

**Date:** 2025-06-03  
**Status:** Approved  

## Problem

The agent's working directory is hardcoded in multiple places with inconsistent values:

- `Dockerfile.agent.tmpl` → `WORKDIR /home/{{ .User }}`
- `session-manager.ts` → `cwd: "/home/agent"`
- `channel-manager/src/index.ts` → `config.cwd ?? "/workspace"`
- `channel-manager/src/wrapper-commands.ts` → `process.env.HOME ?? "/workspace"`

There is no user-facing configuration to control this, and the scattered values create confusion.

## Solution

Introduce a top-level `workdir` field in `agent.yaml` that serves as the single source of truth. The field supports Go template interpolation using the existing `{{ .AGENT_HOME }}` builtin variable (already used for `runtime_volumes` resolution in `internal/generate/builtins.go`).

## Template Variables

| Variable | Derivation | Already Exists | Example |
|----------|-----------|----------------|---------|
| `.AGENT_HOME` | `/home/` + runtime `User` field (defaults to `"agent"`) | Yes (`builtins.go`) | `/home/agent` |

## Config Schema

```yaml
name: coder
runtime: codex
workdir: "{{ .AGENT_HOME }}/workspace"
```

When `workdir` is omitted, it defaults to `{{ .AGENT_HOME }}` (resolves to `/home/agent`), preserving backward compatibility.

A literal path without template syntax is also valid:

```yaml
workdir: /opt/workspace
```

## Resolution Pipeline

1. `internal/config` parses the raw `workdir` string from `agent.yaml`
2. `internal/resolve` passes the raw string through to generation
3. `internal/generate/builtins.go` already computes `AGENT_HOME` from runtime `User` — extend `resolveFeatureBuiltins()` to also resolve the `workdir` field using the same mechanism
4. If `workdir` is empty after parse, set it to the value of `AGENT_HOME`
5. The resolved string is stored in the generator and passed to all downstream templates and configs

## Propagation

The resolved `workdir` value flows to every consumer:

| Consumer | Current Behavior | New Behavior |
|----------|-----------------|--------------|
| `Dockerfile.agent.tmpl` WORKDIR | `WORKDIR /home/{{ .User }}` | `WORKDIR {{ .Workdir }}` |
| `Dockerfile.single.tmpl` WORKDIR | `WORKDIR /home/{{ .User }}` | `WORKDIR {{ .Workdir }}` |
| `channel-manager-config.json` | No `cwd` field emitted | Emits `"cwd": "<resolved>"` |
| `channel-manager/src/index.ts` | `config.cwd ?? "/workspace"` | `config.cwd` (required) |
| `channel-manager/src/wrapper-commands.ts` | `process.env.HOME ?? "/workspace"` | Uses `config.cwd` |
| `session-manager.ts` ACP session | Hardcoded `"/home/agent"` | Reads `cwd` from channel-manager config |

## Dockerfile Directory Creation

When resolved `workdir` differs from `AGENT_HOME`, the Dockerfile must create it:

```dockerfile
{{ if ne .Workdir .AgentHome -}}
RUN mkdir -p {{ .Workdir }} && chown {{ .User }}:{{ .User }} {{ .Workdir }}
{{ end -}}
WORKDIR {{ .Workdir }}
```

## File Changes

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `Workdir string \`yaml:"workdir"\`` to `AgentConfig` |
| `internal/generate/builtins.go` | Extend `resolveFeatureBuiltins()` to also resolve `workdir` using existing `AGENT_HOME` variable, default to `AGENT_HOME` when empty |
| `internal/generate/dockerfile.go` | Add `Workdir` and `AgentHome` to `DockerfileBuilder`, populate from resolved config |
| `internal/generate/templates/Dockerfile.agent.tmpl` | Use `{{ .Workdir }}`, add conditional mkdir |
| `internal/generate/templates/Dockerfile.single.tmpl` | Same as above |
| `internal/generate/channel_manager.go` | Emit `cwd` in `channel-manager-config.json` |
| `channel-manager/src/index.ts` | Remove `/workspace` fallback, require `config.cwd` |
| `channel-manager/src/wrapper-commands.ts` | Use config cwd instead of env fallback |
| `internal/plugins/telegram/channel/session-manager.ts` | Read cwd from config instead of hardcoded value |
| `internal/generate/schema.go` | Add `workdir` to JSON schema |
| `examples/multi-agent/coder/agent.yaml` | Use `{{ .AGENT_HOME }}` in `runtime_volumes` |
| `docs/` (configuration.md, plugins.md, cli-and-ux.md, README.md) | Update hardcoded `/home/agent` examples |

## Backward Compatibility

- `workdir` omitted → resolves to `AGENT_HOME` → `/home/agent` (same as today)
- Existing `agent.yaml` files without `workdir` continue to work unchanged
- Channel-manager config gains a `cwd` field; the channel-manager code is updated atomically in the same change

## Edge Cases

- Template parse error in `workdir` → fail fast at `agent-sandbox generate` with a clear error message
- `workdir` set to a path outside `AGENT_HOME` → works fine, Dockerfile creates and chowns the directory
- `User` override in runtime → `AGENT_HOME` adjusts accordingly
