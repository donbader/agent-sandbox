# Plugins — Developer Notes

**Generated:** 2025-06-17 | **Branch:** feat/egress-hardening-and-docker-proxy

## Overview

Built-in plugins live at `core/plugins/<name>/`. Each plugin is self-contained: a `plugin.yaml` defining options and contributions, optional assets (Go binaries, TS middleware), and documentation.

## Plugin Directory Structure

Every plugin MUST have:

```
core/plugins/<name>/
  plugin.yaml          ← options schema + contributes block
  README.md            ← usage docs, config examples, security notes, limitations
```

Plugins with sidecar binaries:

```
core/plugins/<name>/
  plugin.yaml
  README.md
  cmd/<binary-name>/
    main.go
    Dockerfile
    *_test.go          ← unit tests for the binary
```

Plugins with TypeScript middleware:

```
core/plugins/<name>/
  plugin.yaml
  README.md
  src/
    <middleware>.ts
```

## Requirements Checklist

When creating or modifying a plugin:

- [ ] `plugin.yaml` with clear option descriptions and sensible defaults
- [ ] `README.md` covering: quick start, options table, security model, usage examples, limitations
- [ ] Unit tests for any Go binary (policy, mutation, config parsing, endpoint filtering)
- [ ] Verify `go build` / `go test` passes for sidecar binaries
- [ ] Integration test (manual or scripted) with `agent-sandbox generate` + `docker compose up`
- [ ] Dockerfile builds standalone (no dependency on the root go.mod for sidecar binaries)

## Plugin YAML Conventions

- Options should have `description` fields that explain what the option does
- Use sensible defaults for optional fields (`required: false` + `default: ...`)
- `contributes.sidecar.services` should NOT declare `networks:` (generator assigns sandbox network)
- System env vars (`SANDBOX_ID`, `SANDBOX_NETWORK`, `AGENT_NAME`) are auto-injected into sidecars — don't declare them in the plugin

## Sidecar Binary Conventions

- Sidecar binaries must be self-contained (stdlib only, or vendor dependencies)
- Dockerfile uses `go mod init` + `COPY *.go` pattern (no root go.mod dependency)
- Read config from environment variables (injected by plugin template + generator)
- Log with `slog` in JSON format
- Handle SIGTERM for graceful shutdown / cleanup
- Must compile with `CGO_ENABLED=0` for alpine-based images

## Testing

- Unit tests live alongside the Go source (`*_test.go` in the same package)
- Use `testify` for assertions (project standard)
- Test policy/validation logic without requiring Docker socket
- Mock Docker API with `httptest.NewServer` for integration-style tests
- Run `go test ./core/plugins/<name>/...` to verify

## Existing Plugins (Reference)

| Plugin | Type | Pattern |
|--------|------|---------|
| `github-pat` | Gateway middleware (TS) | Injects auth headers via MITM |
| `mcp-oauth` | Gateway routes + middleware | OAuth callback + token injection |
| `agent-manager-acp` | Sidecar (Node.js) | Runtime build + sidecar service |
| `agent-docker` | Sidecar (Go binary) | Policy proxy with Docker socket |
| `ssh` | Runtime (sshd) | Extra builds + gateway ingress |
| `home-override` | Runtime (volume) | Persistent home directory |
| `deploy-version` | Runtime (env) | Version injection |
