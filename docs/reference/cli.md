# CLI Reference

## Architecture

The `agent-sandbox` CLI is split into two layers:

1. **Shim** (`~/.agent-sandbox/bin/agent-sandbox`) — a POSIX shell script that resolves the correct core version and execs into it
2. **Core** (`agent-sandbox-core`) — the Go binary that does the real work

From the user's perspective, you just run `agent-sandbox <command>`. The shim is transparent.

## Version Resolution

When you run any command, the shim:

1. Reads `core_version` from `agent.yaml` in the current (or `-C`) directory
2. If `latest`, queries GitHub API for the newest `v*` release (cached 1h)
3. Downloads the core binary if not already cached at `~/.agent-sandbox/core/<version>/`
4. Execs into `agent-sandbox-core` with all original arguments

For commands that don't need a project (`version`, `upgrade`), the shim handles them directly.

## Shim Commands

These are handled by the shell script itself — no core binary needed:

| Command | Description |
|---------|-------------|
| `agent-sandbox upgrade` | Update the shim to the latest version |
| `agent-sandbox version` | Print shim version and resolved core version |

## Core Commands

These are handled by `agent-sandbox-core`:

| Command | Description |
|---------|-------------|
| `agent-sandbox init` | Interactive project scaffold |
| `agent-sandbox generate` | Read config, generate `.build/` artifacts |
| `agent-sandbox compose ...` | Docker compose passthrough |
| `agent-sandbox audit` | Verify running sandbox meets security contract |

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-C, --dir` | `.` | Project directory containing fleet.yaml |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `AGENT_SANDBOX_RUNTIME` | Override container runtime binary (`docker` or `podman`). Takes priority over `runtime_engine` in config. |
| `AGENT_SANDBOX_CACHE` | Override core cache directory (default: `~/.agent-sandbox/core/`) |

## generate

Reads `fleet.yaml` and per-agent `agent.yaml` files, then produces the `.build/` directory containing all Docker artifacts.

```bash
agent-sandbox generate
agent-sandbox -C examples/multi-agent generate
```

**Output:**
- `.build/<agent-name>/Dockerfile` — agent container image
- `.build/<agent-name>/entrypoint.sh` — iptables + CA + privilege drop
- `.build/<agent-name>/gateway/` — gateway config and binary
- `.build/docker-compose.yml` — all services
- `.build/schema.json` — JSON Schema for agent.yaml
- `.env.example` — all `${VAR}` references found in configs (project root)

## compose

Passthrough to `docker compose` (or `podman compose`) with auto-injected flags:

- `-f .build/docker-compose.yml`
- `--project-name <folder-name>`
- `--env-file .env` (if .env exists)

```bash
agent-sandbox compose up --build -d     # build + start detached
agent-sandbox compose down -v           # stop + remove volumes
agent-sandbox compose logs -f           # stream all logs
agent-sandbox compose logs agent-001    # one service
agent-sandbox compose exec -it --user agent coder codex   # exec into agent
agent-sandbox compose ps                # status
agent-sandbox compose restart coder     # restart one service
```

## audit

Runs security checks against a live running sandbox. See [Audit Reference](audit.md) for details.

```bash
agent-sandbox audit
agent-sandbox -C examples/multi-agent audit
```

Exit code is non-zero if any check fails.

## init

Interactive scaffold that creates `fleet.yaml`, an agent subdirectory with `agent.yaml`, and `.env.example`:

```bash
mkdir my-agent && cd my-agent
agent-sandbox init
```

Asks for agent name and runtime. Auto-detects `gh auth token` if available.

## upgrade

Updates the shim script to the latest version:

```bash
agent-sandbox upgrade
```

This does not change the core version used by your projects — that's controlled by `core_version` in each project's `agent.yaml`.

## Typical Workflow

```bash
# First time
agent-sandbox generate
agent-sandbox compose up --build -d
agent-sandbox audit

# After config changes
agent-sandbox generate
agent-sandbox compose up --build -d

# Tear down
agent-sandbox compose down -v
```

## Local Development

Use `--dev` to build from source and run from the repo root:

```bash
agent-sandbox --dev -C examples/local-coding generate
# [dev] Building from source...
# Generated .build/ in .../examples/local-coding
```

The binary is built to `./core/agent-sandbox-core` so it resolves sibling assets (plugins, presets, templates, gateway) from the `core/` directory automatically.

Requires `go` or `flox` on PATH.
