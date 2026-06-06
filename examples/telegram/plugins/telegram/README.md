# telegram

Gateway middleware for Telegram bot token injection. Rewrites the dummy bot token in API requests to the real one.

## How It Works

The Telegram bot SDK (grammy) sends requests to `https://api.telegram.org/bot<TOKEN>/...`. This middleware intercepts those requests at the gateway and replaces the dummy token in the URL path with the real bot token — baked into the gateway binary at generate-time.

The agent container never has access to the real bot token.

## Usage

```yaml
# plugin.yaml
name: telegram
requires: ["@builtin/agent-manager-acp"]
```

```yaml
# agent.yaml
installations:
  - plugin: telegram
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      allowed_users:
        - "@yourusername"
```

```bash
# .env
TELEGRAM_BOT_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `bot_token` | string | yes | — | Telegram bot token. Use `${ENV_VAR}` to reference `.env` |
| `allowed_users` | array | no | — | Telegram usernames allowed to interact |
| `agent_manager_port` | string | no | `"3100"` | Port the agent-manager ACP endpoint listens on |

## What It Contributes

- **Gateway:** Custom middleware for Telegram API token rewriting (MITM for `api.telegram.org` with URL path token rewrite)
- **Sidecar:** `telegram` service (telegram-adapter) that connects to agent-manager via WebSocket and bridges Telegram messages to ACP

## Middleware Template

The middleware (`middlewares/telegram-token-rewrite.go`) is a Go template. At generate-time, `{{ .options.bot_token }}` is resolved to the actual token value from `.env`.
