# agw-client plugin

Injects an STX Agent Gateway (AGW) Bearer token into outbound requests to the AGW host. Works with any LLM client — omp, Claude Code, Codex CLI, or anything else that routes traffic through the sandbox gateway.

## How it works

The plugin intercepts all HTTPS traffic to the configured AGW host at the MITM proxy layer and injects `Authorization: Bearer <token>`. The real token lives only in the gateway process — the agent container never sees it.

You are responsible for configuring your LLM client inside the container to point at the AGW host. The plugin handles credential injection transparently once traffic reaches the gateway.

## Usage

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/agw-client"
    options:
      token: "${AGW_TOKEN}"
```

Add `AGW_TOKEN=<your-bearer-token>` to your fleet `.env` file.

Then configure your LLM client in the agent container to target AGW. For example:

- **omp / Claude Code**: set `ANTHROPIC_BASE_URL=https://agent-gateway.stx-ai.net/kiro/anthropic/v1` in your preset or entrypoint
- **OpenAI-compatible clients**: set `OPENAI_BASE_URL=https://agent-gateway.stx-ai.net/kiro/openai/v1`

Use a dummy value for whatever API key the client requires (e.g. `ANTHROPIC_API_KEY=dummy`). The real credential is injected by the gateway and never needs to be in the container.

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `token` | string | ✅ | — | AGW bearer token env var reference (e.g. `${AGW_TOKEN}`) |
| `host` | string | | `agent-gateway.stx-ai.net` | AGW hostname to intercept |

## Custom host

```yaml
installations:
  - plugin: "@builtin/agw-client"
    options:
      token: "${AGW_TOKEN}"
      host: "my-custom-gateway.example.com"
```

## Security

- The real bearer token is stored only in the gateway process environment, never inside the agent container
- The token is registered with `gw.secrets.register()` so it is scrubbed from all gateway logs
