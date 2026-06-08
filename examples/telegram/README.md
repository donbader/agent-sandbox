# Telegram Bot Example

A sandboxed codex agent accessible via Telegram. Uses ACP (Agent Client Protocol) over stdio to manage codex, with a sidecar adapter bridging Telegram messages via WebSocket.

## Architecture

```
Telegram API (long-polling via grammY)
     |
  telegram-adapter (sidecar container)
     | WebSocket (ws://agent:3100/acp)
  agent-manager (entrypoint, inside agent container)
     | ACP over stdio
  codex-acp (child process)
     | HTTPS (transparent proxy via iptables DNAT)
  gateway (MITM proxy container)
     | HTTPS (real credentials injected)
  LLM API (agent-gateway.stx-ai.net)
```

**agent-manager** — spawns codex-acp, performs ACP handshake (initialize + authenticate), exposes an ACP endpoint over HTTP/WebSocket on port 3100.

**telegram-adapter** — connects to agent-manager via WebSocket, receives Telegram messages via grammY long-polling, forwards them as ACP prompts.

**gateway** — transparent HTTPS proxy that MITMs traffic, injects auth headers, rewrites credentials.

## Startup Sequence

1. `agent-sandbox generate` reads `agent.yaml`, loads `.env`, generates Dockerfile + compose + gateway config
2. `agent-sandbox compose up --build` builds and starts all containers
3. Gateway starts first (healthcheck), agent container waits for it
4. Agent container sets up iptables DNAT redirect (transparent proxy), installs CA cert
5. Agent-manager starts, spawns codex-acp, performs ACP init + auth handshake
6. Agent-manager exposes HTTP/WS endpoint on port 3100 (healthcheck)
7. Telegram-adapter starts (depends_on agent healthy), connects to `ws://agent:3100/acp`
8. User messages flow: Telegram -> adapter -> agent-manager -> codex-acp -> gateway -> LLM API -> response back

## Setup

```bash
cd examples/telegram

# Create .env with your secrets
cat > .env << 'ENVEOF'
TELEGRAM_BOT_TOKEN=your-bot-token
TELEGRAM_USERNAME=your-telegram-username
STX_LLM_GATEWAY_API_KEY=your-api-key
ENVEOF

# Generate and run
agent-sandbox generate
agent-sandbox compose up --build -d
agent-sandbox compose logs -f
```

### Required Environment Variables

| Variable | Description |
|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Telegram bot token (from @BotFather) |
| `TELEGRAM_USERNAME` | Allowed Telegram username (without @) |
| `STX_LLM_GATEWAY_API_KEY` | API key for the LLM gateway |

## Configuration

- `agent.yaml` — agent config (runtime, gateway, plugins, adapter)
- `plugins/telegram/` — local plugin (telegram-adapter sidecar definition)

### agent.yaml

```yaml
name: telegram-agent
log_level: debug

runtime:
  image: "@builtin/codex"
  extra_builds:
    - "RUN npm install -g @agentclientprotocol/codex-acp"
    - "ENV OPENAI_API_KEY=gateway-managed"
  entrypoint: ["node", "/opt/agent-manager/dist/index.js"]

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

  - plugin: "@builtin/agent-manager-acp"
    options:
      acp_command: ["codex-acp"]
      acp_install: "npm install -g @zed-industries/codex-acp@0.15.0"

  - plugin: ./plugins/telegram
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      allowed_users:
        - "@${TELEGRAM_USERNAME}"
```

## How It Works

The agent container runs `agent-manager` as its entrypoint. Agent-manager spawns `codex-acp` as a child process and communicates via ACP over stdio. After completing the ACP handshake, it exposes an HTTP/WebSocket server on port 3100.

The telegram-adapter runs as a separate sidecar container. It connects to the agent-manager's WebSocket endpoint and polls Telegram for messages using grammY. When a message arrives from an allowed user, the adapter sends it as an ACP prompt over the WebSocket. Responses stream back the same way.

All outbound HTTPS from the agent container is transparently redirected through the gateway via iptables DNAT. The gateway MITMs connections and injects real credentials (LLM API key) so the agent never sees actual secrets.
