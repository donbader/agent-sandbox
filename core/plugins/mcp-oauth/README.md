# mcp-oauth

Provides full OAuth lifecycle for MCP (Model Context Protocol) providers: automatic token injection, refresh, and browser-based authorization via gateway callback.

## How It Works

1. **Middleware** (`src/oauth.ts`) intercepts requests to configured domains. If a valid token exists, injects `Authorization: Bearer <token>`. If no token exists, passes the request through unauthenticated (the upstream server will respond normally for public endpoints, or return its own 401 for protected ones).
2. **Login handler** (`src/login.ts`) at `/plugins/mcp-oauth/login/{provider}` performs Dynamic Client Registration and PKCE challenge generation, returning an authorize URL.
3. **Callback handler** (`src/callback.ts`) at `/plugins/mcp-oauth/callback` receives the OAuth authorization code, exchanges it for tokens (with PKCE via `src/pkce.ts`), and writes the token file to the shared volume.
4. **Shared volume** (`mcp-oauth-data`) is mounted into both gateway and agent containers so the MCP client can read tokens written by the gateway.

## Login Flow

The login endpoint handles the full OAuth lifecycle including PKCE and Dynamic Client Registration.

### Quick Start (with `callback_port`)

When `callback_port` is configured, the OAuth flow works end-to-end through your browser:

From inside the agent container:

```bash
# 1. Initiate login for a provider
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/login/notion"

# Response:
# {"authorize_url":"https://...","provider":"notion","instructions":"Open the authorize_url in your browser to complete login."}

# 2. Open the authorize_url in your browser and authorize
#    The browser redirects to http://127.0.0.1:<callback_port>/plugins/mcp-oauth/callback
#    The gateway handles the code exchange automatically and shows "Authorization successful"

# 3. Done — the agent can now use the MCP provider transparently
```

> With `callback_port`, the gateway's HTTP routes are published to the host on that port.
> OAuth providers redirect your browser directly to `http://127.0.0.1:<callback_port>/plugins/mcp-oauth/callback`,
> which the gateway handles seamlessly.

### Quick Start (without `callback_port` — manual flow)

Without `callback_port`, the callback endpoint is not reachable from your browser. You must complete the exchange manually:

```bash
# 1. Initiate login with an explicit callback_url
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/login/<provider>?callback_url=http://127.0.0.1/plugins/mcp-oauth/callback"

# 2. Open the authorize_url in your browser and authorize
#    The browser will redirect to http://127.0.0.1/... which won't load — that's expected.

# 3. Copy the full URL from the browser address bar and extract code + state params

# 4. Complete the flow by calling the callback endpoint directly:
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/callback?code=<CODE>&state=<STATE>"

# 5. Done
```

### How It Works

1. `GET /plugins/mcp-oauth/login/{provider}` — Gateway performs Dynamic Client Registration (if needed), generates PKCE challenge, and returns an authorize URL
2. User opens the URL in their browser and authorizes
3. Provider redirects to the callback URL with an authorization code
4. Gateway exchanges the code (with PKCE code_verifier) for tokens and stores them
5. All subsequent agent requests to the provider domain get `Authorization: Bearer <token>` injected automatically

### Listing Providers

```bash
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/login/"
# {"available":["notion","datadog","slack"],"error":"provider name required","usage":"GET /plugins/mcp-oauth/login/<provider_name>"}
```

### Checking Connection Status

```bash
# Single provider
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/status/notion"
# {"connected":true,"expired":false,"has_refresh_token":true,"scope":"read_content write_content"}

# All providers
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/status"
# {"notion":{"connected":true,...},"datadog":{"connected":false,...}}
```

### Disconnecting a Provider

Revokes the token (if the provider supports RFC 7009 revocation) and clears local storage:

```bash
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/disconnect/notion"
# {"disconnected":true,"revoked":true,"provider":"notion"}
```

`revoked: false` means the provider doesn't expose a revocation endpoint — the token was cleared locally only.

## Usage

### Single agent

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/mcp-oauth"
    options:
      callback_port: 9080
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
        datadog:
          mcp_url: https://mcp.datadoghq.com/v1/mcp
```

### Fleet (multiple agents)

Each agent needs a unique `callback_port` to avoid host port conflicts:

```yaml
# fleet.yaml — shared github-pat, per-agent mcp-oauth
shared:
  installations:
    - plugin: "@builtin/github-pat"
      options:
        token: ${GITHUB_PAT}
```

```yaml
# claude-agent/agent.yaml
installations:
  - plugin: "@builtin/mcp-oauth"
    options:
      callback_port: 9080
      volume_strategy: fleet
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
```

```yaml
# codex-agent/agent.yaml
installations:
  - plugin: "@builtin/mcp-oauth"
    options:
      callback_port: 9081
      volume_strategy: fleet
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
```

> **Note:** Using `volume_strategy: fleet` shares OAuth tokens across all agents — authenticate once, and the entire fleet can access the provider.

### Static credentials (no dynamic registration)

```yaml
installations:
  - plugin: "@builtin/mcp-oauth"
    options:
      callback_port: 9080
      providers:
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
| `callback_port` | integer | no | — | Host port to publish the OAuth callback on. Enables seamless browser-based OAuth flow. Each agent in a fleet must use a unique port. |
| `callback_url` | string | no | derived | Explicit callback URL (overrides `callback_port` derivation). Use when behind a reverse proxy or custom domain. |
| `volume_strategy` | string | no | `per_agent` | Token storage volume strategy (see below) |

### Volume Strategy

| Mode | Behavior |
|------|----------|
| `per_agent` | Each agent gets its own token volume. Login once per agent. Default. |
| `fleet` | All agents share one token volume. Login once, entire fleet is authenticated. |
| `none` | No persistent volume. Tokens live in memory only, lost on container restart. |

### Provider Config

Each provider entry supports two modes:

**Dynamic mode** (recommended for MCP servers that support RFC 7591):

| Field | Required | Description |
|-------|----------|-------------|
| `mcp_url` | yes | MCP server endpoint — metadata + registration auto-discovered |
| `skip_resource` | no | Set `true` to omit the `resource` parameter in the authorize URL |

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

- **Gateway middleware:** `src/oauth.ts` — Token injection when authenticated, passthrough when not
- **Gateway routes:**
  - `src/login.ts` — `/plugins/mcp-oauth/login/{provider}` — PKCE + Dynamic Client Registration
  - `src/callback.ts` — `/plugins/mcp-oauth/callback` — OAuth code exchange handler
  - `src/status.ts` — `/plugins/mcp-oauth/status/{provider}` — Connection status check
  - `src/disconnect.ts` — `/plugins/mcp-oauth/disconnect/{provider}` — Token revocation and removal
  - `src/pkce.ts` — PKCE challenge/verifier utilities
- **Published port** (when `callback_port` is set): Maps host port → gateway port 8080 for browser callbacks
- **Gateway volume:** Shared `mcp-oauth-data` volume for token persistence

## OAuth Flow

### With `callback_port` (recommended)

```
1. Agent calls: GET /plugins/mcp-oauth/login/<provider>
2. Gateway performs DCR + PKCE → returns authorize_url
   (redirect_uri = http://127.0.0.1:<callback_port>/plugins/mcp-oauth/callback)
3. User opens authorize_url in browser → provider login page
4. User authorizes → browser redirects to http://127.0.0.1:<callback_port>/plugins/mcp-oauth/callback
5. Gateway receives the code, exchanges it for tokens → shows "Authorization successful"
6. Next request → middleware reads token → injects Bearer header → proxied to provider
```

### Without `callback_port` (manual)

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

## Troubleshooting

### "Connection refused" on OAuth callback

The OAuth provider redirected your browser to a port that isn't published on the host.

**Fix:** Set `callback_port` in the mcp-oauth plugin options:
```yaml
options:
  callback_port: 9080
```

The generator will warn if OAuth routes are present without a published port.

### Port conflict error during generation

```
port conflict: host port 9080 is used by both "claude-agent" and "codex-agent"
```

**Fix:** Each agent in a fleet must use a unique `callback_port`. Move mcp-oauth from shared to per-agent installations with different ports.

### Token not injected after successful login

Check connection status:
```bash
curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/status/<provider>"
```

If `connected: true` but requests still fail, the domain might not be in the middleware's intercept list. Ensure `mcp_url` points to the correct server origin.

## Agent Skill

To let your agent handle OAuth connections via natural language ("connect me to Notion"), seed a skill into the agent's home directory:

```markdown
---
name: mcp-oauth
description: Connect, disconnect, and check status of MCP OAuth services. Use when the user wants to connect a service, check connection status, or fix auth errors.
---

# MCP OAuth Management

## Gateway Base URL

http://${GATEWAY_HOST}:8080/plugins/mcp-oauth

## Discover Providers

curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/status"

## Connect

1. curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/login/<provider>"
2. Show authorize_url to user — ask them to open it in their browser
3. With callback_port configured, the flow completes automatically in the browser
4. Confirm with /status/<provider>

## Disconnect

curl -s "http://${GATEWAY_HOST}:8080/plugins/mcp-oauth/disconnect/<provider>"
```

The `GATEWAY_HOST` environment variable is automatically set in the agent container by agent-sandbox (points to the gateway Docker service name).
