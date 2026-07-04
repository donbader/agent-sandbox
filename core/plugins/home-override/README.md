# home-override

Mounts a local directory from your project into the agent container as `/home/agent/`. Use this to ship dotfiles, config files, scripts, or any other home directory contents with your agent.

## Usage

```yaml
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "@fleet/home"
      volume: true
      clean_stale: true
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `home_directory` | project-path | yes | — | Directory to mount as `/home/agent/`. Must use `@fleet/` prefix (e.g. `@fleet/home`). |
| `volume` | boolean | no | `false` | Persist home across restarts via a named Docker volume. |
| `clean_stale` | boolean | no | `false` | Remove stale files from volume on startup. Auto-discovers managed dirs from source. |

## How It Works

**Build time:** Adds a `COPY` step to the Dockerfile that copies `home_directory` into `/opt/home-seed/` with correct ownership.

**Runtime (volume: false):** Bind-mounts `home_directory` directly to `/home/agent/`. Changes on the host are reflected immediately inside the container, and vice versa.

**Runtime (volume: true):** Mounts a named Docker volume at `/home/agent/`. On every startup, files from `/opt/home-seed/` (baked into the image) are copied into the volume via `cp -a`. This adds/updates files but does not remove files that were deleted from the source.

**Runtime (volume: true + clean_stale):** After the `cp -a`, scans all files in the volume. For each file, if its parent directory exists in the source but the file itself doesn't — it's stale and gets removed. Empty directories are cleaned up.

This auto-discovers which paths to manage: directories that exist in your source are kept in sync, directories created only at runtime (like `.traces/`, `.logs/`) are never touched because they don't exist in the source.

Use `volume: true` when the agent writes state to its home directory that should survive container restarts (e.g., auth tokens, shell history, tool caches).

Use `clean_stale: true` to automatically remove files that were deleted from the repo. No need to list paths — it auto-discovers based on what directories exist in the source.

## What It Contributes

- **Runtime (build):** `COPY` of home directory into `/opt/home-seed/` with ownership set to the agent user
- **Runtime (pre_entrypoint):** `cp -a` sync + optional stale file cleanup when `clean_stale: true`
- **Runtime (namespaced_volumes):** Named volume `home` (auto-prefixed with `{agentName}-` for fleet isolation) when `volume: true`

## Example

```
my-agent/
  agent.yaml
  home/
    .pi/
      agent/
        skills/         ← managed (exists in source)
        extensions/     ← managed (exists in source)
    .gitconfig          ← managed (exists in source)
    .bashrc             ← managed (exists in source)
```

At runtime, the agent may also create:
- `.traces/` ← NOT managed (not in source, never touched)
- `.logs/` ← NOT managed
- `.telegram-adapter/` ← NOT managed

```yaml
# agent.yaml
name: coder
core_version: latest
runtime:
  image: "@builtin/pi"
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "@fleet/home"
      volume: true
      clean_stale: true
```

With `clean_stale: true`: if you delete a skill from `home/.pi/agent/skills/` in your repo, it gets removed from the volume on next restart. Runtime-created directories are safe.
