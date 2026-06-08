# Configuration

## Minimal Example

```yaml
# yaml-language-server: $schema=.build/schema.json
name: coder
core_version: latest

runtime:
  image: "@builtin/codex"

gateway:
  services:
    - url: https://api.openai.com
      headers:
        Authorization: Bearer ${OPENAI_API_KEY}
```

This is a complete, working config. The agent uses the codex preset, and the gateway injects your API key into all requests to `api.openai.com`.

## Editor Autocompletion

Running `agent-sandbox generate` produces `.build/schema.json`. Add this comment at the top of your config for VS Code autocompletion (requires the [YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)):

```yaml
# yaml-language-server: $schema=.build/schema.json
```

You need to run `agent-sandbox generate` at least once before the schema file exists.

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
  volumes:                # optional — named or bind mount volumes
    - "data-vol:/home/agent/data"
    - "./local:/home/agent/local"

gateway:
  public_url: string      # optional — public URL for OAuth callbacks and webhook receivers
  services:               # optional — external services proxied through the gateway
    - url: https://api.example.com
      network: string     # optional — compose network to attach
      headers:            # optional — injected on every proxied request
        Authorization: Bearer ${ENV_VAR}
      middlewares:        # optional — custom Go middleware
        - custom: ./path/to/middleware.go

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

Secrets are resolved at generate time and baked into the gateway binary. They never enter the agent container's environment. The `audit` command verifies this.

## Container Runtime

By default, agent-sandbox uses Docker. To use Podman:

```yaml
runtime_engine: podman
```

Or set the environment variable (takes priority):

```bash
AGENT_SANDBOX_RUNTIME=podman agent-sandbox compose up --build
```

## Gateway Services

Services declare what external endpoints the agent can reach through the gateway:

```yaml
gateway:
  services:
    # External HTTPS — gateway MITMs and injects credentials
    - url: https://api.openai.com
      headers:
        Authorization: Bearer ${OPENAI_API_KEY}

    # Internal sidecar on compose network
    - url: sidecar:8080
      headers:
        X-Token: ${SIDECAR_TOKEN}
```

For HTTPS URLs, the gateway terminates TLS (MITM), injects headers, then forwards to the real server. The agent never sees the real credentials.

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

Plugin references:
- `@builtin/name` — bundled plugins (fetched from core releases)
- `./path` — local plugin in your project directory

See [Plugins](plugins.md) for the full catalog.

## Fleet Mode (Multi-Agent)

For multiple agents, use `fleet.yaml` instead of `agent.yaml`:

```yaml
# fleet.yaml
agents:
  - agent-001
  - agent-002

shared:
  gateway:
    services:
      - url: https://agent-gateway.stx-ai.net
        headers:
          Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}
  installations:
    - plugin: "@builtin/github-pat"
      options:
        token: "${GITHUB_PAT}"
```

Each agent directory contains its own `agent.yaml`:

```
my-fleet/
  fleet.yaml
  .env
  agent-001/
    agent.yaml
    home/
  agent-002/
    agent.yaml
    home/
```

**Merge rules:**
- `shared.gateway.services` merges into each agent (same URL → per-agent wins)
- `shared.installations` merges into each agent (same plugin → per-agent wins)
- Each agent gets its own gateway container with independently compiled middleware

See [Fleet Mode Guide](guides/fleet-mode.md) for a complete walkthrough.

## Project Structure

```
my-agent/
  agent.yaml          ← configuration
  .env                ← secrets (gitignored)
  home/               ← files to copy into /home/agent (via home-override plugin)
  .build/             ← generated artifacts (gitignored)
    Dockerfile
    docker-compose.yml
    gateway-src/
    schema.json
```
