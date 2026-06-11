# Telegram Channel Setup Guide

The Telegram channel lets you interact with your sandboxed AI agent through a Telegram bot. Messages flow from Telegram into the agent container via the ACP (Agent Client Protocol), and responses stream back to your chat. The agent never sees your real bot token — the gateway handles credential injection transparently.

## Architecture

```
Telegram (your chat)
     │
  telegram-adapter (sidecar container, grammY bot)
     │ ACP over WebSocket
  agent-manager (inside agent container)
     │ ACP over stdio
  codex-acp (or any ACP-compatible agent)
     │ HTTPS (transparent proxy via iptables DNAT)
  gateway (MITM proxy container)
     │ HTTPS (real credentials injected)
  LLM API
```

The **telegram-adapter** sidecar runs a grammY bot that polls Telegram for messages. When a user sends a message, it forwards it to the agent-manager via ACP over WebSocket. The agent processes the prompt and streams response chunks back, which the adapter sends as Telegram messages.

The **gateway** intercepts outbound requests to `api.telegram.org` and rewrites the dummy bot token in the URL path with the real one — so the agent container never has access to actual secrets.

## Prerequisites

1. **A Telegram bot token** — Create a bot via [@BotFather](https://t.me/BotFather) on Telegram:
   - Send `/newbot` to BotFather
   - Choose a name and username for your bot
   - Copy the token (format: `123456789:AAH...`)

2. **Your Telegram username** — The username you'll use to interact with the bot (without the `@` prefix in `.env`, with `@` prefix in `allowed_users`)

3. **An LLM API key** — For the LLM gateway your agent will use (e.g. `STX_LLM_GATEWAY_API_KEY`)

4. **agent-sandbox installed** — The `agent-sandbox` CLI must be available in your PATH

5. **Docker** — Docker Engine or Docker Desktop running locally

## Step-by-Step Setup

### 1. Create your project directory

```bash
mkdir my-telegram-agent && cd my-telegram-agent
```

### 2. Create the project structure

Create a `fleet.yaml` at the project root and an agent subdirectory:

```bash
mkdir telegram-agent
```

**fleet.yaml** (project root):

```yaml
agents:
  - telegram-agent
```

**telegram-agent/agent.yaml**:

```yaml
name: telegram-agent
core_version: latest
log_level: debug

runtime:
  image: "@builtin/codex"
  extra_builds:
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

### 3. Create the `.env` file

```bash
cat > .env << 'EOF'
TELEGRAM_BOT_TOKEN=your-bot-token-from-botfather
TELEGRAM_USERNAME=your-telegram-username
STX_LLM_GATEWAY_API_KEY=your-llm-api-key
EOF
```

Replace the placeholder values with your real credentials.

### 4. Set up the Telegram plugin

The Telegram integration is a local plugin that contributes two things:
- A gateway middleware for bot token rewriting
- A sidecar service (telegram-adapter) that bridges Telegram to ACP

Copy the plugin from the example:

```bash
cp -r /path/to/agent-sandbox/examples/telegram/plugins/telegram ./plugins/telegram
```

Or reference the working example at `examples/telegram/` in the agent-sandbox repository.

### 5. Generate and start

```bash
agent-sandbox generate
agent-sandbox compose up --build -d
```

### 6. Verify startup

```bash
# Watch logs for successful initialization
agent-sandbox compose logs -f

# Look for:
# - "connected" (telegram-adapter connected to agent-manager)
# - "ACP initialized"
# - "telegram adapter started"
```

## Configuration Reference

### Plugin Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `bot_token` | string | yes | — | Telegram bot token. Use `${ENV_VAR}` to reference `.env` |
| `allowed_users` | array | no | — | Telegram usernames allowed to interact (with `@` prefix) |
| `agent_manager_port` | string | no | `3100` | Port the agent-manager ACP endpoint listens on |

### Plugin Contributions

The Telegram plugin (`plugin.yaml`) contributes:

**Gateway middleware** — Intercepts HTTPS requests to `api.telegram.org` and rewrites the bot token in the URL path:
```
/bot<DUMMY_TOKEN>/sendMessage → /bot<REAL_TOKEN>/sendMessage
```

**Sidecar service** — Runs the `telegram-adapter` container alongside the agent, connected via:
- `AGENT_MANAGER_URL` — WebSocket endpoint to agent-manager (e.g. `ws://telegram-agent:3100/acp`)
- `TELEGRAM_BOT_TOKEN` — The real bot token (only visible to the sidecar, not the agent)

### How Token Rewriting Works

The gateway middleware (`middlewares/telegram-token-rewrite.go`) is a Go template rendered at generate-time. The `{{ .options.bot_token }}` placeholder is resolved to the actual token from `.env` and baked into the gateway binary. At runtime, any request to `api.telegram.org` with a `/bot<token>/` path has the token replaced with the real one.

This means the agent container can use any dummy token value — the gateway transparently fixes it.

## Testing the Bot

1. **Open Telegram** and search for your bot by its username
2. **Send `/start`** to initiate a conversation
3. **Send a message** — you should see the bot react with an eye emoji (👀) while processing
4. **Wait for a response** — the agent processes your prompt and streams the reply back

### Verifying the Full Chain

```bash
# Check telegram-adapter logs
agent-sandbox compose logs telegram

# Check agent-manager logs
agent-sandbox compose logs agent | grep -i acp

# Check gateway logs for token rewriting
agent-sandbox compose logs gateway | grep telegram
```

## Troubleshooting

### Bot not responding at all

1. **Verify the bot token** — Make sure `TELEGRAM_BOT_TOKEN` in `.env` is correct and the bot is active (check with BotFather)
2. **Check sidecar is running:**
   ```bash
   agent-sandbox compose ps
   ```
   Look for a `telegram` service in the output.
3. **Check adapter logs:**
   ```bash
   agent-sandbox compose logs telegram
   ```
   Look for `TELEGRAM_BOT_TOKEN is required` (missing token) or connection errors.

### Bot reacts (👀) but never replies

The adapter received your message but the agent didn't produce a response.

1. **Check agent-manager connection:**
   ```bash
   agent-sandbox compose logs telegram | grep -i "connected\|error\|WS"
   ```
2. **Check agent logs** for ACP errors:
   ```bash
   agent-sandbox compose logs agent
   ```
3. **Verify the LLM gateway** is reachable and your API key is valid.

### "Sorry, the agent is temporarily unavailable"

The adapter exhausted its retry attempts (3 retries with exponential backoff). This usually means agent-manager is down or unreachable.

1. Restart the stack: `agent-sandbox compose restart`
2. If persistent, check that the `agent_manager_port` matches across your configuration

### Only certain users can interact

The `allowed_users` option restricts access. If set, only listed usernames (with `@` prefix) can send messages. Other users are silently ignored.

To allow anyone:
```yaml
- plugin: ./plugins/telegram
  options:
    bot_token: "${TELEGRAM_BOT_TOKEN}"
    # Remove or leave empty: allowed_users
```

### Messages are truncated

Telegram has a 4096-character message limit. The adapter automatically chunks longer responses. If responses appear cut off mid-thought, this is the LLM stopping generation — not a Telegram issue.

### Token rewriting not working (401 from Telegram API)

The gateway middleware isn't rewriting the token correctly.

1. Regenerate: `agent-sandbox generate && agent-sandbox compose up --build`
2. Verify `.env` has the correct `TELEGRAM_BOT_TOKEN` value
3. Check gateway logs for MITM errors on `api.telegram.org`

## Notes

- The telegram plugin uses a **legacy WebSocket sidecar approach**. For new deployments, consider the `open-acp` plugin with OpenACP, which manages the Telegram channel directly as the container entrypoint (no separate sidecar needed). See the `examples/telegram/` README for the OpenACP architecture.
- The adapter reconnects automatically if the WebSocket connection to agent-manager drops (3-second delay between attempts).
- Each Telegram chat ID maps to a separate ACP session, so different conversations are isolated.
