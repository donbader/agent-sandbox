# mcp-oauth

OAuth Bearer token injection for remote MCP servers. Connects to MCP servers like Notion, Slack, Jira, and Datadog via interactive OAuth flow, then transparently injects credentials at runtime.

## Config

```yaml
features:
  - plugin: mcp-oauth
    providers:
      notion:
        mcp_url: https://mcp.notion.com/mcp
      slack:
        mcp_url: https://mcp.slack.com/mcp
        client_id: "pre-registered-id"
        client_secret: "${SLACK_CLIENT_SECRET}"
    token_dir: /data/oauth-tokens  # optional, default shown
```

### Provider fields

| Field | Required | Description |
|-------|----------|-------------|
| `mcp_url` | yes | MCP server URL (used for RFC 9728 OAuth discovery) |
| `client_id` | no | Pre-registered OAuth client ID. Required until DCR is implemented. |
| `client_secret` | no | OAuth client secret (use `${ENV_VAR}` for secrets) |

## How it works

### Setup (interactive, one-time per provider)

```
You: /oauth
Bot: OAuth providers:
       notion — not connected
       slack — not connected

You: /oauth notion
Bot: Authorize with notion:
     https://api.notion.com/v1/oauth/authorize?client_id=...&code_challenge=...
     After authorizing, paste the callback URL here.

You: http://localhost:3000/oauth/callback?code=abc123&state=xyz
Bot: OAuth complete for notion. Token saved.
```

### Runtime (automatic, transparent)

Once connected, the gateway automatically:
1. Reads the stored token from `/data/oauth-tokens/notion.json`
2. Injects `Authorization: Bearer <token>` into requests to `mcp.notion.com`
3. Auto-refreshes the token when it expires (using refresh_token grant)

The agent never sees the real token — it just makes normal HTTPS requests to the MCP server.

## Architecture

```
┌─ Channel Manager ────────────────────────┐
│  /oauth command                          │
│    → RFC 9728 discovery                  │
│    → PKCE auth URL generation            │
│    → Paste-back callback handling        │
│    → Token exchange + file write         │
│                                          │
│  Token file: /data/oauth-tokens/*.json   │
└──────────────────────────────────────────┘
         │ shared volume (oauth-tokens)
┌────────▼─────────────────────────────────┐
│  Gateway (OAuthRewriter)                 │
│    → Reads token file                    │
│    → Caches in memory                    │
│    → Auto-refreshes when expired         │
│    → Injects Bearer header via MITM      │
└──────────────────────────────────────────┘
```

## Security

- **HTTPS enforced** — token_endpoint must be HTTPS; HTTP is rejected
- **SSRF protection** — refresh requests block private/loopback/link-local IPs
- **Atomic writes** — token files written via tmp + rename (crash-safe)
- **File permissions** — token files are 0600
- **No secrets in agent** — tokens only exist in gateway + channel-manager containers
- **Response size limit** — refresh responses capped at 1MB

## Token file format

Written by `/oauth` command, read by gateway OAuthRewriter:

```json
{
  "access_token": "ntn_...",
  "refresh_token": "ref_...",
  "expires_at": 1715000000,
  "token_endpoint": "https://api.notion.com/v1/oauth/token",
  "client_id": "auto-registered-id",
  "client_secret": "optional-secret"
}

```

`expires_at` is Unix seconds (not milliseconds).

## Limitations

- **No DCR yet** — Dynamic Client Registration (RFC 7591) is not implemented. You must provide a `client_id` for each provider.
- **Paste-back UX** — User must manually copy the callback URL from the browser and paste it back. No automatic redirect handling.
- **Single token per provider** — One token file per provider name. Multiple accounts on the same provider require multiple provider entries with different names.

## Contributing commands

This plugin uses the **command plugin interface** to register `/oauth` into the channel-manager. The command source lives in `command/` and gets copied into the channel-manager build at generate time. See `command/oauth-command.ts` for the implementation.
