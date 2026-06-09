# mcp-oauth

Provides full OAuth lifecycle for MCP (Model Context Protocol) providers: automatic token injection, refresh, and browser-based authorization via gateway callback.

## How It Works

1. **Middleware** intercepts requests to configured domains. If a valid token exists, injects `Authorization: Bearer <token>`. If no token exists, returns 401 with an `authorize_url` for the user to click.
2. **Callback handler** at `/plugins/mcp-oauth/callback` receives the OAuth authorization code, exchanges it for tokens, and writes the token file to the shared volume.
3. **Shared volume** (`oauth-tokens`) is mounted into both gateway and agent containers so the MCP client can read tokens written by the gateway.

## Login Flow (Recommended)

The login endpoint handles the full OAuth lifecycle including PKCE and Dynamic Client Registration.

### Quick Start

```bash
# 1. Start your agent-sandbox environment
agent-sandbox -C <project-dir> compose up --build

# 2. Discover the gateway URL (port is dynamically assigned)
agent-sandbox -C <project-dir> gateway-url
# http://localhost:49321

# 3. Initiate login for a provider
curl $(agent-sandbox -C <project-dir> gateway-url)/plugins/mcp-oauth/login/<provider>

# Response:
# {"authorize_url":"https://...","provider":"<provider>","instructions":"Open the authorize_url in your browser to complete login."}

# 4. Open the authorize_url in your browser and complete authorization
#    The browser will redirect back to the gateway and show "Authorization successful"

# 5. Done — the agent can now use the MCP provider transparently
```

> **Note:** The gateway port is dynamically assigned to avoid conflicts when running
> multiple agent-sandbox instances on the same host. Use `agent-sandbox gateway-url`
> to discover the current port. If you need a fixed port, set `gateway.public_url`
> in your `agent.yaml`.

### How It Works

1. `GET /plugins/mcp-oauth/login/{provider}` — Gateway performs Dynamic Client Registration (if needed), generates PKCE challenge, and returns an authorize URL
2. User opens the URL in their browser and authorizes
3. Provider redirects to the gateway's `/plugins/mcp-oauth/callback` with an authorization code
4. Gateway exchanges the code (with PKCE code_verifier) for tokens and stores them
5. All subsequent agent requests to the provider domain get `Authorization: Bearer <token>` injected automatically

### Listing Providers

```bash
curl $(agent-sandbox -C <project-dir> gateway-url)/plugins/mcp-oauth/login/
# {"available":["notion"],"error":"provider name required","usage":"GET /plugins/mcp-oauth/login/<provider_name>"}
```

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
