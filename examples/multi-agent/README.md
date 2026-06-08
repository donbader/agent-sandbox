# Multi-Agent Fleet Example

Two sandboxed coding agents (codex + claude-code) sharing gateway credentials, each with independent home directories and workspace volumes.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ fleet.yaml                                                  │
│                                                             │
│  ┌─────────────────────┐       ┌─────────────────────┐      │
│  │ agent-001-gateway   │       │ agent-002-gateway   │      │
│  │  MITM + cred inject │       │  MITM + cred inject │      │
│  └────────┬────────────┘       └────────┬────────────┘      │
│           │ DNAT                        │ DNAT              │
│  ┌────────▼────────────┐       ┌────────▼────────────┐      │
│  │ agent-001           │       │ agent-002           │      │
│  │  @builtin/codex     │       │  @builtin/claude-   │      │
│  │  persistent home    │       │  code               │      │
│  │  workspace volume   │       │  persistent home    │      │
│  └─────────────────────┘       └─────────────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

Each agent gets its own gateway container with independently compiled middleware. Shared credentials (LLM API key, GitHub PAT) are declared once in `fleet.yaml` and distributed to each gateway at generate time.

## Setup

```bash
cd examples/multi-agent

# Create .env from the example
cp .env.example .env
# Fill in your credentials:
#   STX_LLM_GATEWAY_API_KEY=your-api-key
#   GITHUB_PAT=ghp_xxxx

# Generate and run
agent-sandbox generate
agent-sandbox compose up --build -d

# Verify security contract
agent-sandbox audit
```

## Usage

Exec into either agent:

```bash
# Agent 001 (codex)
agent-sandbox compose exec -it --user agent agent-001 codex

# Agent 002 (claude-code)
agent-sandbox compose exec -it --user agent agent-002 claude-code
```

## Configuration

### fleet.yaml

```yaml
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

The `shared` block is merged into each agent's config. Per-agent `agent.yaml` can override or extend.

### Per-agent config (agent-001/agent.yaml)

```yaml
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

### Home directories

Each agent has a `home/` directory with pre-seeded config (provider settings, model catalog). The `@builtin/home-override` plugin with `volume: true` persists these across container restarts.

## Environment Variables

| Variable                  | Description                           |
| ------------------------- | ------------------------------------- |
| `STX_LLM_GATEWAY_API_KEY` | API key for the LLM gateway (shared)  |
| `GITHUB_PAT`              | GitHub Personal Access Token (shared) |
