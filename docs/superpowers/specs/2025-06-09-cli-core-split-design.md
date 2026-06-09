# CLI/Core Responsibility Split

**Date:** 2025-06-09  
**Status:** Approved  
**Goal:** Replace the Go CLI binary with a minimal POSIX shell shim that delegates to a versioned core binary, making core the primary release artifact.

## Motivation

The current Go CLI bundles too many concerns: config parsing, plugin resolution, template rendering, compose passthrough, self-update. Every core change requires a CLI release. Users must upgrade the CLI to get new features even when the gateway/plugin system is the only thing that changed.

By making core the primary artifact and reducing the CLI to a transparent version-resolving shim:
- Plugin and runtime updates ship without CLI upgrades
- Per-project version pinning becomes natural
- The install footprint drops to a single shell script
- Core owns its own schema (init, generate, audit all stay consistent with the version)

## Architecture

```
User runs `agent-sandbox <cmd>`
        │
        ▼
┌─────────────────────────────┐
│  Shim (~/.agent-sandbox/bin)│
│  POSIX shell, ~50 lines    │
├─────────────────────────────┤
│  Owns: upgrade, version    │
│  Everything else → core    │
└──────────────┬──────────────┘
               │ resolve version → download/cache → exec
               ▼
┌─────────────────────────────┐
│  Core Binary (Go)           │
│  agent-sandbox-core         │
├─────────────────────────────┤
│  init, generate, compose,   │
│  gateway-url, audit         │
└─────────────────────────────┘
```

## The Shim

A ~50-line POSIX shell script (`/bin/sh`). Only dependency: `curl`.

### Commands Owned by Shim

| Command | Behavior |
|---------|----------|
| `upgrade` | Re-download the shim itself from GitHub |
| `version` | Print shim version + detected core version |

### Delegation Logic

Everything else is forwarded to core:

```sh
case "$1" in
  upgrade) re_download_shim ;;
  version) print_versions ;;
  *)
    if [ -f agent.yaml ]; then
      ver=$(grep '^core_version:' agent.yaml | awk '{print $2}')
      if [ -z "$ver" ]; then
        ver=$(fetch_latest)
        warn "No core_version in agent.yaml. Using latest ($ver)."
        warn "Pin it: add 'core_version: $ver' to your agent.yaml"
      fi
    elif [ "$1" = "init" ]; then
      ver=$(fetch_latest)
    else
      die "No agent.yaml found. Run 'agent-sandbox init' first."
    fi
    ensure_cached "$ver"
    exec "$CACHE_DIR/$ver/agent-sandbox-core" "$@"
    ;;
esac
```

### Version Resolution Rules

| Context | Behavior |
|---------|----------|
| `agent.yaml` exists with `core_version` | Use that version |
| `agent.yaml` exists without `core_version` | Use latest + print deprecation warning |
| No `agent.yaml` + `init` command | Fetch latest from GitHub API |
| No `agent.yaml` + any other command | Error: "Run `agent-sandbox init` first" |

The "missing `core_version` fallback" is a migration aid. Remove it after 2-3 core releases.

## The Core Binary

A Go binary (`agent-sandbox-core`) that ships inside the platform-specific core tarball. Contains all real logic.

### Commands

| Command | Responsibility |
|---------|---------------|
| `init` | Interactive scaffolding → writes `agent.yaml` with `core_version: <self>` |
| `generate` | Parse agent.yaml, resolve plugins, render templates, write `.build/` |
| `compose` | Docker compose passthrough with auto-injected `-f`, `--project-name`, `--env-file` |
| `gateway-url` | Query docker compose port for dynamic gateway port |
| `audit` | Validate running sandbox security contracts |

### Path Resolution

Core knows its own root path (the directory it was extracted to). It resolves sibling assets:

```
$CORE_ROOT/
├── agent-sandbox-core     ← self
├── gateway-linux-amd64    ← copied into .build/ during generate
├── gateway-linux-arm64
├── plugins/               ← TS source copied into .build/
├── presets/               ← runtime.yaml read during generate
└── templates/             ← .tmpl files rendered during generate
```

No env vars or external config needed. `os.Executable()` → resolve symlinks → parent dir = root.

## Filesystem Layout

```
~/.agent-sandbox/
├── bin/
│   └── agent-sandbox          ← the shim (user adds to PATH)
└── core/
    ├── 0.13.0/
    │   ├── agent-sandbox-core
    │   ├── gateway-linux-amd64
    │   ├── gateway-linux-arm64
    │   ├── plugins/
    │   ├── presets/
    │   └── templates/
    └── 0.14.0/
        └── ...
```

Single directory owns everything. `rm -rf ~/.agent-sandbox` is a clean uninstall.

## Release Artifacts

### Core Releases (primary)

Tag format: `core-v0.13.0`

```
core-v0.13.0-darwin-arm64.tar.gz
core-v0.13.0-darwin-amd64.tar.gz
core-v0.13.0-linux-amd64.tar.gz
core-v0.13.0-linux-arm64.tar.gz
```

Each tarball contains: host binary + gateway binaries + plugins + presets + templates.

### Shim Releases

The shim is a single file. Released as a raw script (not a tarball). The install script and the shim's `upgrade` command both fetch it directly.

## Install Experience

```bash
curl -fsSL https://get.agent-sandbox.dev/install.sh | sh
# → downloads shim to ~/.agent-sandbox/bin/agent-sandbox
# → makes it executable
# → prints: "Add ~/.agent-sandbox/bin to your PATH"
```

## First-Run Experience

```bash
$ agent-sandbox init
# shim: no agent.yaml, command is "init"
# shim: fetch latest core version from GitHub API → 0.13.0
# shim: download core-v0.13.0-darwin-arm64.tar.gz → ~/.agent-sandbox/core/0.13.0/
# shim: exec agent-sandbox-core init
# core: interactive wizard → writes agent.yaml with core_version: 0.13.0

$ agent-sandbox generate
# shim: reads agent.yaml → core_version: 0.13.0
# shim: already cached → exec agent-sandbox-core generate
# core: parses config, resolves plugins, writes .build/

$ agent-sandbox compose up --build
# shim: same version resolution
# core: docker compose -f .build/docker-compose.yml up --build
```

## Migration Path

### Final Go CLI Release (v1.27.0)

The existing `agent-sandbox upgrade` command becomes the migration vector:

1. Detects it's the old Go binary (checks for `~/.agent-sandbox/bin/agent-sandbox` absence)
2. Downloads shim → `~/.agent-sandbox/bin/agent-sandbox`
3. Makes it executable
4. Prints instructions:
   ```
   Migration complete.
   Add ~/.agent-sandbox/bin to your PATH (before your current binary location).
   Then remove the old agent-sandbox binary.
   ```

### Existing `agent.yaml` Files

Files without `core_version` continue to work via the "use latest + warn" fallback:

```
⚠ No core_version in agent.yaml. Using latest (0.13.0).
  Pin it: add 'core_version: 0.13.0' to your agent.yaml
```

This fallback is removed after 2-3 core releases.

## What Gets Deleted from the Go Codebase

After migration, the current CLI entrypoint (`cmd/agent-sandbox/`) is replaced by the core binary entrypoint. The Go code largely moves — it doesn't get deleted, it gets reorganized:

| Current location | Becomes |
|------------------|---------|
| `cmd/agent-sandbox/main.go` | `cmd/agent-sandbox-core/main.go` (or stays at same path, just builds differently) |
| `internal/release/` | Removed — shim handles downloading |
| Upgrade logic in main.go | Removed — shim owns this |
| All other `internal/` packages | Unchanged — still used by core binary |

## Security Considerations

- The shim downloads over HTTPS from GitHub Releases (`https://github.com/<org>/<repo>/releases/download/core-v$VER/...`)
- Latest version resolved via GitHub API: `GET /repos/<org>/<repo>/releases?per_page=10`, filter for `core-v*` tags, take newest
- Consider adding checksum verification: download `.sha256` alongside tarball, verify before extracting
- Core binary executes with user's permissions — no privilege escalation
- Cached binaries at `~/.agent-sandbox/core/` are user-owned, same trust model as `~/.cargo/` or `~/go/bin/`

## Documentation Updates

The following docs need updates to reflect the new architecture:

| Document | Changes |
|----------|---------|
| `docs/getting-started.md` | Install instructions change from binary download to `curl \| sh`. First-run flow updated. |
| `docs/reference/cli.md` | Rewrite: document shim commands (`upgrade`, `version`) separately from core commands (`init`, `generate`, `compose`, `gateway-url`, `audit`). Explain version resolution. |
| `docs/configuration.md` | Document new `core_version` field in `agent.yaml` (required, semver, controls which core is used). |
| `docs/internals/build-pipeline.md` | Update release process: core tarball build, platform matrix, shim release mechanism. |
| `docs/troubleshooting.md` | Add shim-specific issues: cache corruption, version mismatch, network failures during download, PATH conflicts with old binary. |
| `docs/guides/creating-plugins.md` | Verify examples still work (they should — `generate` and `compose` commands unchanged from user perspective). |
| `AGENTS.md` | Update project structure, commands section, and build instructions. |
| `README.md` | Update install and quickstart sections. |

### New Documentation

| Document | Purpose |
|----------|---------|
| `docs/internals/cli-core-split.md` | Architecture explanation: why the split, how version resolution works, how releases relate to each other. |
| `docs/reference/migration.md` | Step-by-step migration guide for existing users (from Go binary to shim). |

## Open Questions

None — all design decisions resolved.
