# mcp-oauth

Provides full OAuth lifecycle for MCP (Model Context Protocol) providers: automatic token injection, refresh, and browser-based authorization via gateway callback.

## How It Works

1. **Middleware** (`src/oauth.ts`) intercepts requests to configured domains. If a valid token exists, injects `Authorization: Bearer <token>`. If no token exists, returns 401 with an `authorize_url` for the user to click.
2. **Login handler** (`src/login.ts`) at `/plugins/mcp-oauth/login/{provider}` performs Dynamic Client Registration and PKCE challenge generation, returning an authorize URL.
3. **Callback handler** (`src/callback.ts`) at `/plugins/mcp-oauth/callback` receives the OAuth authorization code, exchanges it for tokens (with PKCE via `src/pkce.ts`), and writes the token file to the shared volume.
4. **Shared volume** (`oauth-tokens`) is mounted into both gateway and agent containers so the MCP client can read tokens written by the gateway.

## Login Flow (Recommended)

The login endpoint handles the full OAuth lifecycle including PKCE and Dynamic Client Registration.

### Quick Start

From inside the agent container:

```bash
# 1. Initiate login for a provider
curl -s "http://<gateway-host>:8080/plugins/mcp-oauth/login/<provider>?callback_url=http://127.0.0.1/plugins/mcp-oauth/callback"

# Response:
# {"authorize_url":"https://...","provider":"<provider>","instructions":"Open the authorize_url in your browser to complete login."}

# 2. Open the authorize_url in your browser and authorize
#    The browser will redirect to http://127.0.0.1/... which won't load — that's expected.

# 3. Copy the full URL from the browser address bar and extract code + state params

# 4. Complete the flow by calling the callback endpoint directly:
curl -s "http://<gateway-host>:8080/plugins/mcp-oauth/callback?code=<CODE>&state=<STATE>"

# 5. Done — the agent can now use the MCP provider transparently
```

> **Note:** The gateway port is not exposed to the Docker host. The agent reaches
> the gateway internally via the Docker network (e.g., `http://my-agent-gateway:8080`).
> The `callback_url=http://127.0.0.1/...` parameter satisfies OAuth providers that
> require loopback URIs for dynamic registration.

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

### Specifying a Custom Callback URL

The login endpoint accepts an optional `callback_url` query parameter to override the derived redirect URI. Use a loopback address to satisfy OAuth providers that require HTTPS or loopback URIs for dynamic registration:

```bash
curl -s "http://<gateway-host>:8080/plugins/mcp-oauth/login/notion?callback_url=http://127.0.0.1/plugins/mcp-oauth/callback"
```

### Checking Connection Status

```bash
# Single provider
curl -s http://<gateway-host>:8080/plugins/mcp-oauth/status/notion
# {"connected":true,"expired":false}

# All providers
curl -s http://<gateway-host>:8080/plugins/mcp-oauth/status
# {"notion":{"connected":true,"expired":false},"jira":{"connected":false,"expired":false}}
```

### Disconnecting a Provider

Revokes the token (if the provider supports RFC 7009 revocation) and clears local storage:

```bash
curl -s http://<gateway-host>:8080/plugins/mcp-oauth/disconnect/notion
# {"disconnected":true,"revoked":true,"provider":"notion"}
```

`revoked: false` means the provider doesn't expose a revocation endpoint — the token was cleared locally only.

## Usage

```yaml
# agent.yaml or fleet.yaml shared config
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

- **Gateway middleware:** `src/oauth.ts` — Token injection + 401 with authorize URL when unauthenticated
- **Gateway routes:**
  - `src/login.ts` — `/plugins/mcp-oauth/login/{provider}` — PKCE + Dynamic Client Registration
  - `src/callback.ts` — `/plugins/mcp-oauth/callback` — OAuth code exchange handler
  - `src/status.ts` — `/plugins/mcp-oauth/status/{provider}` — Connection status check
  - `src/disconnect.ts` — `/plugins/mcp-oauth/disconnect/{provider}` — Token revocation and removal
  - `src/pkce.ts` — PKCE challenge/verifier utilities
- **Gateway volume:** Shared `oauth-tokens` volume at `token_dir`

## OAuth Flow

```
1. Agent calls: GET /plugins/mcp-oauth/login/<provider>?callback_url=http://127.0.0.1/...
2. Gateway performs DCR + PKCE → returns authorize_url
3. User opens authorize_url in browser → provider login page
4. User authorizes → browser redirects to http://127.0.0.1/... (fails to load — expected)
5. User copies the full URL from browser address bar, pastes it back to agent
6. Agent extracts code + state, calls: GET /plugins/mcp-oauth/callback?code=X&state=Y
7. Gateway exchanges code for tokens → writes /data/plugins/mcp-oauth/<provider>.json
8. Next request → middleware reads token → injects Bearer header → proxied to provider
```

## Agent Skill (Pi)

To let your agent handle OAuth connections via natural language ("connect me to Notion"), add a Pi skill:

Create `~/.pi/agent/skills/mcp-oauth/SKILL.md` (or seed it into the agent's home directory):

```markdown
---
name: mcp-oauth
description: Connect, disconnect, and check status of MCP OAuth services. Use when the user wants to connect a service, check connection status, or fix auth errors.
---

# MCP OAuth Management

## Gateway Base URL

http://<gateway-service-name>:8080/plugins/mcp-oauth

## Discover Providers

curl -s http://<gateway-service-name>:8080/plugins/mcp-oauth/status

## Connect

1. curl -s "http://<gateway-service-name>:8080/plugins/mcp-oauth/login/<provider>?callback_url=http://127.0.0.1/plugins/mcp-oauth/callback"
2. Show authorize_url to user
3. User authorizes, browser fails to load redirect — user copies URL and pastes it back
4. Extract code and state from pasted URL
5. curl -s "http://<gateway-service-name>:8080/plugins/mcp-oauth/callback?code=<CODE>&state=<STATE>"
6. Confirm with /status/<provider>

## Disconnect

curl -s http://<gateway-service-name>:8080/plugins/mcp-oauth/disconnect/<provider>
```

Replace `<gateway-service-name>` with your agent's gateway Docker service name (e.g., `my-agent-gateway`).
