# Telegram Example

This example deploys a codex agent reachable via Telegram.

## Prerequisites

- A Telegram bot token (from [@BotFather](https://t.me/BotFather))

## Setup

```bash
cd examples/telegram

# Generate build artifacts
agent-sandbox generate

# Create .env from the generated example
cp .env.example .env
# Edit .env and fill in:
#   TELEGRAM_BOT_TOKEN=your-bot-token

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
  Gateway (MITM for api.telegram.org)
     ↕
  Bridge (grammy bot with dummy token)
     ↕ (JSON lines on stdin/stdout)
  Agent (codex)
```

The real bot token never enters the agent's environment — it's only available to the gateway process.
