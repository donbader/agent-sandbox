# Namespace Translation for Docker Proxy

## Problem

The proxy namespaces container names (`mydb` → `{sandbox-id}-mydb`) and reverse-translates on read. Volumes and networks are pass-through — no namespacing, no isolation. In nested sandbox scenarios this causes name collisions, cross-layer visibility, and abstraction leakage.

## Goal

Uniform namespace translation for containers, volumes, and networks. Each agent sees a clean Docker environment as if it's running on bare metal. Works recursively at any depth.

## Design

### Single rule

All user-created resource names get prefixed with `{SANDBOX_ID}-` on write and stripped on read. Same logic for all three resource types.

```
Agent says:  docker network create mynet
Proxy stores: my-agent-team-v3-dorey-001-mynet
Agent sees:   mynet
```

### Translation points

| Operation | Direction | What proxy does |
|-----------|-----------|-----------------|
| Create (container/volume/network) | agent → Docker | Prefix name with `{SANDBOX_ID}-` |
| List / Inspect | Docker → agent | Strip `{SANDBOX_ID}-` prefix, hide resources without it |
| Remove | agent → Docker | Prefix name before forwarding |
| Bind mount referencing a volume | agent → Docker | Prefix volume name in mount spec |
| `--network` flag on container create | agent → Docker | Prefix network name |

### Filtering

On list operations, proxy returns **only** resources whose real name starts with `{SANDBOX_ID}-`. Agent never sees resources from other sandboxes or the host.

### Nested behavior

Each proxy only knows its own `SANDBOX_ID`. Nesting naturally stacks prefixes:

```
Level 0 (host):     real Docker daemon
Level 1 proxy:      SANDBOX_ID = outer-agent
Level 2 proxy:      SANDBOX_ID = inner-agent

Agent at level 2 creates "mydb":
  Level 2 proxy prefixes → "inner-agent-mydb"
  Level 1 proxy prefixes → "outer-agent-inner-agent-mydb"
  Real Docker sees:        "outer-agent-inner-agent-mydb"

Agent at level 2 lists containers:
  Real Docker returns:     "outer-agent-inner-agent-mydb"
  Level 1 strips its prefix → "inner-agent-mydb" (visible to level 2 proxy)
  Level 2 strips its prefix → "mydb" (visible to agent)
```

No proxy needs to know how many layers exist. Each just prefixes/strips its own ID.

### What about Compose?

Docker Compose adds its own project prefix (`project-service-1`). This is fine — Compose's prefix becomes part of the "user name" that gets sandboxed:

```
Agent runs: docker compose up (project "myapp")
Compose creates: myapp-redis-1
Proxy stores: {SANDBOX_ID}-myapp-redis-1
Agent sees: myapp-redis-1
```

### Socket interception (existing)

When a container mounts `/var/run/docker.sock`, the proxy removes it and injects `DOCKER_HOST=tcp://{proxy}:2375`. That container's Docker calls go through the same proxy — same namespace rules apply. No second proxy needed.

If that container runs its own `agent-sandbox` (spawning a new proxy), the new proxy gets its own `SANDBOX_ID` and connects upstream via `DOCKER_HOST`. Prefix stacking handles isolation automatically.

## Implementation

1. Extract existing container name prefixing into a shared `namespacer` helper
2. Apply same helper to volume and network API handlers
3. Add response filtering to `GET /volumes`, `GET /networks` (same pattern as containers)
4. Translate volume names inside container create body (`Mounts[].Source`, `HostConfig.Binds`)
5. Translate network names inside container create body (`NetworkingConfig`)

## Out of scope

- Image names (already handled by allowlist, no namespace needed)
- Exec/attach (uses container ID, already namespaced)
- Volume subpath translation (existing feature, orthogonal)
