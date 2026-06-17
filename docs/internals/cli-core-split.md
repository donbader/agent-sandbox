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
   - Reads `core_version` from per-agent `agent.yaml` in the fleet (resolved via `fleet.yaml` in the working directory or `-C` target)
   - If `latest`, queries GitHub API for newest `v*` tag (cached 1h)
   - If a specific version (e.g. `v0.13.0`), uses it directly
3. Checks if that version is cached at `~/.agent-sandbox/core/<version>/`
4. If not cached, downloads the platform-appropriate tarball from GitHub Releases
5. Writes a `.complete` sentinel file after successful extraction
6. Execs into `~/.agent-sandbox/core/<version>/agent-sandbox-core` with all original args

The shim never interprets project config beyond `core_version`. It's intentionally minimal so it rarely needs updating.

### Download Timeouts and Offline Fallback (shim v1.8.0)

As of v1.31.1 (shim v1.8.0), both network calls — the GitHub API version resolution and the tarball download — use `--connect-timeout 10 --max-time 60`. This prevents the shim from hanging indefinitely on flaky or unreachable networks.

If either call fails or times out, the shim falls back gracefully:

1. **`resolve_latest()` failure** — if the GitHub API call to resolve the latest tag fails, the shim calls `find_local_latest()` instead of aborting.
2. **Tarball download failure** — if the download of a specific version fails, the shim also calls `find_local_latest()`.
3. **`find_local_latest()`** scans `~/.agent-sandbox/core/*/` for the newest directory containing a `.complete` sentinel file (i.e., a previously-successful extraction).
4. If a local version is found, the shim emits a warning to stderr (e.g. `[warn] Network unavailable, using cached v0.13.0`) and proceeds with that version.
5. If no local version exists (first-ever run with no cache), the shim exits with a clear error explaining that network access is required for initial setup.

This means `agent-sandbox` works offline after the first successful core download — useful for air-gapped environments, CI with intermittent connectivity, or GitHub outages.

## The Core Binary

`agent-sandbox-core` is a self-contained Go binary that implements all real commands: `init`, `generate`, `compose`, `audit`.

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

- **Core releases** are the primary release mechanism. Tagged `v*`, they produce platform tarballs containing everything the core binary needs.
- **Shim releases** are rare — only when the shim protocol changes (new env vars, different resolution logic, new self-hosted commands).
- **Legacy CLI releases** (GoReleaser) are being retired after v1.27.0.

## Local Development

Use `--dev` to build and run from source:

```bash
agent-sandbox --dev -C examples/local-coding generate
# [dev] Building from source...
# Generated .build/ in .../examples/local-coding
```

The binary is placed at `./core/agent-sandbox-core` so it resolves sibling assets (plugins, presets, templates) from the `core/` directory.

Requires `go` or `flox` on PATH. Must be run from the source repo root.
