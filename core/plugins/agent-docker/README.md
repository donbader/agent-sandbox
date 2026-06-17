# @builtin/agent-docker

Gives agents the ability to spin up Docker containers for debugging, development, and running services. All container operations are policy-enforced through a proxy sidecar.

## Quick Start

```yaml
# In your agent.yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "alpine:*"
        - "node:20-*"
        - "python:3.12-*"
        - "postgres:16-*"
        - "redis:7-*"
      max_containers: 5
      memory: "2g"
      cpus: "2"
      pids: 256
```

The agent container gets a `DOCKER_HOST` env var pointing at the proxy. Standard Docker CLI and any Docker SDK work out of the box:

```bash
# Inside the agent container
docker run -d --name mydb postgres:16-alpine
docker ps
docker logs mydb
docker stop mydb
```

## How It Works

```
Agent Container              Docker Proxy (sidecar)         Docker Daemon
+--------------+            +----------------------+       +-------------+
| docker CLI   |--- 2375 -->| policy check         |------>| creates     |
| DOCKER_HOST= |            | mutate (labels,      |       | containers  |
| tcp://proxy  |            |   network, limits)   |       |             |
+--------------+            | namespace filtering  |       +-------------+
                            +----------------------+
                                     |
                              /var/run/docker.sock
```

The proxy:
1. Validates every Docker API request against security policy
2. Mutates container create requests (forces network, labels, resource limits)
3. Namespaces container names to prevent collisions across sandboxes
4. Filters list/inspect responses so agents only see their own containers
5. Cleans up all spawned containers on shutdown

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `allowed_images` | string[] | yes | — | Glob patterns for allowed images (see [Image Allowlist](#image-allowlist)) |
| `max_containers` | number | no | 5 | Max concurrent spawned containers |
| `memory` | string | no | "2g" | Memory limit per container (e.g. "512m", "4g") |
| `cpus` | string | no | "2" | CPU limit per container (e.g. "0.5", "4") |
| `pids` | number | no | 256 | PID limit per container (see [Resource Limits](#resource-limits)) |
| `allow_compose` | boolean | no | false | Enable docker compose operations (see [Nested Sandboxes](#nested-sandboxes)) |

## Image Allowlist

**Why this exists:** Without an allowlist, an agent could pull any image — including ones with pre-installed attack tools (network scanners, exploit kits, crypto miners). Even though spawned containers are network-isolated and resource-limited, a malicious image could still attempt local exploits like kernel vulnerabilities or container escape techniques. The allowlist reduces attack surface by restricting *what software* runs inside those constraints.

Images are matched using glob patterns. The proxy normalizes Docker registry prefixes (`docker.io/library/`) so short names work naturally:

```yaml
allowed_images:
  - "alpine:*"         # matches alpine:latest, alpine:3.20, etc.
  - "node:20-*"        # matches node:20-slim, node:20-alpine
  - "postgres:16-*"    # matches postgres:16-alpine, postgres:16-bookworm
  - "myorg/myimage:*"  # matches custom registry images
```

Images not matching any pattern are rejected with 403.

## Security Policy

This plugin follows a defense-in-depth approach. No single control is the entire security story — they layer together so that if one is bypassed, others still hold.

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
| Network | Sandbox internal network |
| Memory | Configured limit |
| CPU | Configured limit |
| PIDs | Configured limit |
| Labels | `agent-sandbox.agent`, `agent-sandbox.sandbox` |
| RestartPolicy | `no` (prevents zombie containers) |

### Resource Limits

Resource limits prevent denial-of-service against the host and other sandboxes:

- **Memory** — Without a cap, a single container could exhaust host RAM and trigger the OOM killer against unrelated processes (including other sandboxes and the agent itself).
- **CPU** — Prevents a runaway process from starving other containers of compute time.
- **PIDs** — Prevents fork bombs. Without this, a container can spawn unlimited processes until the host's global PID limit is exhausted, which is a denial-of-service against *everything* on the host. A limit of 256 is generous for normal workloads but stops exponential process spawning dead.
- **max_containers** — Prevents resource exhaustion by limiting how many containers a single agent can run concurrently.

## Blocked API Endpoints

Certain Docker API endpoints are blocked entirely because they provide capabilities that break the sandbox isolation model:

| Endpoint | Reason |
|----------|--------|
| `/volumes/*` | Volumes persist on the host filesystem. An agent could write to host paths, persist data across sandbox restarts (violating ephemeral-by-default), or exfiltrate data between sandboxes via shared volume names. |
| `/networks/*` | Allows creating/joining arbitrary networks, bypassing the enforced internal network isolation. |
| `/swarm/*` | Cluster operations could affect other hosts. |
| `/secrets/*` | Exposes Docker secrets stored on the host. |
| `/configs/*` | Exposes Docker configs stored on the host. |
| `/system/*` | Leaks host system information (kernel version, OS, total memory) useful for targeting exploits. |

Any endpoint not explicitly allowed returns 403.

## Namespacing

All spawned containers are namespaced to prevent collisions:

- Container names are prefixed with `<sandbox-id>-` (e.g. `my-project-coder-mydb`)
- `docker ps` only shows containers belonging to this sandbox
- `docker start/stop/rm/logs` by user-provided name works transparently (the proxy translates)

## Network Isolation

Spawned containers join the sandbox's internal network:
- They can reach the agent and each other by container name
- They cannot reach the internet (network is `internal: true`)
- Their traffic does not bypass the gateway

## Lifecycle

When the sandbox shuts down (`docker compose down`), the proxy:
1. Lists all containers with the sandbox label
2. Stops each (5s timeout)
3. Removes each

No orphaned containers are left behind.

## Usage Patterns

### Run a database for testing

```bash
docker run -d --name testdb postgres:16-alpine
docker exec testdb psql -U postgres -c "CREATE DATABASE myapp;"
```

### Run a one-off script

```bash
docker run -d --name build node:20-slim sh -c "npm install && npm test"
docker logs -f build
```

### Multiple services

```bash
docker run -d --name redis redis:7-alpine
docker run -d --name api node:20-slim node server.js
```

## Known Limitations

- **Interactive `docker run` (without `-d`)** hangs due to stream-close timing on the attach connection. Use `docker run -d` + `docker logs`, or `docker create` + `docker start` instead.
- **Image pulls** go through the Docker daemon's configured registries. The proxy only validates image names against the allowlist — it doesn't control where images are fetched from.
- **No volume support (default mode)** — The `/volumes/*` API is blocked because volumes persist on the host filesystem, which breaks sandbox isolation (see [Blocked API Endpoints](#blocked-api-endpoints)). When `allow_compose: true`, volumes are permitted for inner service coordination. Containers can share data via the sandbox network (one container serves data, another consumes it) or temporary files within the container.

## Nested Sandboxes

When `allow_compose: true` is set, the agent can run `docker compose` inside the sandbox to orchestrate inner services — including running another agent-sandbox stack for self-debugging or self-improvement.

### How it works

```
┌─ Outer Sandbox ─────────────────────────────────────────────────┐
│  Agent Container (DOCKER_HOST → proxy)                           │
│  ├─ agent-sandbox generate                                       │
│  └─ docker compose up --build -d                                 │
│                                                                   │
│  Docker Proxy (allow_compose: true)                              │
│  ├─ Allows: network create (forces Internal=true)                │
│  ├─ Allows: volume create/list/remove                            │
│  ├─ Namespaces: networks with sandbox ID prefix                  │
│  └─ Dual-attaches: containers get compose net + sandbox net      │
│                                                                   │
│  ┌─ Inner Sandbox (compose project) ───────────────────────────┐ │
│  │  inner-agent ←─ compose network ─→ inner-gateway             │ │
│  │       └──── sandbox network ────→ outer gateway ──→ internet │ │
│  └──────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────┘
```

Inner containers are dual-attached:
- **Compose network** — for service discovery between inner services (agent ↔ gateway)
- **Sandbox network** — for routing through the outer gateway to the internet

### Configuration

```yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "alpine:*"
        - "node:20-*"
      max_containers: 10
      allow_compose: true
```

### Security invariants (still enforced)

- All created networks are forced to `Internal: true` — inner containers can't bypass the outer gateway
- Network names are namespaced with the sandbox ID to prevent collisions
- All containers still get resource limits, labels, and policy enforcement
- Cleanup removes inner containers AND networks on shutdown
- Image allowlist, privileged mode blocking, and host mount blocking still apply

### What the agent can do

```bash
# Inside the agent container
git clone /workspace /tmp/inner-sandbox
cd /tmp/inner-sandbox
agent-sandbox generate
docker compose -f .build/docker-compose.yml up --build -d
docker compose -f .build/docker-compose.yml logs -f
docker compose -f .build/docker-compose.yml down
```

### Additional endpoints unlocked

When `allow_compose: true`, these endpoints become available (blocked by default):

| Endpoint | Constraint |
|----------|-----------|
| `POST /networks/create` | Forces `Internal: true`, namespaces name, adds sandbox label |
| `DELETE /networks/{id}` | Only networks created by this sandbox |
| `GET /networks` | Filtered to sandbox-owned networks only |
| `POST /networks/{id}/connect` | Allowed (compose needs it for service wiring) |
| `POST /volumes/create` | Allowed for inter-service data sharing |
| `GET /volumes` | Allowed |
| `DELETE /volumes/{id}` | Allowed |

## Prerequisites

- The agent container needs the Docker CLI installed (add `RUN curl -fsSL https://get.docker.com | sh` to `extra_builds`)
- The host must have Docker running with the socket at `/var/run/docker.sock`
