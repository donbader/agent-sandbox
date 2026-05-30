# ADR-004: Composable Egress Presets Over Single Gateway

## Status
Accepted

## Context
Agents need egress rules for different purposes:
- Telegram API access (with bot token injection)
- GitHub API access (with PAT injection)
- MCP OAuth endpoints (with Bearer token injection)
- Docker API Proxy access
- General internet access (catch-all)

Initially we designed a single `gateway:` per agent. This led to either:
- Duplicating rules across gateways (e.g., GitHub PAT in every gateway)
- Sharing one gateway but losing per-agent customization

## Decision
Replace `gateways:` with `egress-presets:` — named, reusable sets of egress rules that agents compose by selecting multiple presets.

```yaml
# agents/coder/agent.yaml
egress: [telegram-bot-1, notion-mcp, docker, main]
```

Rules are evaluated in order across presets (first preset's rules first). First match wins.

## Consequences

**Positive:**
- Composable: mix and match presets per agent
- Reusable: `main` preset shared across all agents
- Clear separation: each preset has one concern (Telegram, GitHub, Docker, etc.)
- Easy to add/remove capabilities by editing the preset list
- Per-agent bot tokens via separate telegram presets

**Negative:**
- Slightly more complex mental model than a single gateway
- Order matters — users must put catch-all (`host: ["*"]`) preset last
- Rule conflicts between presets resolved by "first match wins" (could be surprising)

**Example:**
```yaml
egress-presets:
  telegram-bot-1:
    - host: ["api.telegram.org"]
      provider: ".../egress-rules/telegram-bot"
      options: { token: "${TELEGRAM_BOT_TOKEN}" }

  main:
    - host: ["api.github.com", "github.com"]
      provider: ".../egress-rules/github-pat"
      options: { token: "${GITHUB_PAT_TOKEN}" }
    - host: ["*"]
```
