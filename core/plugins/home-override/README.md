# home-override

Mounts a local directory from your project into the agent container as `/home/agent/`. Use this to ship dotfiles, config files, scripts, or any other home directory contents with your agent.

## Usage

```yaml
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "@fleet/home"
      volume: true
      sync_paths:
        - ".pi/agent/skills"
        - ".pi/agent/extensions"
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `home_directory` | project-path | yes | — | Directory to mount as `/home/agent/`. Must use `@fleet/` prefix (e.g. `@fleet/home`). |
| `volume` | boolean | no | `false` | Persist home across restarts via a named Docker volume. |
| `sync_paths` | array | no | — | Relative paths to keep in sync. Stale files in these paths are deleted on startup. |

## How It Works

**Build time:** Adds a `COPY` step to the Dockerfile that copies `home_directory` into `/opt/home-seed/` with correct ownership.

**Runtime (volume: false):** Bind-mounts `home_directory` directly to `/home/agent/`. Changes on the host are reflected immediately inside the container, and vice versa.

**Runtime (volume: true):** Mounts a named Docker volume at `/home/agent/`. On every startup, files from `/opt/home-seed/` (baked into the image) are copied into the volume via `cp -a`. This adds/updates files but does not remove files that were deleted from the source.

**Runtime (volume: true + sync_paths):** After the `cp -a`, for each path in `sync_paths`, any file in the volume that no longer exists in `/opt/home-seed/` is removed. Empty directories are cleaned up. This ensures managed paths (like skills and extensions) stay perfectly in sync with the repo.

Use `volume: true` when the agent writes state to its home directory that should survive container restarts (e.g., auth tokens, shell history, tool caches).

Use `sync_paths` for directories that should be fully controlled by the repo — where stale files from previous deploys should be cleaned up automatically.

## What It Contributes

- **Runtime (build):** `COPY` of home directory into `/opt/home-seed/` with ownership set to the agent user
- **Runtime (pre_entrypoint):** `cp -a` sync + optional stale file cleanup for `sync_paths`
- **Runtime (namespaced_volumes):** Named volume `home` (auto-prefixed with `{agentName}-` for fleet isolation) when `volume: true`

## Example

```
my-agent/
  agent.yaml
  home/
    .pi/
      agent/
        skills/         ← managed by sync_paths
        extensions/     ← managed by sync_paths
    .gitconfig          ← not managed, persists freely
    .bashrc             ← not managed, persists freely
```

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
      sync_paths:
        - ".pi/agent/skills"
        - ".pi/agent/extensions"
```

With this config:
- `.pi/agent/skills/` and `.pi/agent/extensions/` are fully synced — deleted files in the repo are cleaned from the volume
- Other paths (`.gitconfig`, `.bashrc`, runtime state) persist freely across restarts
