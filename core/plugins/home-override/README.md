# home-override Plugin

Mounts a local directory from your project into the agent container as the agent's home directory (`/home/agent/`). Use this to ship dotfiles, config files, scripts, or any other home directory contents with your agent.

## Configuration

```yaml
features:
  - plugin: home-override
    home_directory: ./home          # required: path to local dir, relative to project root
    volume: false                   # optional: persist home across restarts (default: false)
```

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| `home_directory` | string | yes | Local directory to mount as `/home/agent/`. Relative to project root. |
| `volume` | boolean | no | If `true`, contents are copied into a named Docker volume (`agent-home`) on first run and persisted across restarts. If `false` (default), the directory is bind-mounted directly. |

## How It Works

**Build time:** The plugin adds a `COPY` step to the Dockerfile that copies `home_directory` into `/home/agent/` and sets correct ownership.

**Runtime:**
- `volume: false` — bind-mounts `home_directory` directly to `/home/agent/`. Changes on the host are reflected immediately; changes in the container are visible on the host.
- `volume: true` — mounts a named Docker volume at `/home/agent/`. The volume is seeded from the image contents (which include the copied `home_directory`) on first run. Subsequent restarts use the persisted volume data.

Use `volume: true` when the agent writes state to its home directory that should survive container restarts (e.g., auth tokens, shell history, tool caches).
