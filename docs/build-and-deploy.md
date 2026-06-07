# Build & Deploy

## Build Flow

```
agent-sandbox generate
  │
  ├── Detect mode: agent.yaml (single) or fleet.yaml (multi)
  ├── Load project .env file (resolves secret references for plugins)
  ├── Read preset: core/presets/<runtime>/
  ├── Read feature plugins: core/plugins/<feature>/
  ├── Merge shared features (if fleet mode)
  │
  └── Generate .build/:
        ├── gateway-src/        (fetched from GitHub Releases: core/gateway + core/sdk)
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
2. Built-in plugins (fetched from GitHub Releases, cached locally)

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

## What Gets Fetched from GitHub Releases

| Content | Purpose | Size | Cache |
|---------|---------|------|-------|
| Gateway core source (`core/gateway/`) | TCP proxy, SNI, MITM framework | ~15MB | Indefinite (per version) |
| SDK module (`core/sdk/`) | Gateway middleware interfaces | ~5KB | Indefinite (per version) |
| Built-in presets (`core/presets/`) | Base image, install commands, CMD | ~10KB | Indefinite (per version) |
| Built-in plugins (`core/plugins/`) | Feature plugin definitions | ~10KB | Indefinite (per version) |
| Root go.mod + go.sum | Gateway build dependencies | ~5KB | Indefinite (per version) |
| Generation templates (`templates/`) | Dockerfile, entrypoint, gateway Dockerfile | ~5KB | Indefinite (per version) |

For `core_version: latest`, the CLI queries the GitHub API to find the newest `core-v*` release (cached 1h). For specific versions (e.g. `core_version: v1.0.0`), the tarball is cached indefinitely.

Gateway rewriters (auth-header, telegram-url) are config-driven — instantiated at runtime from `config.yaml`, not compiled per-plugin.

## Secret Resolution via .env

The `generate` command loads the project's `.env` file early in the build flow. This resolves `${SECRET_NAME}` references in plugin options to their actual values. The primary consumer is the `auth-header` gateway middleware — resolved credentials are baked into the gateway binary's `config.yaml` at compile time so the gateway can inject auth headers without runtime env var access.

## Generated Compose Behavior

The generated `docker-compose.yml` includes several conveniences:

- **Network alias:** The agent service gets a `agent` network alias, so sidecar containers can reach it via hostname `agent` (e.g. `ws://agent:3100/acp`).
- **Healthcheck:** If the agent runtime exposes ports (declared in `preset.yaml`), a healthcheck is added using `curl` against the first declared port's `/health` endpoint.
- **Sidecar dependency:** Any sidecar services defined by plugins implicitly get `depends_on: { agent: { condition: service_healthy } }`, ensuring the agent is healthy before sidecars start.

## Template Functions

Plugin `contributes.runtime.extra_builds` lines are evaluated as Go templates. Available functions:

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON` | Serializes any value to a JSON string | `{{ toJSON .options.my_config }}` |

## Gateway Compilation

The gateway binary is compiled during Docker build, not CLI build:

1. CLI fetches gateway core source from GitHub Releases cache to `.build/gateway-src/` (includes `core/gateway/` + `core/sdk/` + root `go.mod`/`go.sum`)
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
    gateway/                ← Gateway core source (fetched from Releases)
      go.mod
      cmd/gateway/main.go
      internal/proxy/       ← TCP listener, SNI routing, passthrough
      internal/mitm/        ← MITM handler, cert generation, rewriters
      internal/dns/         ← UDP DNS forwarder
      internal/redact/      ← Secret-masking slog handler

    sdk/                    ← Gateway middleware interfaces (fetched from Releases)
      go.mod
      gateway/middleware.go

    presets/                ← Runtime presets (codex, claude-code, pi)
      codex/preset.yaml
      claude-code/preset.yaml

    plugins/                ← Feature plugins (github-pat, mcp-oauth)
      github-pat/plugin.yaml
      mcp-oauth/plugin.yaml
```
