# Build & Deploy

## Build Flow

```
agent-sandbox generate
  │
  ├── Detect mode: agent.yaml (single) or fleet.yaml (multi)
  ├── Read runtime plugin: plugins/<runtime>/runtime.yaml
  ├── Read feature plugins: plugins/<feature>/feature.yaml (for each)
  ├── Merge shared features (if fleet mode)
  │
  └── Generate .build/:
        ├── gateway-src/        (embedded gateway core + feature gateway/ dirs)
        ├── channel-manager-src/     (embedded channel manager + channel plugins)
        ├── home-override/      (from user's home/ dir)
        ├── hooks/              (from feature entrypoint hooks)
        ├── Dockerfile.gateway  (gateway container: compile + minimal alpine)
        ├── Dockerfile.agent    (agent container: channel-manager build + runtime)
        ├── gateway-config.yaml (merged hosts from feature.yaml files)
        ├── gateway-entrypoint.sh
        ├── entrypoint.sh       (agent: default route + channel-manager/agent start)
        ├── channel-manager-config.json (channels + agent cmd from runtime.yaml)
        ├── certs/              (CA cert + key for MITM)
        └── docker-compose.yml  (two services + internal network)

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
COPY gateway-config.yaml /etc/gateway/config.yaml
COPY certs/ /etc/gateway/
COPY gateway-entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh
ENTRYPOINT ["/opt/entrypoint.sh"]
```

### Dockerfile.agent

```dockerfile
# Stage 1: Compile channel manager (if channels active)
FROM node:22-slim AS channel-manager-build
WORKDIR /src
COPY channel-manager-src/package.json channel-manager-src/tsconfig.json ./
RUN npm install
COPY channel-manager-src/src/ ./src/
RUN npm run build

# Stage 2: Agent runtime
FROM node:22-slim

# System packages (iproute2 for default route setup)
RUN apt-get update && apt-get install -y --no-install-recommends \
    iproute2 ca-certificates git curl \
    && rm -rf /var/lib/apt/lists/*

# Agent user
RUN useradd -m -s /bin/bash agent

# Trust sandbox CA (for MITM'd connections)
COPY certs/ca.crt /usr/local/share/ca-certificates/sandbox-ca.crt
RUN update-ca-certificates

# Channel Manager
COPY --from=channel-manager-build /src/dist/ /opt/channel-manager/dist/
COPY --from=channel-manager-build /src/node_modules/ /opt/channel-manager/node_modules/
COPY channel-manager-config.json /opt/channel-manager/config.json

# Runtime install (from runtime.yaml)
RUN npm install -g @openai/codex

# Feature commands
RUN apt-get update && apt-get install -y ripgrep fd-find

# Home override + hooks
COPY home-override/ /opt/home-override/
COPY hooks/ /opt/hooks/

COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/entrypoint.sh
ENTRYPOINT ["/opt/entrypoint.sh"]
CMD ["sleep", "infinity"]
```

### Dockerfile Without Gateway/Channel Manager (Phase 1-2)

When no features need gateway or channel manager, the Dockerfile is simple:

```dockerfile
FROM node:22-slim

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
| Gateway core source | TCP proxy, SNI, MITM framework | ~15MB |
| Channel manager runtime | TypeScript: process spawning, plugin loader | ~5MB |
| Built-in plugin YAML | runtime.yaml + feature.yaml defaults | ~10KB |
| Entrypoint template | Shell script template | ~2KB |

Gateway handlers (per-feature Go code) are NOT embedded in CLI. They live in plugin directories and are copied to `.build/gateway-src/` at generate time.

## Gateway Compilation

The gateway binary is compiled during Docker build, not CLI build:

1. CLI extracts gateway core source to `.build/gateway-src/`
2. CLI copies active feature `gateway/` dirs into `.build/gateway-src/handlers/`
3. CLI generates `handlers_registry.go` (imports active handlers)
4. Docker multi-stage compiles everything into one binary

This means:
- Gateway handler fixes = edit local `plugins/<name>/gateway/`, rebuild container
- No CLI upgrade needed for gateway fixes
- User doesn't need Go installed (Docker handles compilation)

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
│  │  Gateway (github + docker + telegram rules)            │  │
│  │  Channel Manager → codex exec                              │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌─ reviewer ────────────────────────────────────────────┐  │
│  │  Gateway (github + telegram rules)                     │  │
│  │  Channel Manager → claude-code                             │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌─ dind (shared) ───────────────────────────────────────┐  │
│  │  Docker daemon                                         │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

Each agent has its own gateway instance (same core, different config + handlers). DinD shared if multiple agents need Docker.

## Project Structure

```
agent-sandbox/
  go.work

  cmd/agent-sandbox/        ← CLI binary (generic template engine)
    main.go
    cmd/                    ← cobra commands (generate, compose)

  sdk/                      ← Plugin SDK (interfaces for gateway handlers)
    go.mod
    plugin.go

  gateway/                  ← Gateway core source (embedded in CLI)
    go.mod
    cmd/gateway/main.go
    proxy.go, sni.go, mitm.go
    handler_interface.go    ← RequestHandler interface

  channel-manager/          ← Channel manager runtime TypeScript (embedded in CLI)
    package.json
    src/index.ts, agent.ts, plugin-loader.ts, types.ts

  internal/
    plugins/               ← Core plugins (embedded in CLI)
      codex/
        runtime.yaml
      claude-code/
        runtime.yaml
      pi/
        runtime.yaml
      telegram/
        feature.yaml
        gateway/handler.go, go.mod
        channel/            ← TypeScript channel implementation
      github-pat/
        feature.yaml
        gateway/handler.go, go.mod
      static-header/
        feature.yaml
        gateway/handler.go, go.mod
      custom-runtime/
        feature.yaml
        plugin.go           ← typed Config struct
    generate/               ← Dockerfile + compose generation (template engine)
    config/                 ← agent.yaml + fleet.yaml parsing
    resolve/                ← plugin resolution (local → embedded)

  ext/
    plugins/               ← External plugins (per-plugin versioning, user overrides)

  templates/               ← entrypoint.sh template, Dockerfile.tmpl
```
