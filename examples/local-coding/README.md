# Local Coding Example

A sandboxed AI coding agent for interactive local development. LLM API access is routed through the gateway for credential injection — the agent never sees your real API key.

## Architecture

```
LLM API (agent-gateway.stx-ai.net)
     ↕ (real API key injected by gateway)
  Gateway (MITM for agent-gateway.stx-ai.net, mcp.notion.com)
     ↕ (iptables DNAT, transparent to agent)
  Agent (claude-code)
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
agent-sandbox -C examples/local-coding compose exec -it --user agent coder bash
claude
```

> **Note:** `--user agent` is required. Without it, exec runs as root and the agent won't find its config.

## Project Structure

```
examples/local-coding/
  fleet.yaml              ← declares the "coder" agent
  .env                    ← secrets (STX_LLM_GATEWAY_API_KEY)
  coder/
    agent.yaml            ← agent config
    home/                 ← pre-seeded home directory
```

## Configuration

### fleet.yaml

```yaml
agents:
  - coder
```

### coder/agent.yaml

```yaml
name: coder
core_version: latest
log_level: debug

runtime:
  image: "@builtin/claude-code"
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
  - plugin: "@builtin/mcp-oauth"
    options:
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
```

### What each piece does

| Config | Purpose |
|--------|---------|
| `runtime.image` | Uses the claude-code preset (node:24-slim + Claude Code CLI) |
| `runtime.entrypoint` | Keeps container alive for interactive exec |
| `gateway.services` | Routes LLM traffic through gateway, injects real API key |
| `@builtin/home-override` | Copies `./home/` into `/home/agent/`, persists via Docker volume |
| `@builtin/mcp-oauth` | OAuth token injection for MCP servers (Notion, etc.) |

### Home directory

The `coder/home/` directory contains pre-seeded agent configuration:

```
coder/home/
  .claude.json          ← MCP server config (Notion)
  .claude/
    settings.json       ← permissions, environment
  .codex/
    config.toml         ← codex provider config (alternative runtime)
    models.json         ← available models
```

With `volume: true`, the home directory persists across container restarts (shell history, auth tokens, tool caches survive).

## Runtime Options

### Claude Code (default)

The default runtime. MCP tools work end-to-end — the agent can use Notion and other MCP servers during conversations.

### Codex (alternative)

Switch to codex by changing `runtime.image` to `@builtin/codex`. Codex connects to the gateway and OAuth works, but **MCP tools are not included in LLM requests** due to an upstream bug in codex v0.139.0. LLM calls work fine for non-MCP tasks.

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

> **Note:** No Notion developer app or API key needed. The plugin uses OAuth Dynamic Client Registration (RFC 7591) — credentials are obtained automatically. Tokens are persisted across container restarts and auto-refresh when expired.
