# Migration Guide

Migrating from the legacy single-binary CLI to the new shim + core architecture.

## Who Needs This

If you installed `agent-sandbox` via GoReleaser (Homebrew tap, direct binary download, or `go install`), you're on the legacy CLI. The new architecture uses a lightweight shim that downloads versioned core binaries on demand.

## Steps

### 1. Install the shim

```bash
agent-sandbox upgrade
```

This downloads the shim and places it at `~/.agent-sandbox/bin/agent-sandbox`.

If `upgrade` isn't available in your current version, install fresh:

```bash
curl -fsSL https://raw.githubusercontent.com/donbader/agent-sandbox/main/scripts/install.sh | sh
```

### 2. Update your PATH

Add `~/.agent-sandbox/bin` **before** your old binary location:

```bash
export PATH="$HOME/.agent-sandbox/bin:$PATH"
```

Add this to your shell profile (`~/.bashrc`, `~/.zshrc`, `~/.profile`).

### 3. Remove the old binary

The `upgrade` command prints the path to the old binary. Remove it:

```bash
# Example — your path may differ
rm /usr/local/bin/agent-sandbox
# Or if installed via Homebrew:
brew uninstall agent-sandbox
```

### 4. Add `core_version` to your projects

Each project's `agent.yaml` should declare which core version to use:

```yaml
core_version: v0.13.0   # pin for reproducibility
```

Or use `latest` if you always want the newest:

```yaml
core_version: latest
```

Pinning is recommended for teams — it ensures everyone generates identical artifacts regardless of when they last ran the command.

### 5. Verify

```bash
agent-sandbox version
```

Should output:

```
shim:  v1.0.0
core:  v0.13.0 (from agent.yaml)
```

Test your existing project:

```bash
agent-sandbox generate
agent-sandbox compose up --build -d
```

## What Changed

| Before | After |
|--------|-------|
| Single Go binary | Shim (shell) + core (Go binary) |
| One global version | Per-project version via `core_version` |
| `agent-sandbox upgrade` replaces binary | `agent-sandbox upgrade` updates shim only |
| GoReleaser releases | Core tarballs per platform |
| Binary at `/usr/local/bin/` | Shim at `~/.agent-sandbox/bin/` |

## Rollback

If something goes wrong, you can revert:

1. Remove the shim: `rm ~/.agent-sandbox/bin/agent-sandbox`
2. Restore your old binary to PATH
3. Remove the PATH addition from your shell profile

The old binary continues to work — `core_version` in `agent.yaml` is ignored by legacy versions.
