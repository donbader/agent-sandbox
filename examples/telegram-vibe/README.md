# Telegram Vibe Example

Everything from `local-coding` plus a Telegram bot channel — talk to your coding agent from Telegram.

## What's Included

- **telegram** — bridge channel that connects a Telegram bot to the agent via ACP protocol (JSON lines on stdin/stdout).
- **static-header** (instance: stx-llm-gateway) — gateway intercepts requests to `agent-gateway.stx-ai.net` and injects your real API key via MITM.
- **custom-runtime** — overlays codex configuration into the agent's home directory.

## Prerequisites

- A Telegram bot token (from [@BotFather](https://t.me/BotFather))

## Setup

```bash
cd examples/telegram-vibe

# Generate build artifacts
agent-sandbox generate

# Create .env from the example
cp .env.example .env
# Edit .env and fill in:
#   TELEGRAM_USERNAME=your-telegram-username
#   TELEGRAM_BOT_TOKEN=your-bot-token
#   STX_LLM_GATEWAY_API_KEY=your-api-key

# Build and run
agent-sandbox compose up --build
```

## How It Works

1. The gateway intercepts connections to `api.telegram.org` via MITM
2. The bridge starts a Telegram bot using a dummy token
3. When the bot makes API calls, the gateway rewrites the dummy token to the real `TELEGRAM_BOT_TOKEN`
4. Messages from Telegram are forwarded to the agent via stdin (JSON lines)
5. Agent responses on stdout are sent back to Telegram

## Architecture

```
Telegram API
     ↕ (real token injected by gateway)
  Gateway (MITM for api.telegram.org + agent-gateway.stx-ai.net)
     ↕
  Bridge (grammy bot with dummy token)
     ↕ (JSON lines on stdin/stdout)
  Agent (codex)
```

The real bot token and API keys never enter the agent's environment — they're only available to the gateway process.
