# Runtime Presets

Runtime presets define the agent's container environment: base image, packages to install, and the default startup command. They are pure data (YAML) — no Go code. Presets ship embedded in the CLI binary and are read at generate time to produce a Dockerfile.

## Usage

Reference a preset by name in `agent.yaml`:

```yaml
name: my-agent
runtime: codex
```

`agent-sandbox generate` reads the preset and generates the Dockerfile automatically.

## Available Presets

| Name | Base Image | Agent Package | ACP Binary | Use Case |
|------|-----------|---------------|------------|----------|
| `codex` | node:24-slim | `@openai/codex` | `codex-acp` | OpenAI Codex agents |
| `claude-code` | node:24-slim | `@anthropic-ai/claude-code` | `claude-agent-acp` | Anthropic Claude Code agents |
| `pi` | node:24-slim | `@earendil-works/pi-coding-agent` | `pi-acp` | Pi coding agents |

`acp_cmd` is the ACP server binary. When a channel manager is active, it replaces the default `sleep infinity` CMD and spawns the agent via ACP.

## Preset File Format (`runtime.yaml`)

```yaml
name: codex                          # matches runtime: in agent.yaml
base_image: node:24-slim             # Docker base image
install:
  - apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*
  - --mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0 @zed-industries/codex-acp@0.15.0
cmd: ["sleep", "infinity"]           # default CMD (no channel manager active)
acp_cmd: ["codex-acp"]              # ACP server binary (used when channel manager is active)
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Identifier — must match the `runtime:` value in agent.yaml |
| `base_image` | yes | Docker base image |
| `install` | yes | Shell commands run as `RUN` steps during Docker build |
| `cmd` | yes | Default CMD when no channel manager is active |
| `acp_cmd` | no | ACP server command — used by channel manager to spawn the agent |

Each `install` entry becomes a separate `RUN` statement. Prefix a line with `--mount=...` to pass BuildKit cache mount flags.

## Creating a New Preset

1. Create `core/presets/<name>/runtime.yaml` following the format above.
2. The CLI embeds all files under `core/presets/` at compile time — rebuild the CLI to pick up the new preset.
3. Reference it with `runtime: <name>` in agent.yaml.

For one-off agents that don't need a reusable preset, define the runtime inline in agent.yaml instead:

```yaml
name: my-agent
runtime:
  base_image: python:3.12-slim
  install:
    - pip install my-agent-cli
  cmd: ["my-agent-cli", "--headless"]
```
