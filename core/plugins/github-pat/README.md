# github-pat

Injects a GitHub Personal Access Token into all requests to `github.com` and `api.github.com` via the gateway.

## How It Works

The middleware (`src/github-auth.ts`) intercepts HTTPS traffic to GitHub domains and adds HTTP Basic authentication using your PAT. Git CLI, `gh`, and any HTTPS-based GitHub access from the agent will be authenticated — without the token ever being exposed in the agent's environment.

The token is passed to the middleware via `options` at runtime. The gateway also sets `GH_TOKEN=dummy` and `GITHUB_TOKEN=dummy` in the agent environment so that git/gh CLI attempt authentication (without these, git skips auth entirely and the gateway never gets a chance to intercept).

## Usage

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "${GITHUB_PAT}"
```

```bash
# .env
GITHUB_PAT=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

## Options

| Option | Type | Required | Description |
|--------|------|----------|-------------|
| `token` | string | yes | GitHub PAT. Use `${ENV_VAR}` to reference `.env` |
| `deny_paths` | array | no | URL path patterns to block. Format: `METHOD /path/glob` or `/path/glob` |

## Blocking Specific Paths

Use `deny_paths` to block specific GitHub API operations without writing custom middleware. Each entry is a pattern in the format `METHOD /path/glob` (method-specific) or `/path/glob` (any method).

Example — prevent the agent from merging pull requests:

```yaml
- plugin: "@builtin/github-pat"
  options:
    token: ${GITHUB_PAT}
    deny_paths:
      - "PUT /repos/*/pulls/*/merge"
```

## What It Contributes

- **Runtime:** Sets `GH_TOKEN=dummy` and `GITHUB_TOKEN=dummy` so git/gh CLI attempt auth (the gateway replaces with real credentials)
- **Gateway:** MITM for `github.com` + `api.github.com` with Basic auth injection middleware (`src/github-auth.ts`)
