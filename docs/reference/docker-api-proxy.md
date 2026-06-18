# Docker API Proxy

The Docker API Proxy is implemented as the `@builtin/agent-docker` plugin. It provides policy-enforced Docker access for sandbox agents through a sidecar proxy.

## Documentation

Full documentation is in the plugin README:

→ [`core/plugins/agent-docker/README.md`](../../core/plugins/agent-docker/README.md)

Covers:
- Architecture and security model
- Options (allowed_images, max_containers, memory, cpus, pids, allow_compose, allow_build)
- Image allowlist and request validation
- Compose mode (multi-service orchestration)
- Build mode (docker build with BuildKit support)
- Namespacing, network isolation, resource limits
- Known limitations and usage patterns

## Quick Start

```yaml
installations:
  - plugin: "@builtin/agent-docker"
    options:
      allowed_images:
        - "alpine:*"
        - "node:20-*"
        - "postgres:16-*"
      max_containers: 5
      memory: "2g"
      cpus: "2"
      pids: 256
      allow_compose: true
      allow_build: true
```
