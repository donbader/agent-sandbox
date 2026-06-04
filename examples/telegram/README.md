# Telegram Bot Example

A sandboxed codex agent accessible via Telegram. The channel-manager bridges Telegram messages to codex using the ACP (Agent Client Protocol).

## Architecture

```
Telegram API (api.telegram.org)
     ↕ (real bot token injected by gateway)
  Gateway (MITM for api.telegram.org + agent-gateway.stx-ai.net)
     ↕
  Agent Container
    └─ channel-manager (entrypoint)
         └─ codex (child process, ACP stdin/stdout)
```

The channel-manager:
1. Receives Telegram updates via long-polling (using a dummy bot token)
2. Routes messages to codex via ACP protocol (stdin/stdout subprocess)
3. Streams codex responses back to Telegram

The gateway transparently rewrites the dummy token to the real one in all api.telegram.org requests.

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

## Configuration

- `agent.yaml` — agent config (runtime, gateway, plugins)
- `channel-manager-config.json` — channel-manager settings (ACP command, working directory, access control)
- `channel-manager/` — channel-manager TypeScript source (builds inside the agent image)
- `plugins/telegram/` — local plugin (gateway middleware for token rewrite)

## Customization

### Change the agent command

Edit `channel-manager-config.json`:
```json
{
  "acp_command": ["codex", "--full-auto"],
  "cwd": "/home/agent/workspace"
}
```

### Access control

Edit `channel-manager-config.json` to restrict who can interact:
```json
{
  "access_control": {
    "allowed_users": ["@yourusername"],
    "require_mention": false
  }
}
```

## How It Works

1. `agent-sandbox generate` produces `.build/` with Dockerfile, compose, gateway config
2. The agent Dockerfile builds the channel-manager from source and installs codex
3. On startup, the entrypoint sets up iptables DNAT → gateway for transparent HTTPS proxying
4. The channel-manager starts, spawns codex as a subprocess, and begins polling Telegram
5. All HTTPS traffic from the agent (including Telegram API calls) flows through the gateway
6. The gateway MITM intercepts api.telegram.org and rewrites the dummy token to the real one
