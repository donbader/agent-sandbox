# Docker API Proxy — Sidecar Plugin

**Date:** 2025-06-17
**Status:** Draft
**Depends on:** `2025-06-17-egress-hardening-design.md`
**Implements:** `docs/reference/docker-api-proxy.md`

## Summary

Allow agents to spin up Docker containers for debugging and development. Delivered as a built-in plugin (`@builtin/agent-docker`) that contributes a sidecar container running a policy-enforced Docker API proxy. The agent reaches the sidecar directly by service name on the sandbox network. Spawned containers are network-isolated by the egress hardening (sandbox network is `internal: true`).

## Architecture

```
sandbox network (internal: true)
+---------------+         +---------------------+
| Agent         |-------->| Docker Proxy        |
| DOCKER_HOST=  |  :2375  | (sidecar)           |
| tcp://agent-  |         | policy enforcement  |
| docker-proxy  |         | /var/run/docker.sock|
| :2375         |         +---------------------+
|               |               |
|               |          spawns containers
+---------------+          (on sandbox network)
      |                         |
      | all egress              | no egress (internal: true)
      v                         |
+------------------+            |
| Gateway          |            |
| (sole internet   |            |
|  path)           |            |
+------------------+
```

- Agent talks directly to Docker proxy sidecar by service name (east-west on sandbox network)
- No gateway involvement for Docker API traffic
- Spawned containers are on the sandbox network (`internal: true`) — they can reach the agent and each other but cannot reach the internet
- Agent's internet egress goes through gateway (enforced by iptables DNAT from egress hardening)

## Plugin Structure

```
core/plugins/agent-docker/
  plugin.yaml
  cmd/docker-proxy/
    main.go
    policy.go
    mutate.go
    cleanup.go
    Dockerfile
```

## Configuration

```yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "node:20-*"
        - "python:3.12-*"
        - "golang:1.22-*"
        - "postgres:16-*"
        - "redis:7-*"
      max_containers: 5
      memory: "2g"
      cpus: "2"
      pids: 256
```

## Plugin Definition (`core/plugins/agent-docker/plugin.yaml`)

```yaml
name: agent-docker
assets:
  - path: cmd/docker-proxy/
    exclude: []

options:
  allowed_images:
    type: array
    items:
      type: string
    required: true
    description: "Glob patterns for allowed Docker images"
  max_containers:
    type: number
    required: false
    default: 5
    description: "Maximum concurrent containers the agent can spawn"
  memory:
    type: string
    required: false
    default: "2g"
    description: "Memory limit per spawned container"
  cpus:
    type: string
    required: false
    default: "2"
    description: "CPU limit per spawned container"
  pids:
    type: number
    required: false
    default: 256
    description: "PID limit per spawned container"

contributes:
  runtime:
    environment:
      DOCKER_HOST: "tcp://agent-docker-proxy:2375"
  sidecar:
    services:
      agent-docker-proxy:
        build: "{{ asset \"cmd/docker-proxy\" }}"
        volumes:
          - "/var/run/docker.sock:/var/run/docker.sock"
        environment:
          ALLOWED_IMAGES: '{{ toJSON .plugin.options.allowed_images }}'
          MAX_CONTAINERS: "{{ .plugin.options.max_containers }}"
          MEMORY_LIMIT: "{{ .plugin.options.memory }}"
          CPU_LIMIT: "{{ .plugin.options.cpus }}"
          PID_LIMIT: "{{ .plugin.options.pids }}"
```

Note: `networks` is not declared — the generator assigns all sidecars to the sandbox network automatically. System-injected env vars (`SANDBOX_ID`, `SANDBOX_NETWORK`, `AGENT_NAME`) are also not declared here — the generator provides them to all sidecars.

## System-Injected Sidecar Environment

The generator automatically injects the following env vars into all sidecar services:

| Env var | Value | Example |
|---------|-------|---------|
| `SANDBOX_ID` | `<projectName>-<agentName>` | `my-project-coder` |
| `SANDBOX_NETWORK` | `<projectName>_sandbox` | `my-project_sandbox` |
| `AGENT_NAME` | `<agentName>` | `coder` |

The project name is derived from `filepath.Base(projectDir)` — the same value used for `--project-name` in `docker compose`. This ensures the sidecar knows the fully-qualified Docker network name and has a unique sandbox identity across fleet instances.

## Namespacing

All spawned containers are namespaced to the sandbox instance (identified by `SANDBOX_ID` = `<projectName>-<agentName>`):

### Naming

Container names are prefixed:
- `<sandbox-id>-<user-requested-name>` (if `--name` provided)
- `<sandbox-id>-<random>` (if no name provided)

### Visibility

The proxy filters all list/inspect/stop/rm/logs/exec calls so that an agent can only see and manage containers matching its own `agent-sandbox.sandbox=<id>` label. Requests targeting containers from other sandboxes or the host return 404.

### Network

Spawned containers join the sandbox network. They are isolated from the internet (network is `internal: true` per egress hardening spec). They can reach the agent and other spawned containers by name.

## Docker Proxy Sidecar

A small Go binary (~500 lines core logic):
1. Listens on `:2375` for Docker API requests
2. Validates requests against policy (image allowlist, blocked fields, max containers)
3. Mutates container create requests (inject labels, force network, set limits, namespace names)
4. Forwards valid requests to `/var/run/docker.sock`
5. Filters responses (list only sandbox-owned containers)
6. On SIGTERM: cleans up all labeled containers (stop + remove)

### Policy Enforcement

#### Blocked on Container Create

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

#### Mutations (always applied on create)

| Field | Forced Value |
|-------|-------------|
| `NetworkMode` | `sandbox` (the internal sandbox network) |
| `Memory` | Policy limit (e.g., 2GB) |
| `NanoCPUs` | Policy limit (e.g., 2 CPUs) |
| `PidsLimit` | Policy limit (e.g., 256) |
| `Labels` | `agent-sandbox.agent=<name>`, `agent-sandbox.sandbox=<id>` |
| `RestartPolicy` | `no` |
| Container name | Prefixed with `<sandbox-id>-<agent-name>-` |

#### Image Pull (`/images/create`)

1. Extract image name from `?fromImage=` parameter
2. Check against allowlist (glob matching)
3. If not allowed: 403
4. If allowed: forward to Docker daemon

#### Allowed Endpoints

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

#### Blocked Endpoints

| Endpoint | Reason |
|----------|--------|
| `/volumes/*` | Prevent host filesystem access |
| `/networks/*` | Prevent network manipulation |
| `/swarm/*` | Prevent cluster operations |
| `/secrets/*` | Prevent secret access |
| `/configs/*` | Prevent config access |
| `/system/*` | Prevent system info leakage |

All unrecognized endpoints return 403.

## Lifecycle

On Docker proxy shutdown (SIGTERM from `docker compose down`):
1. List containers with label `agent-sandbox.sandbox=<id>`
2. Stop each (5s timeout)
3. Remove each
4. Proxy exits

## No Gateway Changes Required

- Docker API traffic is east-west on the sandbox network (agent → sidecar). No gateway routing needed.
- Spawned container internet isolation is handled by egress hardening (`internal: true` network). No gateway changes needed.
- Plugin uses existing `contributes.sidecar.services` and `contributes.runtime.environment`. No plugin system extensions needed.

## Testing Strategy

- Unit tests for policy validation (allowed/blocked scenarios)
- Unit tests for mutation logic (labels, limits, name prefixing)
- Unit tests for namespace filtering (list only sandbox containers)
- Unit tests for image allowlist glob matching
- Integration test: agent runs `docker run`, container appears on sandbox network
- Integration test: spawned container cannot reach internet
- Integration test: cleanup on shutdown removes all labeled containers
- Integration test: agent cannot see host containers or containers from other sandboxes
