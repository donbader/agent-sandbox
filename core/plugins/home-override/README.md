# home-override

Mounts a local directory from your project into the agent container as `/home/agent/`. Use this to ship dotfiles, config files, scripts, or any other home directory contents with your agent.

## Usage

```yaml
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "@fleet/home"
      volume: true
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `home_directory` | project-path | yes | — | Directory to mount as `/home/agent/`. Must use `@fleet/` prefix (e.g. `@fleet/home`). |
| `volume` | boolean | no | `false` | If `true`, persist home across restarts via a named Docker volume. |

## How It Works

**Build time:** Adds a `COPY` step to the Dockerfile that copies `home_directory` into `/home/agent/` with correct ownership.

**Runtime (volume: false):** Bind-mounts `home_directory` directly to `/home/agent/`. Changes on the host are reflected immediately inside the container, and vice versa.

**Runtime (volume: true):** Mounts a named Docker volume at `/home/agent/`. The volume is seeded from the image contents (which include the copied directory) on first run. Subsequent restarts use the persisted volume data.

Use `volume: true` when the agent writes state to its home directory that should survive container restarts (e.g., auth tokens, shell history, tool caches).

## What It Contributes

- **Runtime (build):** `COPY` of home directory into `/home/agent/` with ownership set to the agent user
- **Runtime (volumes):** Named volume `<agent-name>-home` when `volume: true`

## Example

```
my-agent/
  agent.yaml
  home/
    .codex/
      config.toml       ← provider config
      models.json       ← model catalog
    .gitconfig          ← git identity
    .bashrc             ← shell customization
```

```yaml
# agent.yaml
name: coder
core_version: latest
runtime:
  image: "@builtin/codex"
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "@fleet/home"
      volume: true
```
