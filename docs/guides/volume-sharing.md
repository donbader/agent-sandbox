# Volume Sharing in Docker-out-of-Docker (DooD)

When the agent spawns containers via the Docker proxy, those containers are **sibling containers** — managed by the same host Docker daemon. This has an important implication for file sharing:

> **Bind mount paths are resolved against the host filesystem, not the agent container's filesystem.**

This means `docker run -v /home/agent/workspace:/app ...` does NOT mount the agent's workspace into the spawned container. It mounts whatever `/home/agent/workspace` is on the **host** (which may not exist).

## How File Sharing Works

The Docker proxy automatically translates bind mount requests into **named volume mounts** with subpath isolation. This works for any path that lives on a named Docker volume inside the agent container.

```
┌─ Agent Container ────────────────────────────────────┐
│                                                       │
│  Named volumes:                                       │
│    agent-home  → /home/agent     ← shareable ✓       │
│    agent-nix   → /nix            ← shareable ✓       │
│                                                       │
│  Container layer:                                     │
│    /usr, /etc, /var              ← NOT shareable ✗    │
│                                                       │
└───────────────────────────────────────────────────────┘
```

When the agent runs:
```bash
docker run -v /home/agent/workspace/project/src:/app myimage
```

The proxy:
1. Recognizes `/home/agent/workspace/project/src` falls under volume `agent-home` (mounted at `/home/agent`)
2. Calculates subpath: `workspace/project/src`
3. Translates to a volume-subpath mount: `agent-home:/app` with subpath `workspace/project/src`

The spawned container sees the agent's files at `/app` — live, read-write, no copies.

## What's Shareable

| Path | Shareable? | Why |
|------|-----------|-----|
| `/home/agent/*` | ✅ Yes | On named volume (home-override plugin) |
| `/nix/*` | ✅ Yes | On named volume (flox plugin) |
| Any path on a named volume | ✅ Yes | Proxy auto-detects |
| `/tmp/*` | ❌ No* | Container writable layer |
| `/usr/*`, `/etc/*` | ❌ No | Image layers |

*To make `/tmp` shareable, add a named volume for it in your fleet configuration.

## Configuration

**No manual configuration needed** for paths already on named volumes. The proxy discovers the agent container's volume mounts automatically at startup.

To make additional paths shareable, mount them as named volumes in your agent configuration:

```yaml
# fleet.yaml — adding a temp volume for sharing
agents:
  - myagent

shared:
  # ...
```

Then in your agent's plugin configuration, ensure the relevant plugins create named volumes. The `@builtin/home-override` plugin with `volume: true` already handles `/home/agent`.

## Docker Compose Files

Compose files with relative path bind mounts work transparently:

```yaml
# docker-compose.yml (run by agent)
services:
  app:
    image: node:20
    volumes:
      - ./src:/app           # ✅ Works — translated to volume mount
      - ./config.yaml:/etc/app/config.yaml:ro  # ✅ Works
    working_dir: /app
    command: npm test
```

Docker resolves `./src` to an absolute path inside the agent container (e.g., `/home/agent/workspace/project/src`). The proxy intercepts this and translates it to a volume-subpath mount.

## Limitations

- **Docker Engine 26+ required** — volume-subpath was introduced in Engine 26.0 (March 2024)
- **Only named volumes are shareable** — files in the container's overlay filesystem (image layers + writable layer) cannot be mounted into sibling containers
- **Live sharing** — changes are immediately visible in both directions (agent and spawned container)
- **Paths outside any volume are blocked** — the proxy rejects host bind mounts that don't map to a known volume

## If You Need to Share Non-Volume Files

For one-off files not on a named volume, use `docker cp`:

```bash
# Copy a file into a running container
docker cp /etc/myconfig.yaml mycontainer:/app/config.yaml

# Copy from a container
docker cp mycontainer:/output/result.json ./result.json
```

## Requirements

- `@builtin/agent-docker` plugin with `allow_compose: true`
- Docker Engine 26.0+ on the host
- Paths to share must be on named Docker volumes
