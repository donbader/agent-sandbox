# github-pat

Injects a GitHub Personal Access Token into all requests to `github.com` and `api.github.com` via the gateway.

## How It Works

The middleware intercepts HTTPS traffic to GitHub domains and adds HTTP Basic authentication using your PAT. Git CLI, `gh`, and any HTTPS-based GitHub access from the agent will be authenticated — without the token ever being exposed in the agent's environment.

The token is baked into the gateway binary at generate-time (not passed as an env var at runtime).

## Usage

```yaml
# agent.yaml
installations:
  - plugin: github-pat
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

## What It Contributes

- **Runtime:** Sets `GH_TOKEN=dummy` and `GITHUB_TOKEN=dummy` so git/gh CLI attempt auth (the gateway replaces with real credentials)
- **Gateway:** MITM for `github.com` + `api.github.com` with Basic auth injection middleware
