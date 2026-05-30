# Home Version Control Example

Demonstrates the `home-version-control` feature plugin:

- **Custom packages**: Install ripgrep and fd-find into the container
- **Entrypoint hooks**: Run a dotfiles sync script on every container start
- **Persistent home**: Named volume preserves `/home/agent` across restarts
- **Home override**: Copy `.gitconfig` into the agent's home directory

## Usage

```bash
cd examples/home-vc
agent-sandbox generate
agent-sandbox compose up --build
```

## What happens

1. `generate` reads `agent.yaml` and produces `.build/` artifacts
2. The Dockerfile installs codex + ripgrep + fd-find
3. On container start, the entrypoint:
   - Copies `home/.gitconfig` → `/home/agent/.gitconfig`
   - Runs `scripts/sync-dotfiles.sh` (syncs dotfiles from a git repo if `DOTFILES_REPO` is set)
   - Starts `sleep infinity` (waiting for bridge in Phase 4)
4. The named volume `agent-home` persists home directory across restarts

## Configuration

| Field | Description |
|-------|-------------|
| `commands` | Additional apt packages to install |
| `entrypoint_hooks` | Scripts to run on every container start |
| `runtime_volumes` | Named volumes for persistence |
| `home_override` | Directory whose contents are copied to `/home/agent` on start |
