# Multi-Agent Example

Two agents sharing GitHub credentials, each with their own Telegram bot.

## What's Included

- `fleet.yaml` — declares both agents + shared features
- `coder/agent.yaml` — Codex agent with persistent home + TypeScript
- `reviewer/agent.yaml` — Claude Code agent for code review
- Shared: github-pat + ripgrep/fd across both agents

## Setup

1. Copy `.env.example` to `.env` and fill in credentials
2. Create two Telegram bots via @BotFather (one per agent)
3. Update `allowed_users` in both agent configs

```bash
cp .env.example .env
# Edit .env with your tokens

agent-sandbox generate
agent-sandbox compose up --build -d
```

## Architecture

```
fleet.yaml
├── coder/agent.yaml    → @MyCoderBot (codex)
└── reviewer/agent.yaml → @MyReviewerBot (claude-code)

Shared features (github-pat, ripgrep) applied to both.
Per-agent features (telegram with different bot tokens) stay separate.
```

## How It Works

- `fleet.yaml` lists agent subdirectories and shared features
- `agent-sandbox generate` produces `.build/<agent>/` for each agent
- A combined `docker-compose.yml` includes all agent compose files
- Each agent gets its own gateway + container pair
- Shared features are merged with per-agent features (per-agent overrides shared if same plugin name)
