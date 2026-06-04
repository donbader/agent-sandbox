# mcp-oauth

Provides OAuth token storage for MCP (Model Context Protocol) providers via a shared volume between the gateway and agent.

## How It Works

Declares a named volume (`oauth-tokens`) mounted into the gateway container at a configurable path. MCP providers that require OAuth can store and read token files from this shared location.

> **Note:** This plugin currently only handles the volume declaration. Users must manually declare gateway services for each MCP provider endpoint in their `agent.yaml`. Full automation (dynamic service entries from provider URLs) is planned for a future release.

## Usage

```yaml
# agent.yaml
installations:
  - plugin: mcp-oauth
    options:
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
      token_dir: "/data/oauth-tokens"

gateway:
  services:
    - url: https://mcp.notion.com
      headers:
        Authorization: Bearer ${NOTION_TOKEN}
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `providers` | object | yes | — | Map of provider name to MCP config |
| `token_dir` | string | no | `/data/oauth-tokens` | Directory for OAuth token files |

## What It Contributes

- **Gateway:** Shared volume `oauth-tokens` mounted at `token_dir`
