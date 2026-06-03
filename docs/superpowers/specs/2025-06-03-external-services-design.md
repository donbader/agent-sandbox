# External Services Plugin Design

## Summary

A feature plugin that allows agent-sandbox agents to reach pre-existing Docker containers (running outside agent-sandbox) while maintaining the security model: all traffic flows through the sandbox gateway.

## Problem

Agents need to connect to locally running services (LLM proxies, databases, MCP servers, etc.) that are managed outside agent-sandbox. Currently, the agent container is isolated on an internal network with only gateway access. There is no mechanism to reach external Docker containers.

## Design

### Config

```yaml
features:
  - plugin: external-services
    services:
      - name: rkgw
        network: rkgw-external
      - name: postgres
        network: my-db-net
```

- `name` — label for the service (used for documentation and validation; also the Docker DNS hostname the agent uses to reach it)
- `network` — pre-existing external Docker network that the service is on

### How It Works

1. The **gateway container** joins each specified external network
2. Docker's embedded DNS (available on joined networks) resolves service hostnames
3. Agent traffic still routes through the gateway via the existing IP forwarding setup
4. The gateway forwards DNS queries for external service hostnames and proxies TCP connections

### Network Topology

```
┌─────────────────────────────────────┐
│  agent-sandbox compose              │
│                                     │
│  ┌─────────┐       ┌───────────┐   │
│  │  agent  │──────▶│  gateway  │   │
│  └─────────┘       └───────────┘   │
│   (internal)        (internal +     │
│                      default +      │
│                      rkgw-external) │
└─────────────────────────────────────┘
                           │
                    rkgw-external network
                           │
                    ┌──────────────┐
                    │  rkgw (ext)  │
                    └──────────────┘
```

### Generated docker-compose Changes

The plugin adds external networks to the gateway service:

```yaml
services:
  <agent>-gateway:
    networks:
      internal:
      default:
      rkgw-external:   # added by external-services plugin
      my-db-net:       # added by external-services plugin

networks:
  rkgw-external:
    external: true
  my-db-net:
    external: true
```

### Gateway DNS

The gateway runs a DNS server that currently forwards all queries to `8.8.8.8:53`. Docker container names on external networks are only resolvable through Docker's embedded DNS at `127.0.0.11`.

**Required change:** The gateway DNS forwarder must try Docker's embedded DNS (`127.0.0.11:53`) first, then fall back to `8.8.8.8:53` for internet domains. This ensures external service hostnames (like `rkgw`) resolve when the gateway has joined their network.

### Security Model

- Agent never directly connects to external networks (stays on `internal` only)
- All traffic routes through gateway — MITM/logging/credential injection still applies
- Gateway can optionally be configured to restrict which external hosts are reachable (future enhancement, not in scope)
- External services are not automatically trusted — no credential injection unless separately configured (e.g., via `static-header` plugin)

### What the Plugin Does NOT Do

- Does not inject environment variables into the agent container
- Does not configure agent runtime settings (models.json, config.toml, etc.)
- Does not validate that the external network/service exists at generate time (fails at compose-up time)
- Does not manage the external service's lifecycle

### Implementation

The plugin is **pure data** — no Go code needed beyond what the generator already handles. It:

1. Reads `services[].network` from config
2. Adds each network to the gateway service's `networks` list in docker-compose
3. Adds each network as `external: true` in the top-level `networks` section

### Plugin Files

```
internal/plugins/external-services/
  feature.yaml    ← metadata + config schema
  plugin.go       ← typed Config struct, generates compose network entries
```

### Config Schema

```go
type ServiceConfig struct {
    Name    string `yaml:"name" schema:"required,description=Label and DNS hostname for the service"`
    Network string `yaml:"network" schema:"required,description=External Docker network the service is on"`
}

type Config struct {
    Services []ServiceConfig `yaml:"services" schema:"required,minItems=1"`
}
```

### Validation

- `name` must be non-empty
- `network` must be non-empty
- Duplicate network names are deduplicated (multiple services on the same network)

### Example Usage

```yaml
# agent.yaml
name: dorey
runtime: pi

features:
  - plugin: telegram
    bot_token: ${TELEGRAM_BOT_TOKEN}
    access_control:
      allowed_users: ["@${TELEGRAM_USERNAME}"]

  - plugin: external-services
    services:
      - name: rkgw
        network: rkgw-external

  - plugin: static-header
    name: rkgw-auth
    domains: ["rkgw"]
    header: "Authorization"
    value_format: "Bearer ${value}"
    secret: "${RKGW_API_KEY}"
```

The agent's runtime config (e.g., pi's `models.json`) would then point at `http://rkgw:8765/v1/messages` — reachable because the gateway joined `rkgw-external`.
