# Plugin Authoring

Plugins are TypeScript + YAML. No compilation step — scripts are loaded by the gateway at runtime.

## Plugin Structure

```
my-plugin/
  plugin.yaml          # metadata, options, contributions
  src/
    middleware.ts      # middleware handler(s)
    route-handler.ts   # route handler(s)
```

## plugin.yaml Schema

```yaml
name: my-plugin

options:
  token:
    type: string          # string | object | boolean | integer | project-path
    required: true
    description: "Description shown in docs"
  data_dir:
    type: string
    required: false
    default: "/data/my-plugin"

contributes:
  gateway:
    egress:                            # egress rules (same format as user config)
      - hosts: ["api.example.com"]
        middlewares:                    # intercept proxied requests
          - "./src/auth.ts"
    namespaced_volumes:            # auto-prefixed with {agentName}-
      - "my-data:{{ .plugin.options.data_dir }}"
    routes:                        # expose HTTP endpoints on the gateway
      - path: "/callback"
        handler: "./src/callback.ts"

  runtime:
    extra_builds:                   # injected into the agent Dockerfile
      - "ENV MY_TOKEN=dummy"
    environment:                    # compose-level env vars (no rebuild needed)
      MY_VAR: "my-value"
```

**Template expressions** — YAML values support Go templates. Available namespaces:

| Namespace | Description | Example |
|-----------|-------------|----------|
| `.plugin.options.*` | User-provided plugin options | `{{ .plugin.options.token }}` |
| `.agent.*` | Agent config fields | `{{ .agent.name }}`, `{{ .agent.runtime.cwd }}` |
| `.generator.*` | Framework-provided values | `{{ .generator.core_version }}` |
| `call .plugin.<fn>` | Plugin-computed functions | `{{ call .plugin.gitDescribe }}` |

**Built-in template functions:** `toJSON`, `asset`, `index` (Go built-in).

Dynamic egress list example:

```yaml
egress:
{{- range $name, $cfg := .plugin.options.providers }}
  - hosts: ["{{ index $cfg "host" }}"]
{{- end }}
```

## Plugin Computed Functions

Plugins can declare computed functions that execute shell scripts at generate time. This is useful for injecting dynamic values (like git versions) that aren't known until generation.

```yaml
name: my-plugin

functions:
  gitDescribe:
    script: "./scripts/git-describe.sh"

options:
  version_key:
    type: string
    default: "APP_VERSION"

contributes:
  runtime:
    environment:
      "{{ .plugin.options.version_key }}": "{{ call .plugin.gitDescribe }}"
```

**How it works:**
1. Declare functions in `functions:` with a `script:` path (relative to plugin directory)
2. At generate time, the framework executes each script with `sh`
3. Scripts receive `PROJECT_DIR` and `CORE_VERSION` as environment variables
4. Script stdout (trimmed) becomes the function's return value
5. Access via `{{ call .plugin.<name> }}` in the contributes template

If the script exits non-zero, the value defaults to `"unknown"`.

## Writing a Middleware Handler

A middleware intercepts proxied requests before they reach the upstream service.

```typescript
// src/github-auth.ts
export default function(ctx: any, options: any) {
  const token = options.token;
  if (!token) return;

  const basic = gw.crypto.base64.encode("x-access-token:" + token);
  ctx.request.setHeader("Authorization", "Basic " + basic);
  gw.secrets.register(token);
}
```

**Signature:** `export default function(ctx, options) { ... }`

- `ctx` — the request context (see Host APIs below)
- `options` — resolved plugin options from `agent.yaml`

**Behavior:**
- Return normally → request continues to upstream
- Call `ctx.abort(status, body)` → request is terminated with the given response

Middlewares are scoped to the hosts defined in their parent egress rule — they only fire for requests matching those hosts.

## Writing a Route Handler

Routes expose HTTP endpoints directly on the gateway (e.g. OAuth callbacks).

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

  // Exchange code for token...
  ctx.response.status(200);
  ctx.response.header("Content-Type", "text/html; charset=utf-8");
  ctx.response.body("<h1>Success</h1>");
}
```

Route handlers use `ctx.response.*` to build the response. The path in `plugin.yaml` is mounted under `/plugins/<plugin-name>/`.

## Host APIs

The `gw` global and `ctx` object are injected by the gateway runtime.

### ctx.request

| Property/Method | Description |
|----------------|-------------|
| `ctx.request.method` | HTTP method (GET, POST, etc.) |
| `ctx.request.url` | Full request URL |
| `ctx.request.host` | Request hostname |
| `ctx.request.path` | URL path |
| `ctx.request.query` | Raw query string |
| `ctx.request.headers` | Header map (lowercase keys) |
| `ctx.request.setHeader(name, value)` | Set/overwrite a request header |

### ctx.abort(status, body)

Terminates the request immediately with the given HTTP status and body. Use in middlewares to block requests (e.g. return 401 when no token exists).

```typescript
ctx.abort(401, JSON.stringify({ error: "oauth_required", authorize_url: url }));
```

### ctx.response (route handlers only)

| Method | Description |
|--------|-------------|
| `ctx.response.status(code)` | Set HTTP status code |
| `ctx.response.header(name, value)` | Set response header |
| `ctx.response.body(content)` | Set response body (string) |

### ctx.env(key)

Read an environment variable from the gateway process.

### gw.crypto

| Method | Description |
|--------|-------------|
| `gw.crypto.sha256(data, encoding?)` | SHA-256 hash. Returns hex by default. |
| `gw.crypto.hmac(key, data)` | HMAC-SHA256. Returns hex. |
| `gw.crypto.randomBytes(n)` | Cryptographically random bytes (hex string). |
| `gw.crypto.base64.encode(data)` | Base64 encode |
| `gw.crypto.base64.decode(data)` | Base64 decode |
| `gw.crypto.base64url.encode(data)` | Base64url encode (no padding) |
| `gw.crypto.base64url.decode(data)` | Base64url decode |

### gw.fs

File I/O scoped to the plugin's data directory (the volume mount path).

| Method | Description |
|--------|-------------|
| `gw.fs.read(path)` | Read file contents as string |
| `gw.fs.write(path, data)` | Write string to file |

```typescript
const token = JSON.parse(gw.fs.read("provider.json"));
gw.fs.write("provider.json", JSON.stringify(token, null, 2));
```

### gw.http

| Method | Description |
|--------|-------------|
| `gw.http.fetch(url, opts)` | Synchronous HTTP request |

`opts`: `{ method: string, body?: string, headers?: Record<string, string> }`

Returns: `{ status: number, headers: Record<string, string>, body: string }`

```typescript
const resp = gw.http.fetch("https://oauth.example.com/token", {
  method: "POST",
  body: "grant_type=authorization_code&code=" + code,
  headers: { "Content-Type": "application/x-www-form-urlencoded" },
});
if (resp.status !== 200) throw new Error("token exchange failed");
const token = JSON.parse(resp.body);
```

### gw.secrets

| Method | Description |
|--------|-------------|
| `gw.secrets.register(value)` | Register a value for scrubbing from logs/responses |

Call this for any credential you inject so it never leaks in gateway logs.

### gw.log

| Method | Description |
|--------|-------------|
| `gw.log.info(msg)` | Info-level log |
| `gw.log.error(msg)` | Error-level log |
| `gw.log.debug(msg)` | Debug-level log |

## Options

Options declared in `plugin.yaml` are resolved from the user's `agent.yaml`:

```yaml
# agent.yaml
installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "${GITHUB_PAT}"
```

**Env var expansion** — String values in gateway `services[].headers` support `${ENV_VAR}` syntax, resolved at compose runtime from the `.env` file on the deployment machine.

> **Important:** Plugin options used in `contributes.runtime.extra_builds` are rendered at **generate time** via Go templates. If you use `${VAR}` inside a plugin option value (e.g. in an `object`-type option), it will be baked **literally** into the Dockerfile — the shell variable will NOT be expanded at build time because:
>
> 1. The template engine (`toJSON`) outputs `${VAR}` as a literal string
> 2. `RUN echo '...'` with single quotes prevents shell expansion
> 3. Docker build doesn't have access to compose `.env` vars
>
> Use literal values for plugin options that get baked into the image. The CLI will emit a warning if it detects unresolved `${VAR}` patterns in rendered `extra_builds` lines.

Option types: `string`, `object`, `boolean`, `integer`, `project-path`. The `required` and `default` fields control validation.

- `project-path` — a filesystem path within the project. Must use the `@fleet/` prefix (e.g. `@fleet/shared-home`). Resolved relative to the project root at generate time.

## Development Workflow

Use `--dev` to build from source and auto-compile the gateway binary for your Docker platform:

```bash
agent-sandbox --dev -C examples/local-coding generate
```

This bypasses the GitHub Releases fetch and uses plugins directly from `core/plugins/`. The gateway binary is cross-compiled automatically for the Docker daemon's architecture. Edit TypeScript, re-run `generate`, and `compose up --build` to test changes.

## Examples

### github-pat (simple middleware)

Injects GitHub PAT as HTTP Basic auth on all requests to `github.com` and `api.github.com`.

- [`core/plugins/github-pat/plugin.yaml`](../core/plugins/github-pat/plugin.yaml)
- [`core/plugins/github-pat/src/github-auth.ts`](../core/plugins/github-pat/src/github-auth.ts)

Key patterns: single middleware with domain filter, `gw.crypto.base64.encode`, `gw.secrets.register`.

### mcp-oauth (complex multi-handler)

Full OAuth lifecycle: token injection middleware, login route (PKCE), callback route (code exchange), status checking, disconnect with revocation, dynamic client registration, token refresh.

- [`core/plugins/mcp-oauth/plugin.yaml`](../core/plugins/mcp-oauth/plugin.yaml)
- [`core/plugins/mcp-oauth/src/oauth.ts`](../core/plugins/mcp-oauth/src/oauth.ts) — middleware
- [`core/plugins/mcp-oauth/src/login.ts`](../core/plugins/mcp-oauth/src/login.ts) — login route
- [`core/plugins/mcp-oauth/src/callback.ts`](../core/plugins/mcp-oauth/src/callback.ts) — callback route
- [`core/plugins/mcp-oauth/src/status.ts`](../core/plugins/mcp-oauth/src/status.ts) — status route
- [`core/plugins/mcp-oauth/src/disconnect.ts`](../core/plugins/mcp-oauth/src/disconnect.ts) — disconnect route

Key patterns: multiple routes + middleware, `gw.http.fetch` for token exchange, `gw.fs` for token persistence, `ctx.abort` for auth gating, `ctx.response.*` for route responses.

### flox (runtime-only plugin)

Installs Nix and Flox in the agent container. Pure runtime contributions — no gateway middleware or routes.

- [`core/plugins/flox/plugin.yaml`](../core/plugins/flox/plugin.yaml)
- [`core/plugins/flox/README.md`](../core/plugins/flox/README.md)

Key patterns: `extra_builds` for Dockerfile commands, `pre_entrypoint` for shell setup, conditional volume mount with Go templates.

### agent-docker (sidecar with Go binary)

Policy-enforced Docker access via a Go proxy sidecar. Validates every Docker API call against image allowlists, resource limits, and security rules.

- [`core/plugins/agent-docker/plugin.yaml`](../core/plugins/agent-docker/plugin.yaml)
- [`core/plugins/agent-docker/README.md`](../core/plugins/agent-docker/README.md)
- [`core/plugins/agent-docker/cmd/docker-proxy/`](../core/plugins/agent-docker/cmd/docker-proxy/) — Go sidecar binary

Key patterns: `contributes.sidecar.services` for a sidecar container, `contributes.runtime.environment` for injecting DOCKER_HOST, asset bundling with `{{ asset "cmd/docker-proxy" }}`, conditional `pre_entrypoint` with Go templates.
