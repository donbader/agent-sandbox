# agw-client plugin

Routes agent LLM traffic through the STX Agent Gateway (AGW) using the transparent MITM proxy + credential injection pattern.

## How it works

1. The agent container starts with `ANTHROPIC_BASE_URL` pointing to AGW and a dummy `ANTHROPIC_API_KEY`
2. Claude Code / omp sends requests to the AGW host — the MITM proxy intercepts them
3. The gateway middleware injects `Authorization: Bearer <token>` using the real token from plugin options
4. The real token never enters the container

## Usage

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/agw-client"
    options:
      token: "${AGW_TOKEN}"
```

Add `AGW_TOKEN=<your-bearer-token>` to your fleet `.env` file.

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `token` | string | ✅ | — | AGW bearer token env var reference (e.g. `${AGW_TOKEN}`) |
| `host` | string | | `agent-gateway.stx-ai.net` | AGW hostname |
| `provider` | string | | `kiro` | AGW provider segment |

## Custom host/provider

```yaml
installations:
  - plugin: "@builtin/agw-client"
    options:
      token: "${AGW_TOKEN}"
      host: "my-custom-gateway.example.com"
      provider: "kiro"
```

This sets:
- `ANTHROPIC_BASE_URL=https://my-custom-gateway.example.com/kiro/anthropic/v1`
- `ANTHROPIC_API_KEY=agw-via-gateway` (dummy, replaced at proxy layer)

## Security

- The real bearer token is stored only in the gateway process environment, never inside the agent container
- The token is registered with `gw.secrets.register()` so it is scrubbed from all gateway logs
- The dummy `x-api-key` header is cleared before forwarding to AGW
