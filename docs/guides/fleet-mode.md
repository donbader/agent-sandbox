# Multi-Agent Projects

Every agent-sandbox project uses a unified structure: a `fleet.yaml` at the project root listing agents, with each agent in its own subdirectory containing an `agent.yaml`. This applies whether you have one agent or many.

This guide covers working with multiple agents — sharing config, per-agent overrides, and managing the fleet as a whole.

## Project Structure

```
my-fleet/
  fleet.yaml              ← declares agents and shared config
  .env                    ← shared secrets
  agent-001/
    agent.yaml            ← per-agent config
    home/                 ← per-agent home directory (optional)
  agent-002/
    agent.yaml
    home/
```

## Configuration

### fleet.yaml

```yaml
# yaml-language-server: $schema=.build/fleet-schema.json
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

### Per-agent agent.yaml

```yaml
# yaml-language-server: $schema=../.build/schema.json
name: agent-001
core_version: latest

runtime:
  image: "@builtin/codex"
  volumes:
    - "agent-001-data:/home/agent/workspace"

installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "./home"
      volume: true
```

## Merge Rules

The `shared` block merges into each agent's config automatically:

| What | Rule |
|------|------|
| `shared.gateway.services` | Prepended to per-agent services. Same URL → per-agent wins. |
| `shared.installations` | Prepended to per-agent installations. Same plugin → per-agent wins. |

This lets you declare credentials once and override per-agent when needed.

## Generated Output

```
.build/
  docker-compose.yml      ← unified compose for all agents
  schema.json
  fleet-schema.json
  agent-001/
    Dockerfile
    entrypoint.sh
    config.yaml
    gateway-src/          ← independent gateway with its own middleware
  agent-002/
    Dockerfile
    entrypoint.sh
    config.yaml
    gateway-src/
```

Each agent gets its own gateway container with independently compiled middleware. This means agent-001's gateway only has the middleware for agent-001's services.

## Commands

```bash
# Generate all agents
agent-sandbox generate

# Start everything
agent-sandbox compose up --build -d

# Audit all agents
agent-sandbox audit

# Exec into a specific agent
agent-sandbox compose exec -it --user agent agent-001 codex
agent-sandbox compose exec -it --user agent agent-002 claude-code

# View logs for one agent
agent-sandbox compose logs agent-001

# Tear down
agent-sandbox compose down -v
```

## Tips

- Use `{{ .agent.name }}` in plugin templates to avoid volume name collisions across agents
- The compose project name is derived from the folder name (not hardcoded)
- Each agent's `home/` directory is independent — seed it with runtime-specific config (e.g., codex provider settings)
- The `.env` file is shared across all agents; per-agent secrets aren't supported yet

## Adding a New Agent

To add another agent to an existing project:

1. Create a new subdirectory with an `agent.yaml` inside
2. Add the directory name to the `agents` list in `fleet.yaml`
3. Run `agent-sandbox generate` to regenerate build artifacts

The new agent inherits all `shared` config automatically.

## Example

See [`examples/multi-agent/`](../../examples/multi-agent/) for a working two-agent fleet with codex and claude-code.
