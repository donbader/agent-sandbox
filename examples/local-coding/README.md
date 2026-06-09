# Local Coding Example

A sandboxed codex agent for interactive local coding. LLM API access is routed through the gateway for credential injection — the agent never sees your real API key.

## Architecture

```
LLM API (agent-gateway.stx-ai.net)
     ↕ (real API key injected by gateway)
  Gateway (MITM for agent-gateway.stx-ai.net)
     ↕ (iptables DNAT, transparent to agent)
  Agent (codex with dummy bearer token)
```

## Setup

```bash
cd examples/local-coding

# Create .env from the example
cp .env.example .env
# Fill in: STX_LLM_GATEWAY_API_KEY=your-api-key

# Generate and run
agent-sandbox generate
agent-sandbox compose up --build -d
```

## Usage

Exec into the agent container as the `agent` user:

```bash
agent-sandbox -C examples/local-coding compose exec -it --user agent coder codex
```

> **Note:** `--user agent` is required. Without it, exec runs as root and codex won't find its config at `/home/agent/.codex/`.

## Configuration

### agent.yaml

```yaml
name: coder
core_version: latest
log_level: debug

runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]

gateway:
  services:
    - url: https://agent-gateway.stx-ai.net
      headers:
        Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}

installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "./home"
      volume: true
```

### What each piece does

| Config | Purpose |
|--------|---------|
| `runtime.image` | Uses the codex preset (node:24-slim + codex CLI) |
| `runtime.entrypoint` | Keeps container alive for interactive exec |
| `gateway.services` | Routes LLM traffic through gateway, injects real API key |
| `@builtin/home-override` | Copies `./home/` into `/home/agent/`, persists via Docker volume |

### Home directory

The `home/` directory contains pre-seeded codex configuration:

```
home/
  .codex/
    config.toml       ← provider + API base URL pointing to gateway
    models.json       ← available models
```

With `volume: true`, the home directory persists across container restarts (shell history, auth tokens, tool caches survive).

## Environment Variables

| Variable | Description |
|----------|-------------|
| `STX_LLM_GATEWAY_API_KEY` | API key for the LLM gateway |

## Notion MCP Integration

This example includes the `mcp-oauth` plugin configured for Notion. On first use:

1. Start the sandbox:
   ```bash
   agent-sandbox -C examples/local-coding generate
   agent-sandbox -C examples/local-coding compose up --build -d
   ```

2. Initiate login for Notion:
   ```bash
   curl $(agent-sandbox -C examples/local-coding gateway-url)/plugins/mcp-oauth/login/notion
   ```
   This returns a JSON response with an `authorize_url`.

3. Open the `authorize_url` in your browser, log in to Notion, and authorize access.

4. The browser redirects back to the gateway — you'll see "Authorization successful".

5. Subsequent requests to Notion are authenticated transparently. No restart needed.

> **Note:** No Notion developer app or API key needed. The plugin uses OAuth Dynamic Client Registration (RFC 7591) — credentials are obtained automatically.
