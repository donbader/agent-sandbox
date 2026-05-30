# Docker API Proxy [PLANNED]

> ⚠️ This feature is not yet implemented. This document describes the planned design.

## Overview

The Docker API Proxy is an optional component that allows agents to spin up Docker containers in a controlled, policy-enforced way. It sits between the agent and the Docker daemon, validating every API call against a security policy.

## Why Not Direct Docker Access?

Giving an agent raw Docker socket access is equivalent to giving it root on the host:

```bash
# With raw Docker socket, an agent could:
docker run --privileged --pid=host -v /:/host ubuntu chroot /host
# → Full host access, game over
```

The Docker API Proxy prevents this by intercepting and validating every Docker API call.

## Architecture

```
┌─ Agent Container ───────────────────────────────────────────┐
│                                                             │
│  Agent                                                      │
│    │                                                        │
│    │ DOCKER_HOST=tcp://docker-proxy:2375                    │
│    │ (standard Docker client, no special SDK needed)        │
│    ▼                                                        │
└────┼────────────────────────────────────────────────────────┘
     │
     ▼
┌─ Docker API Proxy ──────────────────────────────────────────┐
│                                                             │
│  1. Authenticate request (sandbox token)                    │
│  2. Parse Docker API call                                   │
│  3. Validate against policy                                 │
│  4. Reject or forward to Docker daemon                      │
│  5. Mutate: force network, inject labels, set limits        │
│                                                             │
└────┼────────────────────────────────────────────────────────┘
     │
     ▼
┌─ Docker Daemon ─────────────────────────────────────────────┐
│  Creates container on internal network                       │
└─────────────────────────────────────────────────────────────┘
```

## Configuration

```yaml
# In fleet.yaml
agents:
  coder:
    runtime: codex
    egress: [docker, main]
    channel: ...
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

## Policy Enforcement

### On Container Create (`/containers/create`)

The proxy validates and mutates the create request:

**Validation checks:**

| Check | Action on Violation |
|-------|-------------------|
| Image not in allowlist | Reject with 403 |
| `Privileged: true` | Reject with 403 |
| `NetworkMode: host` | Reject with 403 |
| `CapAdd` not empty | Reject with 403 |
| `PidMode: host` | Reject with 403 |
| `IpcMode: host` | Reject with 403 |
| Host path bind mounts | Reject with 403 |
| Container count > max | Reject with 429 |

**Mutations (always applied):**

| Field | Forced Value |
|-------|-------------|
| `NetworkMode` | Internal network (same as sandbox) |
| `Memory` | Policy limit (e.g., 2GB) |
| `NanoCPUs` | Policy limit (e.g., 2 CPUs) |
| `PidsLimit` | Policy limit (e.g., 256) |
| `Labels` | `agent-fleet.agent=<name>`, `agent-fleet.sandbox=<id>` |
| `RestartPolicy` | `no` (prevent zombie containers) |

### On Image Pull (`/images/create`)

```
1. Extract image name from ?fromImage= parameter
2. Check against allowlist (glob matching)
3. If not allowed → 403
4. If allowed → forward to Docker daemon
```

## Allowed API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/containers/create` | POST | Create a container (validated) |
| `/containers/{id}/start` | POST | Start a container |
| `/containers/{id}/stop` | POST | Stop a container |
| `/containers/{id}/kill` | POST | Kill a container |
| `/containers/{id}` | DELETE | Remove a container |
| `/containers/{id}/json` | GET | Inspect a container |
| `/containers/{id}/logs` | GET | Get container logs |
| `/containers/{id}/exec` | POST | Create exec instance |
| `/exec/{id}/start` | POST | Start exec instance |
| `/images/json` | GET | List images |
| `/images/create` | POST | Pull an image (validated) |

### Blocked Endpoints

| Endpoint | Reason |
|----------|--------|
| `/volumes/*` | Prevent host filesystem access |
| `/networks/*` | Prevent network manipulation |
| `/swarm/*` | Prevent cluster operations |
| `/secrets/*` | Prevent secret access |
| `/configs/*` | Prevent config access |
| `/system/*` | Prevent system info leakage |

## Authentication

The proxy authenticates requests using a sandbox-scoped token:

```
Agent → Docker API Proxy
  Header: X-Sandbox-Token: <jwt>
  
Proxy validates:
  - Token signed by proxy
  - Token bound to this sandbox ID
  - Token not expired
```

Agent-spawned containers do NOT receive this token — they cannot talk to the proxy.

## Lifecycle Management

### Container Cleanup

When the sandbox is destroyed, the proxy cleans up all agent-spawned containers:

```
1. agent-fleet compose down (or sandbox timeout)
2. Proxy queries Docker for containers with label agent-fleet.sandbox=<id>
3. Proxy stops and removes all matching containers
4. Proxy removes itself
```

## Network Behavior

New containers join the same internal network as the sandbox. Their egress also goes through the gateway proxy:

```
Agent-spawned container
  → makes HTTP request
  → hits internal network
  → routed through gateway proxy
  → egress rules apply (same as agent)
```

This means agent-spawned containers:
- ✅ Can reach allowed hosts
- ✅ Get credential injection where configured
- ❌ Cannot bypass egress rules
- ❌ Cannot reach the Docker API Proxy (no auth token)

## Implementation Notes

The Docker API Proxy is a small Go binary (~500 lines core logic):
- HTTP reverse proxy with request interception
- JSON parsing for Docker API request bodies
- Glob matching for image allowlist
- JWT validation for authentication
- Docker client for cleanup operations

Deployed as a container on the internal network, managed by agent-fleet lifecycle.
