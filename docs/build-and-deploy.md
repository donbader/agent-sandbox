# Build & Deploy

## Build Flow

```
agent-sandbox generate
  │
  ├── Detect mode: agent.yaml (single) or fleet.yaml (multi)
  ├── Read preset: core/presets/<runtime>/
  ├── Read feature plugins: core/plugins/<feature>/
  ├── Merge shared features (if fleet mode)
  │
  └── Generate .build/:
        ├── gateway-src/        (extracted from embed.FS: core/gateway + core/sdk)
        ├── home-override/      (from user's home/ dir)
        ├── Dockerfile.gateway  (gateway container: compile + minimal alpine)
        ├── Dockerfile          (agent container: runtime + entrypoint)
        ├── config.yaml         (gateway runtime config: MITM domains + rewriters)
        ├── entrypoint.sh       (agent: iptables DNAT + CA cert install + exec)
        └── docker-compose.yml  (two services + shared certs volume + internal network)

agent-sandbox compose up --build -d
  │
  └── docker compose -f .build/docker-compose.yml up --build -d
```

## Plugin Resolution

CLI resolves plugins in order:
1. Inline definition in agent.yaml (for custom runtimes)
2. Built-in plugins (embedded in CLI binary as YAML/templates)

## Generated Dockerfiles (Separate Containers)

When gateway is enabled, two Dockerfiles are generated for security isolation:

### Dockerfile.gateway

```dockerfile
# Compile gateway binary
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY gateway-src/ .
RUN go mod tidy && CGO_ENABLED=0 go build -o /gateway ./cmd/gateway/

# Minimal runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /gateway /usr/local/bin/gateway
COPY config.yaml /etc/gateway/config.yaml
ENTRYPOINT ["gateway"]
```

### Dockerfile (Agent)

```dockerfile
FROM node:24-slim

# System packages (iptables for transparent proxy routing)
RUN apt-get update && apt-get install -y --no-install-recommends \
    iptables ca-certificates git curl iputils-ping \
    && rm -rf /var/lib/apt/lists/*

# Agent user
RUN useradd -m -s /bin/bash agent

# Runtime install (from preset)
RUN npm install -g @openai/codex

# Home override
COPY home-override/ /opt/home-override/

COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh
ENTRYPOINT ["/opt/entrypoint.sh"]
CMD ["sleep", "infinity"]
```

The agent entrypoint:
1. Waits for gateway health check (`curl http://gateway:8080/health`)
2. Resolves gateway container IP
3. Sets up iptables DNAT: outbound TCP 443 → gateway:8443
4. Installs gateway's CA cert from shared volume (`/shared/certs/ca.crt`)
5. Execs into CMD

### Dockerfile Without Gateway (Simple Mode)

When no features need gateway, the Dockerfile is simple:

```dockerfile
FROM node:24-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @openai/codex

RUN useradd -m -s /bin/bash agent
USER agent
WORKDIR /home/agent

CMD ["sleep", "infinity"]
```

## What Gets Embedded in CLI (go:embed)

| Content | Purpose | Size |
|---------|---------|------|
| Gateway core source (`core/gateway/`) | TCP proxy, SNI, MITM framework | ~15MB |
| SDK module (`core/sdk/`) | Gateway middleware interfaces | ~5KB |
| Built-in presets (`core/presets/`) | Base image, install commands, CMD | ~10KB |
| Built-in plugins (`core/plugins/`) | Feature plugin definitions | ~10KB |
| Root go.mod + go.sum | Gateway build dependencies | ~5KB |

Gateway rewriters (auth-header, telegram-url) are config-driven — instantiated at runtime from `config.yaml`, not compiled per-plugin.

## Gateway Compilation

The gateway binary is compiled during Docker build, not CLI build:

1. CLI extracts gateway core source from `embed.FS` to `.build/gateway-src/` (includes `core/gateway/` + `core/sdk/` + root `go.mod`/`go.sum`)
2. CLI generates `config.yaml` with rewriter rules from active feature plugins
3. Docker multi-stage compiles gateway binary into a single binary
4. Config-driven rewriter types (`telegram-url`, `auth-header`) are instantiated at runtime from `config.yaml`

This means:
- Gateway config changes = re-run `agent-sandbox generate`, rebuild container
- User doesn't need Go installed (Docker handles compilation)

## CA Certificate Lifecycle

The CA keypair is generated at **gateway startup** (not build time):

1. Gateway starts → generates ECDSA CA cert + key
2. Writes CA cert to shared volume: `/shared/certs/ca.crt`
3. Agent entrypoint waits for gateway health → copies CA cert from volume → `update-ca-certificates`
4. All MITM'd connections use certificates signed by this runtime-generated CA

## Channel Manager Loading

The channel manager is TypeScript. Runs as the container entrypoint when channels are active:

1. CLI extracts channel manager runtime to `.build/channel-manager-src/`
2. CLI copies active feature `channel/` dirs to `.build/channel-manager-plugins/<name>/`
3. Channel manager dynamically imports plugins at runtime from `/opt/channel-manager/plugins/<name>/`
4. Channel manager spawns agent CLI as child process (reads cmd from channel-manager-config.json)

## Multi-Agent Topology

```
┌─ Internal Network ──────────────────────────────────────────┐
│                                                              │
│  ┌─ coder ───────────────────────────────────────────────┐  │
│  │  Gateway (github + telegram rules)                     │  │
│  │  Agent (codex, iptables DNAT → gateway:8443)           │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌─ reviewer ────────────────────────────────────────────┐  │
│  │  Gateway (github rules)                                │  │
│  │  Agent (claude-code, iptables DNAT → gateway:8443)     │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

Each agent has its own gateway instance (same binary, different config). Agent traffic routes to its gateway via iptables DNAT (port 443 → gateway:8443).

## Project Structure

```
agent-sandbox/
  go.work

  cmd/agent-sandbox/        ← CLI binary (generic template engine)
    main.go

  core/
    gateway/                ← Gateway core source (embedded in CLI)
      go.mod
      cmd/gateway/main.go
      internal/proxy/       ← TCP listener, SNI routing, passthrough
      internal/mitm/        ← MITM handler, cert generation, rewriters
      internal/dns/         ← UDP DNS forwarder
      internal/redact/      ← Secret-masking slog handler

    sdk/                    ← Gateway middleware interfaces (embedded in CLI)
      go.mod
      gateway/middleware.go

    presets/                ← Runtime presets (codex, claude-code, pi)
      codex/preset.yaml
      claude-code/preset.yaml

    plugins/                ← Feature plugins (github-pat, mcp-oauth)
      github-pat/plugin.yaml
      mcp-oauth/plugin.yaml
```
