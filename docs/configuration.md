# Configuration

## Minimal Example

```yaml
# yaml-language-server: $schema=.build/schema.json
name: coder
core_version: latest

runtime:
  image: "@builtin/codex"

gateway:
  egress:
    - hosts: ["api.openai.com"]
      headers:
        Authorization: "Bearer ${OPENAI_API_KEY}"
    - hosts: ["*"]  # allow all other traffic
```

This is a complete, working config. The agent uses the codex preset, and the gateway injects your API key into all requests to `api.openai.com`. Rules are evaluated in order — first match wins. A `hosts: ["*"]` catch-all at the end allows all remaining traffic (omit it for implicit-deny mode).

## Editor Autocompletion

Running `agent-sandbox generate` produces `.build/schema.json`. Add this comment at the top of your config for VS Code autocompletion (requires the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)):

```yaml
# yaml-language-server: $schema=.build/schema.json
```

You need to run `agent-sandbox generate` at least once before the schema file exists.

## core_version

Required. Specifies which core release to use for generation and runtime.

```yaml
core_version: v0.13.0   # pin to specific version (recommended for teams)
core_version: latest     # always use newest (re-resolves on each run)
```

The shim downloads and caches the specified core version automatically on first use.
Pin to a specific version for reproducible builds across team members.

## Full Schema

```yaml
name: string              # required — agent instance name
core_version: string      # required — "latest" or semver tag (e.g. "v1.0.0")
log_level: string         # optional — "info" (default) or "debug"
runtime_engine: string    # optional — "docker" (default) or "podman"

runtime:
  image: string           # required — "@builtin/codex", "@builtin/claude-code", "@builtin/pi", or any Docker image
  extra_builds:           # optional — additional Dockerfile instructions
    - "RUN apt-get install -y ripgrep"
    - "ENV MY_VAR=value"
  entrypoint:             # optional — override container CMD
    - "my-binary"
    - "--flag"
  namespaced_volumes:    # optional — named volumes, auto-prefixed with {agentName}-
    - "data-vol:/home/agent/data"
  raw_volumes:            # optional — bind mounts or volumes used as-is
    - "./local:/home/agent/local"

gateway:
  egress:                 # optional — ordered egress rules (first match wins, no match = deny)
    - hosts:              # required — list of host patterns to match
        - "api.example.com"
      headers:            # optional — injected on every proxied request (implies MITM + allow)
        Authorization: "Bearer ${ENV_VAR}"
      middlewares:        # optional — TypeScript middleware scripts
        - ./path/to/middleware.ts
    - hosts: ["internal.bad.com"]
      deny: true          # optional — explicitly block matching hosts
    - hosts: ["*"]        # optional — catch-all allow (permissive mode)
  # services:            # DEPRECATED — still works, but use egress instead

installations:            # optional — plugins to install
  - plugin: "@builtin/github-pat"
    options:
      token: "${GITHUB_PAT}"
```

## Secrets (`.env` file)

Credentials are stored in a `.env` file in the project root. The `${VAR}` syntax in `headers` and plugin `options` references these values:

```bash
# .env
OPENAI_API_KEY=sk-xxxx
GITHUB_PAT=ghp_xxxx
```

Secrets are resolved at generate time and passed to the gateway at runtime via options. They never enter the agent container's environment. The `audit` command verifies this.

The `generate` command automatically produces a `.env.example` file listing all `${VAR}` references found across fleet config, agent configs, and plugin options. Copy it to `.env` and fill in values:

```bash
cp .env.example .env
# Edit .env with real values
```

## Container Runtime

By default, agent-sandbox uses Docker. To use Podman:

```yaml
runtime_engine: podman
```

Or set the environment variable (takes priority):

```bash
AGENT_SANDBOX_RUNTIME=podman agent-sandbox compose up --build
```

## Gateway Egress Rules

Egress rules declare what external endpoints the agent can reach through the gateway and how credentials are injected. Rules are evaluated in order — first match wins. If no rule matches, traffic is implicitly denied.

```yaml
gateway:
  egress:
    # External HTTPS — gateway MITMs and injects credentials
    - hosts: ["api.openai.com"]
      headers:
        Authorization: "Bearer ${OPENAI_API_KEY}"

    # Internal sidecar on compose network
    - hosts: ["sidecar:8080"]
      headers:
        X-Token: "${SIDECAR_TOKEN}"

    # Block a specific host
    - hosts: ["evil.example.com"]
      deny: true

    # Allow everything else (omit for strict deny-by-default)
    - hosts: ["*"]
```

For HTTPS hosts with `headers`, the gateway terminates TLS (MITM), injects headers, then forwards to the real server. The agent never sees the real credentials.

Rules can also use `deny_paths: [...]` to block specific URL paths on otherwise-allowed hosts.

Rules can also use `deny_graphql:` to block specific GraphQL mutations — useful when `deny_paths` can't distinguish operations since all GraphQL traffic goes to a single `POST /graphql` endpoint:

```yaml
gateway:
  egress:
    - hosts: ["api.github.com"]
      headers:
        Authorization: "Bearer ${GITHUB_PAT}"
      deny_graphql:
        mutations:
          - "mergePullRequest"
          - "deleteBranch"
```

The gateway inspects POST requests to paths containing `graphql`, extracts all candidate mutation names (from `operationName`, the named operation, and the first field inside the mutation body), and returns 403 if any match the deny list. Matching is case-insensitive. Requires MITM (auto-enabled).

> **Deprecation note:** `gateway.services` is deprecated but still supported for backward compatibility. It is converted to equivalent egress rules internally. New configs should use `gateway.egress`. See [Gateway Egress Reference](reference/gateway-egress.md) for full details.

## Plugins (installations)

Plugins add capabilities to the agent. Each entry needs a `plugin` reference and optional `options`:

```yaml
installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "${GITHUB_PAT}"

  - plugin: "@builtin/home-override"
    options:
      home_directory: "./home"
      volume: true

  - plugin: "@builtin/ssh"
    options:
      port: 2222
      authorized_keys: "./ssh_key.pub"
```

> **Note:** The SSH plugin installs the authorized key outside `/home/agent`, so it works correctly even when `home-override` mounts a volume over the home directory.

Plugin references:
- `@builtin/name` — bundled plugins (fetched from core releases)
- `./path` — local plugin in your project directory

Path values in plugin options:
- `@fleet/path` — relative to the project root (fleet.yaml directory). Use this for all file/directory references in fleet mode. See [Fleet Mode](guides/fleet-mode.md#fleet-path-prefix-fleet).

Plugin option types:
- `type: string` — any string value. `@fleet/` prefix is allowed. Bare `..` is blocked.
- `type: project-path` — **must** use `@fleet/` prefix. Enforced at validation time. Use this for options that reference files or directories within the project.
- `type: integer` — numeric value
- `type: boolean` — true/false
- `type: object` — nested structure

See [Plugins](plugins.md) for the full catalog.

## plugin.yaml Schema

Each plugin is defined by a `plugin.yaml` file:

```yaml
name: my-plugin
description: What this plugin does

contributes:
  gateway:
    egress:
      - hosts: ["api.example.com"]
        middlewares:
          - "./src/my-middleware.ts"    # TypeScript middleware loaded at gateway runtime
    routes:
      - path: "/hook"
        handler: "./src/hook-handler.ts"    # TypeScript route handler
  runtime:
    environment:
      MY_VAR: "value"                  # environment variables set in agent container
    namespaced_volumes:
      - "my-data:/data/my-plugin"
```

### Key Fields

| Field | Type | Description |
|-------|------|-------------|
| `contributes.gateway.egress[].hosts` | list | Host patterns this egress rule matches |
| `contributes.gateway.egress[].middlewares` | list | TypeScript middleware script paths (relative to plugin dir) |
| `contributes.gateway.routes[].path` | string | HTTP path the route handles |
| `contributes.gateway.routes[].handler` | string | Path to TypeScript handler file (relative to plugin dir) |
| `contributes.runtime.environment` | map | Environment variables injected into the agent container |
| `contributes.runtime.namespaced_volumes` | list | Per-agent volumes (auto-prefixed with `{agentName}-`) |
| `contributes.runtime.raw_volumes` | list | Volumes used as-is (bind mounts, fleet-shared volumes) |

## Fleet Structure (fleet.yaml)

Every project uses `fleet.yaml` at the root, even for a single agent. `agent-sandbox init` always creates this structure.

```yaml
# fleet.yaml
agents:
  - coder

shared:
  gateway:
    egress:
      - hosts: ["api.openai.com"]
        headers:
          Authorization: "Bearer ${OPENAI_API_KEY}"
      - hosts: ["*"]
  installations:
    - plugin: "@builtin/github-pat"
      options:
        token: "${GITHUB_PAT}"
```

Each agent directory contains its own `agent.yaml`:

```
my-project/
  fleet.yaml
  .env
  coder/
    agent.yaml
    home/
```

For multiple agents, add entries to `agents:`:

```yaml
agents:
  - agent-001
  - agent-002
```

**Merge rules:**
- `shared.gateway.egress` merges into each agent (shared rules prepended; per-agent rules take priority for same host)
- `shared.installations` merges into each agent (same plugin → per-agent wins)
- Each agent gets its own gateway container with independently loaded middleware

See [Multi-Agent Guide](guides/fleet-mode.md) for a complete walkthrough.

## Project Structure

```
my-project/
  fleet.yaml            ← project configuration (lists agents + shared config)
  .env                  ← secrets (gitignored)
  coder/                ← agent subdirectory
    agent.yaml          ← per-agent configuration
    home/               ← files to copy into /home/agent (via home-override plugin)
  .build/               ← generated artifacts (gitignored)
    docker-compose.yml  ← single compose file for all agents
    coder/              ← per-agent build artifacts
      Dockerfile
      entrypoint.sh
      gateway-config/
      schema.json
```
