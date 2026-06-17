# Docker API Proxy — Gateway-Integrated Plugin

**Date:** 2025-06-17
**Status:** Draft
**Implements:** `docs/reference/docker-api-proxy.md`

## Summary

Allow agents to spin up Docker containers for debugging and development by exposing a policy-enforced Docker API proxy inside the existing gateway binary. Delivered as a built-in plugin at `core/plugins/docker/`.

## Architecture

The Docker API proxy runs as an HTTP handler inside the gateway, listening on `:2375`. It reverse-proxies validated requests to the host Docker daemon via `/var/run/docker.sock`.

```
Agent Container                      Gateway Container
+-------------------+               +----------------------------+
|  Agent process    |               |  :8443 - MITM proxy        |
|  docker CLI       |--- :2375 --->|  :8080 - health + HTTP     |
|                   |               |  :2375 - Docker API proxy  |
+-------------------+               |         |                  |
                                    |         v                  |
                                    |  /var/run/docker.sock      |
                                    +----------------------------+
                                             |
                                             v
                                    +-------------------+
                                    |  Docker Daemon    |
                                    +-------------------+
                                             |
                                    spawns sibling containers
                                    (namespaced to sandbox)
```

No JWT auth required. Network-level isolation is sufficient: only the agent container can reach the gateway on the internal network, and spawned containers are not given access to port 2375.

## Plugin Structure

```
core/plugins/docker/
  plugin.yaml
```

The plugin contributes:
- Docker socket volume mount on the gateway container
- `DOCKER_HOST=tcp://gateway:2375` env var on the agent container
- Gateway config section for Docker policy

No TypeScript middleware needed — this is pure Go logic in the gateway binary.

## Configuration

```yaml
# agent.yaml or fleet.yaml
docker:
  enabled: true
  allowed_images:
    - "node:20-*"
    - "python:3.12-*"
    - "golang:1.22-*"
    - "postgres:16-*"
    - "redis:7-*"
  max_containers: 5
  resource_limits:
    memory: "2g"
    cpus: "2"
    pids: 256
```

## Namespacing

All spawned containers are namespaced to the sandbox instance:

### Naming

Container names are prefixed with the sandbox ID:
- `<sandbox-id>-<agent-name>-<user-requested-name>` (if `--name` provided)
- `<sandbox-id>-<agent-name>-<random>` (if no name provided)

### Visibility

The proxy filters all list/inspect/stop/rm/logs/exec calls so that an agent can only see and manage containers matching its own `agent-sandbox.sandbox=<id>` label. Requests targeting containers from other sandboxes or the host return 404.

### Network

Spawned containers join the sandbox's bridge network. They are isolated from other sandboxes' containers and get their egress routed through the gateway (same rules as the agent).

## Gateway Changes

New package: `core/gateway/internal/docker/`

| File | Responsibility |
|------|---------------|
| `proxy.go` | HTTP reverse proxy to Docker socket, request routing, endpoint allowlist |
| `policy.go` | Validates container create requests, image allowlist (glob matching), max container enforcement |
| `mutate.go` | Injects labels, forces network mode, sets resource limits, sets restart policy, namespaces container names |
| `cleanup.go` | On shutdown: queries Docker for labeled containers, stops (5s timeout), removes |

The gateway's main wiring starts the Docker proxy listener on `:2375` when docker config is present in the gateway config.

## Policy Enforcement

### Blocked on Container Create

| Check | Response |
|-------|----------|
| Image not in allowlist | 403 |
| `Privileged: true` | 403 |
| `NetworkMode: host` | 403 |
| `CapAdd` not empty | 403 |
| `PidMode: host` | 403 |
| `IpcMode: host` | 403 |
| Host path bind mounts | 403 |
| Container count >= `max_containers` | 429 |

### Mutations (always applied on create)

| Field | Forced Value |
|-------|-------------|
| `NetworkMode` | Sandbox internal network |
| `Memory` | Policy limit (e.g., 2GB) |
| `NanoCPUs` | Policy limit (e.g., 2 CPUs) |
| `PidsLimit` | Policy limit (e.g., 256) |
| `Labels` | `agent-sandbox.agent=<name>`, `agent-sandbox.sandbox=<id>` |
| `RestartPolicy` | `no` |
| Container name | Prefixed with `<sandbox-id>-<agent-name>-` |

### Image Pull (`/images/create`)

1. Extract image name from `?fromImage=` parameter
2. Check against allowlist (glob matching)
3. If not allowed: 403
4. If allowed: forward to Docker daemon

### Allowed Endpoints

| Endpoint | Method | Notes |
|----------|--------|-------|
| `/containers/create` | POST | Validated + mutated |
| `/containers/{id}/start` | POST | Namespace-checked |
| `/containers/{id}/stop` | POST | Namespace-checked |
| `/containers/{id}/kill` | POST | Namespace-checked |
| `/containers/{id}` | DELETE | Namespace-checked |
| `/containers/{id}/json` | GET | Namespace-checked |
| `/containers/{id}/logs` | GET | Namespace-checked |
| `/containers/json` | GET | Filtered to sandbox labels |
| `/containers/{id}/exec` | POST | Namespace-checked |
| `/exec/{id}/start` | POST | Tracked to parent container |
| `/images/json` | GET | Unfiltered |
| `/images/create` | POST | Allowlist-checked |

### Blocked Endpoints

| Endpoint | Reason |
|----------|--------|
| `/volumes/*` | Prevent host filesystem access |
| `/networks/*` | Prevent network manipulation |
| `/swarm/*` | Prevent cluster operations |
| `/secrets/*` | Prevent secret access |
| `/configs/*` | Prevent config access |
| `/system/*` | Prevent system info leakage |

All unrecognized endpoints return 403.

## Network Behavior

Spawned containers join the sandbox bridge network:
- Agent can reach spawned containers by container name (Docker DNS)
- Spawned containers get egress through the gateway proxy (same rules as the agent)
- Spawned containers cannot reach port 2375 on the gateway (not exposed to them)
- Spawned containers cannot reach other sandboxes' networks

## Lifecycle

On gateway shutdown (SIGTERM):
1. List containers with label `agent-sandbox.sandbox=<id>`
2. Stop each (5s timeout)
3. Remove each
4. Gateway exits

## Compose Generation Changes

When the docker plugin is active, the generator:
1. Mounts `/var/run/docker.sock:/var/run/docker.sock` on the gateway container
2. Exposes port 2375 on the gateway (internal network only, not published to host)
3. Adds `DOCKER_HOST=tcp://<gateway-service-name>:2375` to the agent container's environment
4. Passes docker policy config into gateway config.yaml

## Config Schema Changes

New fields in `internal/config/`:

```go
type DockerConfig struct {
    Enabled        bool           `yaml:"enabled"`
    AllowedImages  []string       `yaml:"allowed_images"`
    MaxContainers  int            `yaml:"max_containers"`
    ResourceLimits ResourceLimits `yaml:"resource_limits"`
}

type ResourceLimits struct {
    Memory string `yaml:"memory"`
    CPUs   string `yaml:"cpus"`
    PIDs   int    `yaml:"pids"`
}
```

Added to the agent `Config` struct as `Docker DockerConfig`.

## Gateway Config Changes

New section in gateway `config.yaml`:

```yaml
docker:
  enabled: true
  sandbox_id: "abc123"
  agent_name: "coder"
  network_name: "sandbox_abc123_default"
  allowed_images:
    - "node:20-*"
    - "python:3.12-*"
  max_containers: 5
  resource_limits:
    memory_bytes: 2147483648
    nano_cpus: 2000000000
    pids: 256
```

## Testing Strategy

- Unit tests for policy validation (allowed/blocked scenarios)
- Unit tests for mutation logic (labels, limits, name prefixing)
- Unit tests for namespace filtering (list only sandbox containers)
- Integration test: agent container runs `docker run`, verifies container appears, verifies cleanup on shutdown
