# Plugin Development Guide

## Plugin Types

| Type | Location | When to use |
|------|----------|-------------|
| Runtime preset | `core/presets/<name>/runtime.yaml` | New agent runtime (base image + CLI install) |
| Feature plugin | `core/plugins/<name>/plugin.yaml` | Credential injection, gateway rules, home customization |

Runtime presets are pure YAML — no code. Feature plugins are YAML + TypeScript.

## Directory Structure

```
core/plugins/<name>/
  plugin.yaml              ← required: metadata, config schema, contributions
  src/
    middleware.ts          ← optional: TypeScript middleware (loaded at gateway runtime)
    route-handler.ts       ← optional: TypeScript route handler
```

Feature plugins are fetched from GitHub Releases as part of the core tarball. For local development, use `agent-sandbox --dev` to load plugins directly from source.

## plugin.yaml Schema

```yaml
name: my-plugin

options:
  token:
    type: string           # string | boolean | object | array
    required: true
    description: "Env var reference, e.g. ${MY_TOKEN}"
  cache:
    type: boolean
    required: false
    default: false
    description: "Enable response caching"

contributes:
  runtime:
    extra_builds:
      - "ENV MY_TOKEN=dummy"
  gateway:
    services:
      - url: "https://api.example.com"
    middlewares:
      - script: "./src/my-auth.ts"
        domains: ["api.example.com"]
```

### Real Example: github-pat

```yaml
name: github-pat
options:
  token:
    type: string
    required: true
    description: "GitHub PAT env var reference (e.g. ${GITHUB_PAT})"

contributes:
  runtime:
    extra_builds:
      - "ENV GH_TOKEN=dummy GITHUB_TOKEN=dummy"
  gateway:
    services:
      - url: "https://api.github.com"
      - url: "https://github.com"
    middlewares:
      - script: "./src/github-auth.ts"
        domains: ["api.github.com", "github.com"]
```

### Real Example: mcp-oauth (dynamic services via Go templates)

```yaml
name: mcp-oauth
options:
  providers:
    type: object
    required: true
    description: "Map of provider name to MCP config"
  callback_url:
    type: string
    required: false

contributes:
  gateway:
    services:
{{- range $name, $cfg := .plugin.options.providers }}
      - url: "{{ index $cfg "mcp_url" }}"
{{- end }}
    volumes:
      - "oauth-tokens:/data/plugins/mcp-oauth"
    middlewares:
      - script: "./src/oauth.ts"
    routes:
      - path: "/callback"
        handler: "./src/callback.ts"
      - path: "/login"
        handler: "./src/login.ts"
```

## contributes Reference

### `contributes.runtime`

| Field | Description |
|-------|-------------|
| `extra_builds` | Dockerfile lines appended after base install (RUN, ENV, COPY, etc.) |
| `volumes` | Volume mounts for the agent container |
| `pre_entrypoint` | Commands run before the agent process starts |
| `ports` | Exposed container ports |
| `cap_add` | Linux capabilities (e.g. `SYS_PTRACE`) |
| `skip_userns` | Disable user namespace isolation (boolean) |

### `contributes.gateway`

| Field | Description |
|-------|-------------|
| `services` | Upstream URLs the gateway should proxy |
| `middlewares` | TypeScript middleware scripts with optional domain filter |
| `routes` | HTTP endpoints mounted on the gateway |
| `volumes` | Volumes for the gateway container |

### Middleware entry format

```yaml
middlewares:
  - script: "./src/my-middleware.ts"   # path relative to plugin dir
    domains: ["api.example.com"]       # optional: only fire for these hosts
```

### Route entry format

```yaml
routes:
  - path: "/callback"                  # mounted at /plugins/<plugin-name>/callback
    handler: "./src/callback.ts"       # path relative to plugin dir
```

## Template Context

All `contributes` fields support Go `text/template` syntax. Available data:

| Expression | Description |
|------------|-------------|
| `.plugin.options.<field>` | Resolved plugin option (with defaults applied) |
| `.agent.name` | Agent name from `agent.yaml` |
| `.agent.runtime.image` | Agent base image |
| `.agent.runtime.cwd` | Working directory inside the container |
| `.agent.*` | Full `agent.yaml` as a nested map |

### Template Functions

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON` | Serialize value to JSON string | `{{ toJSON .plugin.options.config }}` |
| `asset` | Resolve path of a declared asset | `{{ asset "my-sidecar" }}` |
| `index` | Access map keys with special characters | `{{ index .plugin.options "my-key" }}` |

## Writing a Middleware

TypeScript middleware intercepts proxied requests. Scripts are loaded by the gateway at runtime via goja (no compilation step).

Type definitions are generated into `.build/gateway.d.ts` at generate time. Reference them for autocompletion:

```typescript
/// <reference path="../../.build/gateway.d.ts" />

const handler: MiddlewareHandler = (ctx, options) => {
  const token = options.token;
  if (!token) return;

  const basic = gw.crypto.base64.encode("x-access-token:" + token);
  ctx.request.setHeader("Authorization", "Basic " + basic);
  gw.secrets.register(token);
};
export default handler;
```

**Key points:**

- `export default handler` — required: must export a function as default
- `ctx.request.path`, `ctx.request.url` — read-only (assignment is a no-op)
- `ctx.request.setHeader(key, val)` — set/overwrite a request header
- `ctx.request.setPath(newPath)` — rewrite the URL path before forwarding
- `ctx.abort(status, body)` — terminate the request early with a response
- `options` — resolved plugin options from `agent.yaml`
- `gw.*` — host APIs (see below)
- Return normally → request continues to upstream

**Common mistake:** Do NOT assign to `ctx.request.path` or `ctx.request.url` directly — it silently does nothing. Use `ctx.request.setPath()` instead.

## Writing a Route Handler

Routes expose HTTP endpoints on the gateway, mounted at `/plugins/<plugin-name>/<path>`.

```typescript
// src/callback.ts
export default function(ctx: any, options: any) {
  const query = ctx.request.query || "";
  const params = new URLSearchParams(query);
  const code = params.get("code");

  if (!code) {
    ctx.response.status(400);
    ctx.response.body("missing code parameter");
    return;
  }

  ctx.response.status(200);
  ctx.response.header("Content-Type", "text/html; charset=utf-8");
  ctx.response.body("<h1>Success</h1>");
}
```

## Host APIs

TypeScript middleware and route handlers have access to gateway host APIs via the `gw` global:

| API | Methods | Description |
|-----|---------|-------------|
| `gw.crypto` | `sha256(data)`, `hmac(key, data)`, `randomBytes(n)` | Cryptographic primitives |
| `gw.crypto.base64` | `encode(data)`, `decode(data)` | Base64 encoding/decoding |
| `gw.crypto.base64url` | `encode(data)`, `decode(data)` | URL-safe base64 |
| `gw.fs` | `read(path)`, `write(path, data)` | File I/O scoped to plugin data dir |
| `gw.http` | `fetch(url, opts)` | Synchronous HTTP client |
| `gw.secrets` | `register(value)` | Mark a value for scrubbing from logs |
| `gw.log` | `info(msg)`, `error(msg)`, `debug(msg)` | Structured logging |

## Testing a Plugin

```bash
# Build from source and generate artifacts
agent-sandbox --dev -C examples/local-coding generate

# Inspect .build/ for correctness:
#   Dockerfile — verify extra_builds lines
#   docker-compose.yml — verify volumes, services
#   config.yaml — verify gateway middleware entries

# Full end-to-end (requires Docker)
agent-sandbox -C examples/local-coding compose up --build

# Edit TypeScript, then re-run generate + compose up to test changes
```

## Example: Credential Injection Plugin

Walk-through of building a simple credential injection plugin from scratch.

**1. Create the plugin directory:**

```
core/plugins/example-auth/
  plugin.yaml
  src/
    example-auth.ts
```

**2. Write `plugin.yaml`:**

```yaml
name: example-auth
options:
  token:
    type: string
    required: true
    description: "API token env var reference (e.g. ${EXAMPLE_TOKEN})"
contributes:
  gateway:
    services:
      - url: "https://api.example.com"
    middlewares:
      - script: "./src/example-auth.ts"
        domains: ["api.example.com"]
```

**3. Write the middleware** (`src/example-auth.ts`):

```typescript
/// <reference path="../../.build/gateway.d.ts" />

const handler: MiddlewareHandler = (ctx, options) => {
  const token = options.token;
  if (!token) return;

  ctx.request.setHeader("Authorization", "Bearer " + token);
  gw.secrets.register(token);
};
export default handler;
```

**4. Configure in `agent.yaml`:**

```yaml
installations:
  - plugin: "@builtin/example-auth"
    options:
      token: "${EXAMPLE_TOKEN}"
```

**5. Test:**

```bash
agent-sandbox --dev generate
# Check .build/config.yaml for api.example.com service entry + middleware
agent-sandbox compose up --build
```

## Common Patterns

**Domain-scoped middleware** — only intercept requests to specific hosts:

```yaml
middlewares:
  - script: "./src/auth.ts"
    domains: ["api.github.com", "github.com"]
```

**Secret registration** — prevent credential leaks in gateway logs:

```typescript
gw.secrets.register(token);
gw.secrets.register(refreshToken);
```

**OAuth lifecycle** — persist tokens between requests:

```typescript
const stored = gw.fs.read("tokens.json");
const tokens = stored ? JSON.parse(stored) : {};
// ... refresh logic ...
gw.fs.write("tokens.json", JSON.stringify(tokens));
```

**Dynamic service lists** — iterate over user-provided config:

```yaml
contributes:
  gateway:
    services:
{{- range $name, $cfg := .plugin.options.providers }}
      - url: "{{ index $cfg "url" }}"
{{- end }}
```

**Asset bundling** — include sidecar build contexts:

```yaml
assets:
  - my-sidecar/
contributes:
  runtime:
    extra_builds:
      - "COPY {{ asset \"my-sidecar\" }}/ /opt/sidecar/"
```
