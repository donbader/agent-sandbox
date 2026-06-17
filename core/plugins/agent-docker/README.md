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
| `allowed_images` | string[] | yes | — | Glob patterns for allowed images |
| `max_containers` | number | no | 5 | Max concurrent spawned containers |
| `memory` | string | no | "2g" | Memory limit per container (e.g. "512m", "4g") |
| `cpus` | string | no | "2" | CPU limit per container (e.g. "0.5", "4") |
| `pids` | number | no | 256 | PID limit per container |

## Image Allowlist

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

## Blocked API Endpoints

| Endpoint | Reason |
|----------|--------|
| `/volumes/*` | Prevents host filesystem access |
| `/networks/*` | Prevents network manipulation |
| `/swarm/*` | Prevents cluster operations |
| `/secrets/*` | Prevents secret access |
| `/configs/*` | Prevents config access |
| `/system/*` | Prevents system info leakage |

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
- **No volume support** — `/volumes/*` endpoints are blocked. Containers share data via the sandbox network or temporary files within the container.

## Prerequisites

- The agent container needs the Docker CLI installed (add `RUN curl -fsSL https://get.docker.com | sh` to `extra_builds`)
- The host must have Docker running with the socket at `/var/run/docker.sock`
