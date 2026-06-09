# CLI/Core Split Architecture

## Why the Split

The original `agent-sandbox` was a single Go binary distributed via GoReleaser. This created friction:

- **Plugin updates required CLI upgrades** — even though plugins are data-driven, users had to update the CLI to get new template logic or asset-fetching changes
- **No per-project version pinning** — teams couldn't lock a specific version per project, leading to "works on my machine" issues
- **Monolithic releases** — every change (gateway fix, new preset, CLI tweak) required a full release

The split solves all three: the shim is stable, the core is versioned per-project, and core releases ship independently.

## The Shim

`~/.agent-sandbox/bin/agent-sandbox` is a POSIX shell script (~100 lines). It:

1. Handles `version` and `upgrade` directly (no core needed)
2. For all other commands, resolves the target core version:
   - Reads `core_version` from `agent.yaml` in the working directory (or `-C` target)
   - If `latest`, queries GitHub API for newest `core-v*` tag (cached 1h)
   - If a specific version (e.g. `v0.13.0`), uses it directly
3. Checks if that version is cached at `~/.agent-sandbox/core/<version>/`
4. If not cached, downloads the platform-appropriate tarball from GitHub Releases
5. Writes a `.complete` sentinel file after successful extraction
6. Execs into `~/.agent-sandbox/core/<version>/agent-sandbox-core` with all original args

The shim never interprets project config beyond `core_version`. It's intentionally minimal so it rarely needs updating.

## The Core Binary

`agent-sandbox-core` is a self-contained Go binary that implements all real commands: `init`, `generate`, `compose`, `audit`, `gateway-url`.

It resolves assets (presets, plugins, templates, gateway source) relative to its own directory:

```
~/.agent-sandbox/core/v0.13.0/
  agent-sandbox-core        ← the binary
  gateway/                  ← gateway source for Docker build
  presets/                  ← runtime preset YAMLs
  plugins/                  ← built-in plugin definitions
  templates/                ← Dockerfile/compose/entrypoint templates
  .complete                 ← sentinel (extraction succeeded)
```

This means the core binary + its sibling directories form a complete, versioned unit. No external dependencies at runtime.

## Filesystem Layout

```
~/.agent-sandbox/
  bin/
    agent-sandbox           ← the shim (POSIX shell script)
  core/
    v0.12.0/
      agent-sandbox-core
      gateway/
      presets/
      plugins/
      templates/
      .complete
    v0.13.0/
      agent-sandbox-core
      gateway/
      presets/
      plugins/
      templates/
      .complete
```

Multiple core versions coexist. Different projects can pin different versions without conflict.

## Release Model

- **Core releases** are the primary release mechanism. Tagged `core-v*`, they produce platform tarballs containing everything the core binary needs.
- **Shim releases** are rare — only when the shim protocol changes (new env vars, different resolution logic, new self-hosted commands).
- **Legacy CLI releases** (GoReleaser) are being retired after v1.27.0.

## Local Development

For development, bypass the shim's download logic with `--core`:

```bash
# Build core locally
go build -o ./core/agent-sandbox-core ./cmd/agent-sandbox-core/

# Run with local core (shim passes --core to override resolution)
agent-sandbox --core=./core generate
```

Or invoke the core binary directly:

```bash
./cmd/agent-sandbox-core/agent-sandbox-core generate
```
