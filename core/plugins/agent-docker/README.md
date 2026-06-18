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
│  │       └─────── sandbox network ──→ gateway ──→ internet    ││
│  └────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

Containers are dual-attached:
- **Compose network** — service discovery between compose services (by name)
- **Sandbox network** — routing through the gateway to the internet

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
| `POST /networks/create` | Forces `Internal: true`, namespaces name, adds sandbox label |
| `DELETE /networks/{id}` | Only sandbox-owned networks |
| `GET /networks` | Filtered to sandbox-owned only |
| `POST /networks/{id}/connect` | Allowed for service wiring |
| `POST /volumes/create` | Allowed for inter-service data |
| `GET /volumes` | Allowed |
| `DELETE /volumes/{id}` | Allowed |

### Security invariants still enforced

All other security controls remain active in compose mode. Additionally:
- All created networks are forced to `Internal: true` — cannot bypass the gateway
- Network names are namespaced with the sandbox ID
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

### Limitations

The buildx `docker-container` driver creates a **privileged** buildkit container, which the proxy blocks (privileged mode is never allowed). This means:

- `docker buildx build` with the default driver won't work
- Dockerfiles using `RUN --mount=type=cache` require BuildKit and won't work with the legacy builder
- Use `DOCKER_BUILDKIT=0 docker build` for builds through the proxy

Future work: support rootless buildkit (`--oci-worker-no-process-sandbox`) to enable full BuildKit without privileged mode.

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

## Known Limitations

- **Interactive `docker run` (without `-d`)** hangs due to stream-close timing on the hijacked attach connection. Use `docker run -d` + `docker logs` instead.
- **Image pulls** go through the Docker daemon's registries. The proxy validates image names but doesn't control where they're fetched from.
- **Volumes require compose mode** — `/volumes/*` is blocked by default. Enable `allow_compose: true` if services need shared data.
- **BuildKit requires legacy builder** — The buildx `docker-container` driver creates a privileged buildkit container, which is blocked by policy. Use `DOCKER_BUILDKIT=0 docker build` (legacy builder) instead. Note: Dockerfiles using `--mount=type=cache` won't work with the legacy builder.
- **Locally-built images** must match an `allowed_images` pattern to be run. Tag your built images with a name matching your allowlist (e.g. `docker build -t node:20-myapp .`).
- **Image tagging** requires `allow_build: true` (the `POST /images/{name}/tag` endpoint is gated behind build mode).

## Prerequisites

- Docker CLI installed in the agent container
- Host Docker running with socket at `/var/run/docker.sock`
