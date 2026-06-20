# @builtin/agent-docker

Policy-enforced Docker access for sandbox agents. Lets agents spin up containers for debugging, testing, and running services — all through a security proxy that enforces image allowlists, resource limits, network isolation, and namespace separation.

## Quick Start

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "alpine:*"
        - "node:20-*"
        - "postgres:16-*"
        - "redis:7-*"
      max_containers: 5
      memory: "2g"
      cpus: "2"
      pids: 256
```

The agent gets `DOCKER_HOST` pointing at the proxy. Standard Docker CLI works out of the box:

```bash
docker run -d --name mydb postgres:16-alpine
docker exec mydb psql -U postgres -c "CREATE DATABASE myapp;"
docker ps
docker logs mydb
docker stop mydb
```

## Architecture

```
Agent Container              Docker Proxy (sidecar)         Docker Daemon
+--------------+            +----------------------+       +-------------+
| docker CLI   |--- 2375 -->| 1. policy check      |------>| containers  |
| DOCKER_HOST= |            | 2. mutate request    |       |             |
| tcp://proxy  |            | 3. namespace + filter |       |             |
+--------------+            +----------------------+       +-------------+
                                     |
                              /var/run/docker.sock
```

Every request passes through the proxy, which:
1. Checks the endpoint against an allowlist (rejects blocked APIs with 403)
2. Validates container create requests against security policy
3. Mutates requests to force network, resource limits, and labels
4. Namespaces container names to prevent cross-sandbox collisions
5. Filters list responses so agents only see their own containers
6. Cleans up all spawned containers and networks on shutdown

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `allowed_images` | string[] | yes | — | Glob patterns for permitted images |
| `max_containers` | number | no | 5 | Max concurrent containers |
| `memory` | string | no | "2g" | Memory limit per container |
| `cpus` | string | no | "2" | CPU limit per container |
| `pids` | number | no | 256 | PID limit per container |
| `allow_compose` | boolean | no | false | Enable `docker compose` support (unlocks network/volume APIs) |
| `allow_build` | boolean | no | false | Enable `docker build` support (unlocks build endpoints + auto-allows buildkit image) |
| `allowed_capabilities` | string[] | no | [] | Linux capabilities permitted on spawned containers (e.g. NET_ADMIN). Empty = all blocked. |

---

## Security

This plugin follows a defense-in-depth approach. Controls layer together so that if one is bypassed, others still hold.

### Image Allowlist

Without an allowlist, an agent could pull any image — including ones with pre-installed attack tools (network scanners, exploit kits, crypto miners). Even with network isolation and resource limits, a malicious image could attempt local exploits like kernel vulnerabilities or container escapes. The allowlist restricts *what software* runs inside those constraints.

Images are matched using glob patterns. The proxy normalizes Docker registry prefixes (`docker.io/library/`) so short names work naturally:

```yaml
allowed_images:
  - "alpine:*"         # matches alpine:latest, alpine:3.20
  - "node:20-*"        # matches node:20-slim, node:20-alpine
  - "postgres:16-*"    # matches postgres:16-alpine, postgres:16-bookworm
  - "myorg/myimage:*"  # custom registry images
```

Images not matching any pattern are rejected with 403.

### Request Validation

Every `docker create` request is validated. The following are **always blocked**:

| Field | Reason |
|-------|--------|
| `Privileged: true` | Prevents container escape |
| `NetworkMode: host` | Prevents host network access |
| `CapAdd` (any) | Prevents privilege escalation |
| `PidMode: host` | Prevents host process visibility |
| `IpcMode: host` | Prevents host IPC access |
| Host bind mounts | Prevents host filesystem access |

The following are **always forced** (regardless of what the agent requests):

| Field | Forced Value |
|-------|-------------|
| Network | Sandbox internal network (+ compose network in compose mode) |
| Memory | Configured limit |
| CPU | Configured limit |
| PIDs | Configured limit |
| Labels | `agent-sandbox.agent`, `agent-sandbox.sandbox` |
| RestartPolicy | `no` (prevents zombie containers) |

### Resource Limits

Resource limits prevent denial-of-service against the host and other sandboxes:

- **Memory** — Without a cap, a single container could exhaust host RAM and trigger the OOM killer against unrelated processes.
- **CPU** — Prevents a runaway process from starving other containers of compute time.
- **PIDs** — Prevents fork bombs. A limit of 256 is generous for normal workloads but stops exponential process spawning dead.
- **max_containers** — Prevents resource exhaustion by limiting how many containers a single agent can run concurrently.

### Blocked API Endpoints

These Docker API categories are blocked entirely (returns 403):

| Endpoint | Reason |
|----------|--------|
| `/networks/*` | Bypasses enforced network isolation. Unlocked with `allow_compose: true` (forced `Internal: true`). |
| `/volumes/*` | Host filesystem persistence, cross-sandbox data leakage. Unlocked with `allow_compose: true`. |
| `/swarm/*` | Cluster operations affecting other hosts. |
| `/secrets/*` | Exposes Docker secrets on the host. |
| `/configs/*` | Exposes Docker configs on the host. |
| `/system/*` | Leaks host info (kernel, OS, memory) useful for targeting exploits. |

### Namespacing

All spawned containers are namespaced to prevent collisions:

- Names are prefixed with `<sandbox-id>-` (e.g. `local-coding-coder-mydb`)
- `docker ps` only shows containers belonging to this sandbox
- Commands using user-provided names work transparently (the proxy translates)

### Network Isolation

Spawned containers join the sandbox's internal network:
- They can reach the agent and each other by container name
- They cannot reach the internet (network is `internal: true`)
- All internet-bound traffic routes through the gateway

---

## Compose Mode

When `allow_compose: true`, the agent can run `docker compose` to orchestrate multi-service workloads:

- Integration testing (app + database + cache)
- Full development environments
- Nested agent-sandbox stacks for self-debugging

### How it works

```
┌─ Sandbox ─────────────────────────────────────────────────────┐
│  Agent Container                                               │
│  └─ docker compose up -d                                       │
│                                                                 │
│  ┌─ Compose Project ──────────────────────────────────────────┐│
│  │  service-a ←── compose network ──→ service-b               ││
│  │       │                                                     ││
│  │       └─ iptables DNAT (init wrapper) → gateway → internet ││
│  └────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

The proxy injects a **transparent proxy init wrapper** into each spawned container's entrypoint:

1. Adds `NET_ADMIN` capability (for iptables)
2. Inlines a shell script that resolves the gateway IP, sets up iptables DNAT, and configures DNS
3. Resolves the image's default CMD/Entrypoint to preserve the original command
4. Execs the original container command after proxy setup

Containers keep their compose networks for service discovery. Internet-bound traffic is transparently routed through the gateway via iptables — no dual-network attachment needed.

For **standalone `docker run`** (no compose networks), the container is also placed on the sandbox network directly.

### Configuration

```yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "alpine:*"
        - "node:20-*"
        - "postgres:16-*"
        - "redis:7-*"
      max_containers: 10
      allow_compose: true
```

### Unlocked endpoints

| Endpoint | Constraint |
|----------|-----------|
| `POST /networks/create` | Forces `Internal: true`, adds sandbox label |
| `DELETE /networks/{id}` | Only sandbox-owned networks |
| `GET /networks` | Filtered to sandbox-owned only |
| `POST /networks/{id}/connect` | Allowed for service wiring |
| `POST /volumes/create` | Allowed for inter-service data |
| `GET /volumes` | Allowed |
| `DELETE /volumes/{id}` | Allowed |

### Security invariants still enforced

All other security controls remain active in compose mode. Additionally:
- All created networks are forced to `Internal: true` — cannot bypass the gateway
- All spawned containers get transparent proxy (iptables DNAT to gateway)
- `NET_ADMIN` is auto-injected by the proxy (not user-configurable, not counted against `allowed_capabilities`)
- Cleanup removes spawned containers AND networks on shutdown

---

## Build Mode

When `allow_build: true`, the agent can build Docker images using `docker build`:

```yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "node:20-*"
      allow_build: true
```

### How it works

Build mode unlocks these endpoints:

| Endpoint | Purpose |
|----------|--------|
| `GET /info` | Capability detection (buildx uses this) |
| `POST /build` | Legacy Docker build (sends build context to daemon) |
| `GET /images/{name}/get` | Image export |
| `POST /images/load` | Image import |
| `POST /images/{name}/tag` | Image tagging |

Additionally, `moby/buildkit:*` images are auto-allowed when build mode is active (needed by the buildx `docker-container` driver).

### Usage

```bash
# Build using legacy builder (recommended for proxy environments)
DOCKER_BUILDKIT=0 docker build -t node:20-myapp .

# Tag the image to match your allowlist pattern
# (so you can run it through the proxy)
docker build -t node:20-myapp -f Dockerfile .
docker run -d --name myapp node:20-myapp
```

### BuildKit Sidecar

When `allow_build: true`, the generator deploys an isolated BuildKit sidecar (`agent-docker-buildkit`) alongside the agent. The agent connects to it via the `remote` buildx driver — no privileged containers needed inside the agent.

```
Agent Container              BuildKit Sidecar
+--------------+            +------------------------+
| docker CLI   |            | buildkitd              |
| buildx       |--- 8372 -->| - runc worker          |
| (remote drv) |            | - native snapshotter   |
+--------------+            +------------------------+
```

At startup, the agent's pre-entrypoint auto-configures the remote driver:
```bash
docker buildx create --name buildkit --driver remote \
  --driver-opt url=tcp://<agent>-agent-docker-buildkit:8372 --use
```

This enables full BuildKit features (`RUN --mount=type=cache`, multi-stage parallel builds) without giving the agent container any elevated capabilities.

### BuildKit Sidecar Security

The buildkit sidecar runs with `CAP_SYS_ADMIN` and `security_opt: apparmor=unconfined`. This is required because:

1. **runc needs mount()** — every `RUN` instruction creates a short-lived container. Setting up its rootfs requires bind mounts, which need `CAP_SYS_ADMIN`.
2. **AppArmor blocks mount** — even with SYS_ADMIN, the default AppArmor profile denies mount syscalls. `apparmor=unconfined` lifts this.

**Why this is acceptable:**

- The **agent container does NOT have SYS_ADMIN** — only the infrastructure sidecar does
- The agent can only submit build requests via TCP (port 8372) — it cannot exec into or control the sidecar
- The sidecar has no access to host volumes, Docker socket, or other agents' data
- Network traffic from the sidecar is routed through the gateway (credentials never leak)

**Threat model:** a compromised agent could submit malicious Dockerfiles to the sidecar. The sidecar builds them in isolation — it cannot escape to the host because it lacks the Docker socket and its network is sandboxed. The worst case is resource abuse (CPU/memory during builds), which is bounded by container resource limits.

**Future:** rootless buildkit (via rootlesskit + fuse-overlayfs) would eliminate the need for SYS_ADMIN entirely, but requires rewriting the sidecar's network routing (iptables → HTTP proxy).

---

## Usage Patterns

### Database for testing

```bash
docker run -d --name testdb postgres:16-alpine
docker exec testdb psql -U postgres -c "CREATE DATABASE myapp;"
```

### One-off script

```bash
docker run -d --name build node:20-slim sh -c "npm install && npm test"
docker logs -f build
```

### Multi-service with compose

```bash
docker compose -f docker-compose.test.yml up -d
docker compose -f docker-compose.test.yml logs -f
docker compose -f docker-compose.test.yml down
```

### Nested sandbox for self-debugging

```bash
agent-sandbox generate
docker compose -f .build/docker-compose.yml up --build -d
docker compose -f .build/docker-compose.yml down
```

---

## Lifecycle

When the sandbox shuts down, the proxy receives SIGTERM and:
1. Gracefully stops accepting new requests
2. Force-removes all labeled containers in parallel
3. Removes all labeled networks

No orphaned resources are left behind.

## Volume Sharing (DooD)

In Docker-out-of-Docker mode, spawned containers are **sibling containers** — managed by the same host daemon. Bind mount paths resolve against the **host filesystem**, not the agent container's.

The proxy solves this by automatically translating bind mounts into **volume-subpath mounts**:

```
Agent runs:  docker run -v /home/agent/workspace/src:/app myimage
Proxy sees:  Binds=["/home/agent/workspace/src:/app"]
Translates:  volume "agent-home" mounted at /app with subpath "workspace/src"
```

### How it works

1. At startup, the proxy inspects the agent container's named volume mounts
2. On container create, bind mounts matching a known volume are translated to volume-subpath mounts
3. Paths not under any known volume are blocked with a clear error

### What's shareable

| Path | Shareable? | Why |
|------|-----------|-----|
| `/home/agent/*` | ✅ Yes | On named volume (home-override plugin with `volume: true`) |
| `/nix/*` | ✅ Yes | On named volume (flox plugin with `cache: true`) |
| Any path on a named volume | ✅ Yes | Auto-discovered |
| `/tmp/*`, `/usr/*`, `/etc/*` | ❌ No | Container image/writable layer — not on a volume |

### Compose files work transparently

```yaml
# docker-compose.yml (run by agent inside /home/agent/workspace/project/)
services:
  app:
    image: node:20
    volumes:
      - ./src:/app           # ✅ Translated to volume mount
      - ./config.yaml:/etc/app/config.yaml:ro  # ✅ Works
    command: npm test
```

### Making additional paths shareable

To share paths not already on a volume, mount them as named volumes in your agent's compose configuration. The `@builtin/home-override` plugin with `volume: true` already handles `/home/agent`.

### Requirements

- **Docker Engine 26.0+** (API 1.45+) — volume-subpath was introduced in March 2024
- Agent container paths must be on named Docker volumes
- The proxy returns a clear error if Docker is too old or the path isn't on a volume

### If you need to share non-volume files

For one-off files not on a named volume, use `docker cp`:

```bash
docker cp /etc/myconfig.yaml mycontainer:/app/config.yaml
```

---

## Known Limitations

- **Interactive `docker run` (without `-d`)** hangs due to stream-close timing on the hijacked attach connection. Use `docker run -d` + `docker logs` instead.
- **Image pulls** go through the Docker daemon's registries. The proxy validates image names but doesn't control where they're fetched from.
- **Volumes require compose mode** — `/volumes/*` is blocked by default. Enable `allow_compose: true` if services need shared data.
- **BuildKit requires buildx reset** — If buildx defaults to the docker-container driver (privileged), the plugin's pre-entrypoint resets it on container start. If you see "privileged mode is not allowed" during build, restart the container or run `rm -rf ~/.docker/buildx/instances`.
- **Locally-built images** are automatically tracked and allowed when `allow_build: true`. Images built via `docker build` or tagged via `docker tag` through the proxy are auto-allowed for `docker run` without needing to match `allowed_images` patterns.
- **Image tagging** requires `allow_build: true` (the `POST /images/{name}/tag` endpoint is gated behind build mode).

---

## Security: Capabilities

By default, ALL `cap_add` requests are blocked. The `allowed_capabilities` option relaxes this for specific capabilities:

```yaml
allowed_capabilities:
  - NET_ADMIN
  - NET_BIND_SERVICE
  - SETUID
  - SETGID
  - CHOWN
  - FOWNER
  - DAC_OVERRIDE
```

### Risk Assessment

| Capability | Risk | Use Case |
|-----------|------|----------|
| `NET_ADMIN` | Medium | iptables routing, network config. Scoped to container's network namespace. |
| `NET_BIND_SERVICE` | Low | Bind to ports < 1024. |
| `SETUID` / `SETGID` | Medium | Change user/group IDs. Needed for multi-user containers. |
| `DAC_OVERRIDE` | Medium-High | Bypass file permission checks within the container. |
| `CHOWN` / `FOWNER` | Low-Medium | Change file ownership. Needed for setup scripts. |
| `SYS_ADMIN` | **High** | Broad privileges, potential container escape. Avoid unless absolutely necessary. |
| `SYS_PTRACE` | Medium-High | Debug processes, read memory. Useful for debugging but information leak risk. |

### Recommendations

- **Never allow** `SYS_ADMIN` unless you fully understand the implications (near-equivalent to privileged mode).
- **Nested sandbox** (running agent-sandbox compose inside a sandbox) requires: `NET_ADMIN`, `NET_BIND_SERVICE`, `SETUID`, `SETGID`, `DAC_OVERRIDE`, `CHOWN`, `FOWNER`.
- Keep the list as short as possible. Only add capabilities your workload actually needs.
- `privileged: true` remains permanently blocked regardless of allowed_capabilities.

## Prerequisites

- Docker CLI installed in the agent container
- Host Docker running with socket at `/var/run/docker.sock`, or `DOCKER_HOST` set to a reachable Docker API

---

## Docker Socket Interception (Recursive DinD)

When a container tries to bind mount `/var/run/docker.sock`, the proxy intercepts the request and transparently redirects Docker access back to itself:

1. The socket bind mount is removed
2. `DOCKER_HOST=tcp://<proxy>:2375` is injected into the container's environment

This enables recursive Docker-in-Docker — containers that need Docker get routed through the same policy proxy regardless of nesting depth.

### How it works

```
Agent → docker run -v /var/run/docker.sock:/var/run/docker.sock dind-image
         ↓ (proxy intercepts)
Container gets: DOCKER_HOST=tcp://proxy:2375 (no socket mount)
         ↓
Container runs: docker run alpine echo hello
         ↓ (goes through same proxy)
Proxy: policy check → create container → done
```

All levels share the same proxy and buildkit instance. No nested proxy chains, no extra infrastructure.

### Detected socket paths

The proxy recognizes all common Docker socket mount patterns:
- `/var/run/docker.sock`
- `/run/docker.sock`
- Any path ending in `/docker.sock`

Read-only mounts (`:ro`) are also intercepted.

### Works with any Docker-compatible runtime

Since `DOCKER_HOST` is the standard mechanism, this works transparently with:
- Docker CLI
- Docker Compose
- BuildKit / Buildx
- Podman (Docker compatibility mode)
- Any tool that respects `DOCKER_HOST`

### Security

All policy controls remain active for containers spawned at any depth:
- Image allowlist applies
- Resource limits enforced
- Network isolation maintained
- Namespacing active
- Container count limits enforced

---

## Upstream Connection

The proxy itself respects the `DOCKER_HOST` environment variable for its upstream Docker connection:

| `DOCKER_HOST` value | Behavior |
|---------------------|----------|
| (not set) | Connects to `/var/run/docker.sock` (default) |
| `tcp://host:port` | Connects via TCP |

This enables running the proxy in environments where a Unix socket isn't available (e.g. inside another sandbox). No special configuration needed — the standard Docker convention is sufficient.
