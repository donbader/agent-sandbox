# flox plugin

Installs [Nix](https://nixos.org) (single-user) and [Flox](https://flox.dev) in the agent container, allowing the agent to use `flox activate` in any directory with a `.flox/` manifest.

## Quick Start

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/flox"
```

That's it. The agent can now run `flox activate` in any project directory with a Flox manifest.

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `cache` | boolean | `true` | Persist `/nix` store across container restarts using a named Docker volume |

## How It Works

1. Installs Nix using the [Determinate Systems installer](https://github.com/DeterminateSystems/nix-installer) with `--init none` (no systemd, suitable for containers)
2. Disables Nix sandboxing (`sandbox = false`) since it conflicts with container isolation
3. Installs Flox via `nix profile install`
4. Transfers `/nix` ownership to the agent user so it can run without root

## Volume Caching

When `cache: true` (default), a named Docker volume (`<agent-name>-nix-store`) is mounted at `/nix`. This means:
- First build: Nix store is populated from the image and preserved in the volume
- Subsequent runs: volume data is reused, skipping re-download of packages
- `flox activate` in project directories will cache resolved packages across restarts

Set `cache: false` if you want a fresh `/nix` on every container restart (slower but reproducible).

## Limitations

- **Root-only install:** Nix is installed as root at build time, then `/nix` is `chown`'d to the agent user. This adds build time proportional to the store size.
- **No Nix daemon:** Single-user mode means no background garbage collection. The store grows over time if cached.
- **sandbox=false:** Nix's own build sandboxing is disabled. This is standard for container deployments but means Nix builds inside the container are less isolated.
- **Large image layer:** The Nix + Flox install adds ~1-2 GB to the Docker image. Use `cache: true` to avoid re-downloading on every rebuild.

## Example

```yaml
# agent.yaml
name: my-agent
runtime:
  preset: pi

installations:
  - plugin: "@builtin/flox"
    options:
      cache: true
```

The agent can then:
```bash
cd /workspace/my-project   # has .flox/ directory
flox activate              # activates the Flox environment
```
