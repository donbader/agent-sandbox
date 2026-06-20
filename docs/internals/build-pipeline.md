# Build Pipeline

How `agent-sandbox generate` transforms fleet.yaml into Docker build artifacts.

## Overview

The generate command reads configuration, resolves plugins, and writes a complete `.build/` directory containing everything needed for `docker compose up --build`. No compilation happens at generate-time (unless `--dev` mode is used for local development).

## Pipeline Flow

```
fleet.yaml + per-agent agent.yaml → resolve plugins → render templates → .build/
```

Detailed steps:

1. **Load configuration** — Parse fleet.yaml + per-agent agent.yaml configs. Load `.env` for secret resolution.

2. **Resolve core** — Fetch core tarball from GitHub Releases (cached at `~/.agent-sandbox/core/<version>/`). Contains presets, plugins, templates, and pre-built gateway binaries. Falls back to local cache on network failure (60s timeout).

3. **Resolve plugins** — `@builtin/` from core tarball, `./` from local filesystem. Validate options, check dependencies.

4. **Render contributions** — Each plugin's `contributes` fields are rendered as Go templates with access to plugin options and agent context.

5. **Merge contributions** — All plugin contributions are merged (runtime extra_builds, gateway egress rules, volumes, etc.).

6. **Generate Dockerfile** — Combine runtime preset (base image, install commands) with plugin contributions (extra_builds, ports, volumes).

7. **Generate entrypoint.sh** — Pre-entrypoint commands from plugins, then the agent CMD.

8. **Generate gateway config** — `config.yaml` (MITM domains from egress rules with headers, deny_paths, or middlewares; auth headers; DNS; port forwards) and `plugins.yaml` (TypeScript plugin manifest for runtime loading).

9. **Copy gateway binary** — Pre-built `gateway-linux-<arch>` binary from core tarball into `.build/`.

10. **Copy plugin sources** — TypeScript files from `src/` directories copied to `.build/plugins/<name>/`.

11. **Generate docker-compose.yml** — Orchestrates agent + gateway containers with networking, volumes, and depends_on.

12. **Generate .env.example** — Scans all `${VAR}` references across egress headers, plugin options, and plugin-contributed service headers. Writes a sorted, deduplicated `.env.example` to the project root.

## Generated Artifacts

```
.build/
  <agent-name>/
    Dockerfile                ← agent container (preset + plugin contributions)
    entrypoint.sh             ← startup script (pre_entrypoint + CMD)
    gateway/
      gateway-linux-<arch>    ← pre-built binary (from core tarball)
      config.yaml             ← proxy config (MITM domains, auth headers, DNS)
      plugins.yaml            ← TypeScript plugin manifest
      Dockerfile              ← minimal FROM + COPY binary + config
      ca/                     ← generated CA cert/key for MITM
    plugins/
      github-pat/
        src/github-auth.ts    ← TypeScript loaded at gateway runtime
      mcp-oauth/
        src/oauth.ts
        src/login.ts
        src/callback.ts
        src/pkce.ts
  docker-compose.yml          ← single compose file orchestrating all agents
  schema.json
.env.example                  ← all ${VAR} references (project root, not .build/)
```

## Gateway Container

The gateway Dockerfile is minimal — no compilation:

```dockerfile
FROM debian:bookworm-slim
COPY gateway-linux-amd64 /usr/local/bin/gateway
COPY config.yaml /etc/gateway/config.yaml
COPY plugins.yaml /etc/gateway/plugins.yaml
COPY plugins/ /etc/gateway/plugins/
COPY ca/ /etc/gateway/ca/
HEALTHCHECK CMD wget -qO- http://localhost:8080/health || exit 1
CMD ["gateway"]
```

The gateway binary is cross-compiled during the release workflow (CI) for linux/amd64 and linux/arm64. No per-project compilation is needed.

## Agent Container

The agent Dockerfile combines:
- Base image from runtime preset (e.g. `node:22-slim` for codex)
- System packages from preset
- Plugin `extra_builds` (ENV, RUN, COPY lines)
- iptables rules for transparent proxy (NET_ADMIN)
- User creation and permissions
- Entrypoint script

## Docker Layer Cache Optimization

The generator automatically reorders Dockerfile instructions for optimal layer caching. The key insight: heavy tool installations (docker, playwright, nix, python) should not be invalidated when plugin source code changes.

### Generated Dockerfile Structure

```
# 1. Parallel build stages (plugins compile independently)
FROM node:24-slim AS build-plugin-a
...
FROM node:24-slim AS build-plugin-b
...

# 2. Final image — base packages
FROM node:24-slim
RUN apt-get install ...

# 3. EARLY: heavy installs (auto-hoisted, cached)
RUN curl -fsSL https://get.docker.com | sh
RUN npm install -g agent-browser && npx playwright install
RUN <nix install>
RUN apt-get install python3 && pip install ...

# 4. COPY --from (plugin artifacts — changes on code updates)
COPY --from=build-plugin-a /src/dist /opt/plugin-a/dist
COPY --from=build-plugin-b /src/out.tgz /tmp/out.tgz

# 5. LATE: config steps (depend on artifacts or build context)
COPY dorey-home /opt/home-seed/
RUN npm install -g /tmp/out.tgz
RUN echo '{...}' > /opt/plugin-a/config.json
```

When plugin code changes, only sections 4 and 5 rebuild. Section 3 stays cached.

### Auto-Hoist Heuristic

The generator splits `extra_builds` into early (hoistable) and late (must come after COPY --from) using a two-pass approach:

1. **Pass 1:** COPY instructions → late. Steps referencing build stage artifact paths → late. Everything else → early candidate.
2. **Pass 2:** Collect destination paths from late COPY instructions. Re-check early candidates — any that reference those paths also go late.

Artifact paths include both direct paths (`/opt/foo/dist`) and their parent directories (`/opt/foo/`) to catch config writes to the same directory.

No plugin.yaml changes needed — existing plugins benefit automatically.

### Config Fingerprint

Sidecar containers include a `com.agent-sandbox.config-hash` label containing a SHA256 of their entire service definition (serialized as JSON). When any config field changes (environment, cap_add, security_opt, volumes, image, etc.), the hash changes and `docker compose up -d` recreates the container.

The fingerprint is computed last, after all service mutations (gateway routing injection, capability additions).

## Secret Resolution

Secrets in plugin options (`${ENV_VAR}`) are resolved at generate-time from:
1. Project `.env` file
2. Shell environment

Resolved values are baked into gateway `config.yaml` (for auth-header injection) and available to TypeScript plugins via the `options` parameter.

## CA Lifecycle

If any egress rule requires MITM (has `headers`, `deny_paths`, or `middlewares`), the generator:
1. Configures the gateway to perform MITM on those domains
2. The gateway generates/reuses a CA keypair at runtime (persisted on shared volume)
3. The agent container trusts this CA (injected into system trust store at boot)
4. CA persists across gateway restarts (365-day validity, auto-regenerated if expired)

## Dev Mode (`--dev`)

When running from the source repo with `--dev`:
- Skips GitHub Releases fetch
- Uses plugins directly from `core/plugins/`
- Cross-compiles the gateway binary for the Docker daemon's architecture
- Templates loaded from local filesystem instead of embedded FS

```bash
agent-sandbox --dev -C examples/local-coding generate
```

## Release Model

The `core-release.yml` workflow (triggered on `v*` tags) produces platform tarballs:

```
agent-sandbox-core-v1.31.1-darwin-arm64.tar.gz
agent-sandbox-core-v1.31.1-darwin-amd64.tar.gz
agent-sandbox-core-v1.31.1-linux-arm64.tar.gz
agent-sandbox-core-v1.31.1-linux-amd64.tar.gz
```

Each tarball contains:
- `agent-sandbox-core` — host CLI binary
- `presets/` — runtime YAML files
- `plugins/` — plugin YAML + TypeScript sources
- `templates/` — Go text/template `.tmpl` files
- `gateway/bin/` — pre-built gateway binaries (linux/amd64 + linux/arm64)
- `sdk/` — Go SDK for gateway extensions

The shim downloads and caches the appropriate platform tarball on first run.
