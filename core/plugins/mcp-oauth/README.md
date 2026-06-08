# mcp-oauth

Provides full OAuth lifecycle for MCP (Model Context Protocol) providers: automatic token injection, refresh, and browser-based authorization via gateway callback.

## How It Works

1. **Middleware** intercepts requests to configured domains. If a valid token exists, injects `Authorization: Bearer <token>`. If no token exists, returns 401 with an `authorize_url` for the user to click.
2. **Callback handler** at `/plugins/mcp-oauth/callback` receives the OAuth authorization code, exchanges it for tokens, and writes the token file to the shared volume.
3. **Shared volume** (`oauth-tokens`) is mounted into both gateway and agent containers so the MCP client can read tokens written by the gateway.

## Usage

```yaml
# agent.yaml
gateway:
  public_url: "https://gateway.myagent.example.com"
  services:
    - url: https://mcp.notion.com

installations:
  - plugin: "@builtin/mcp-oauth"
    options:
      providers:
        # Dynamic mode: just provide mcp_url — credentials auto-discovered
        notion:
          mcp_url: https://mcp.notion.com/mcp

        # Static mode: provide all OAuth details manually
        custom-provider:
          mcp_url: https://custom.example.com/mcp
          authorize_endpoint: https://custom.example.com/oauth/authorize
          token_endpoint: https://custom.example.com/oauth/token
          client_id: "your-client-id"
          client_secret: "your-client-secret"
          scopes: "read_content"
      token_dir: "/data/oauth-tokens"
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `providers` | object | yes | — | Map of provider name to OAuth config |
| `token_dir` | string | no | `/data/oauth-tokens` | Directory for OAuth token files |

### Provider Config

Each provider entry supports two modes:

**Dynamic mode** (recommended for MCP servers that support RFC 7591):

| Field | Required | Description |
|-------|----------|-------------|
| `mcp_url` | yes | MCP server endpoint — metadata + registration auto-discovered |

**Static mode** (for providers without dynamic registration):

| Field | Required | Description |
|-------|----------|-------------|
| `mcp_url` | yes | MCP server endpoint |
| `authorize_endpoint` | yes | OAuth authorize URL |
| `token_endpoint` | yes | OAuth token exchange URL |
| `client_id` | yes | OAuth client ID |
| `client_secret` | no | OAuth client secret |
| `scopes` | no | Space-separated scopes |

Mode is auto-detected: if `client_id` is absent, dynamic mode is used.

## What It Contributes

- **Gateway middleware:** Token injection + 401 with authorize URL when unauthenticated
- **Gateway route:** `/plugins/mcp-oauth/callback` — OAuth code exchange handler
- **Gateway volume:** Shared `oauth-tokens` volume at `token_dir`

## OAuth Flow

```
1. Agent MCP client → request to notion domain
2. Gateway middleware: no token file → returns 401 + authorize_url
3. User clicks authorize_url → Notion login page
4. Notion redirects → https://gateway.example.com/plugins/mcp-oauth/callback?code=X&state=notion
5. Gateway callback handler: exchanges code → writes /data/oauth-tokens/notion.json
6. Next request → middleware reads token → injects Bearer header → proxied to Notion
```
