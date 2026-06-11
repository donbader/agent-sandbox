# Telegram Bot Example

A sandboxed codex agent accessible via Telegram, using OpenACP as the channel layer.

## Architecture

```
Telegram API (forum Topics)
     |
  OpenACP (entrypoint, manages Telegram channel)
     | ACP over stdio
  agent-manager (stdio relay)
     | ACP over stdio
  codex-acp (child process)
     | HTTPS (transparent proxy via iptables DNAT)
  gateway (MITM proxy container)
     | HTTPS (real credentials injected)
  LLM API (agent-gateway.stx-ai.net)
```

**OpenACP** — entrypoint process. Manages the Telegram channel via forum Topics, spawns agent-manager over stdio using the ACP protocol.

**agent-manager** — spawned by OpenACP via stdio. Spawns codex-acp (or any other ACP agent) as a child process and relays ACP messages.

**gateway** — transparent HTTPS proxy that MITMs traffic, injects auth headers, rewrites credentials (e.g. Telegram bot token rewriting).

## Project Structure

```
examples/telegram/
  fleet.yaml              ← declares the "telegram-agent" agent
  .env                    ← secrets (bot token, API key)
  telegram-agent/
    agent.yaml            ← agent config
    home/                 ← pre-seeded home directory
  plugins/
    telegram/             ← local plugin (bot token rewriting)
    open-acp/             ← local plugin (OpenACP entrypoint)
```

## Startup Sequence

1. `agent-sandbox generate` reads `fleet.yaml` + `telegram-agent/agent.yaml`, loads `.env`, generates Dockerfile + compose + gateway config
2. `agent-sandbox compose up --build` builds and starts all containers
3. Gateway starts first (healthcheck), agent container waits for it
4. Agent container sets up iptables DNAT redirect (transparent proxy), installs CA cert
5. OpenACP starts as entrypoint, spawns agent-manager via stdio (ACP)
6. agent-manager spawns codex-acp via stdio, performs ACP init + auth handshake
7. User messages flow: Telegram → OpenACP → agent-manager → codex-acp → gateway → LLM API → response back

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

- `fleet.yaml` — declares the agent(s) in this project
- `telegram-agent/agent.yaml` — agent config (runtime, gateway, plugins)
- `plugins/telegram/` — local plugin (gateway middleware for bot token rewriting)

### telegram-agent/agent.yaml

```yaml
name: telegram-agent
log_level: debug

runtime:
  image: "@builtin/codex"
  extra_builds:
    - "RUN npm install -g @agentclientprotocol/codex-acp"
    - "ENV OPENAI_API_KEY=gateway-managed"

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

  - plugin: ./plugins/open-acp
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      allowed_users:
        - "@${TELEGRAM_USERNAME}"

  - plugin: ./plugins/telegram
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
```

## How It Works

OpenACP runs as the container entrypoint and manages the Telegram channel using forum Topics (one topic per conversation). It spawns agent-manager via stdio using the ACP protocol.

agent-manager in turn spawns `codex-acp` as a child process and relays ACP messages between OpenACP and the agent. The entire chain is stdio-based — no WebSocket or HTTP between components.

All outbound HTTPS from the agent container is transparently redirected through the gateway via iptables DNAT. The gateway MITMs connections and injects real credentials (LLM API key, Telegram bot token rewriting) so the agent never sees actual secrets.
