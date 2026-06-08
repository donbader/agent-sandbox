# Plugin Development Guide

## Plugin Types

| Type | Location | When to use |
|------|----------|-------------|
| Runtime preset | `core/presets/<name>/runtime.yaml` | New agent runtime (base image + CLI install) |
| Feature plugin | `core/plugins/<name>/plugin.yaml` | Credential injection, gateway rules, home customization |

Runtime presets are pure YAML — no code. Feature plugins are YAML with optional Go gateway middleware.

## Directory Structure

```
core/plugins/<name>/
  plugin.yaml              ← required: metadata, config schema, contributions
  middlewares/
    <name>.go              ← optional: Go gateway middleware (compiled during Docker build)
```

Feature plugins are fetched from GitHub Releases as part of the core tarball. For local development, plugins in `core/plugins/` are packaged by the `core-release.yml` workflow on `core-v*` tag push.

## plugin.yaml Schema

```yaml
name: my-plugin

requires:
  - "@builtin/agent-manager-acp"   # fails at generate-time if not installed

assets:
  - agent-manager/                  # directories bundled with the plugin

options:
  token:
    type: string           # string | boolean | array
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
      - "ENV MY_TOKEN=dummy"                    # Dockerfile RUN/ENV/COPY lines
    volumes:
      - "my-data:/data"                         # volume mounts (Go template supported)
  gateway:
    services:
      - url: "https://api.example.com"
        middlewares:
          - custom: "./middlewares/my-auth.go"  # path relative to plugin dir
  sidecar:
    services:
      my-adapter:
        build: ./my-adapter
        environment:
          AGENT_URL: "ws://agent:3100/acp"
```

### `options` fields

| Field | Required | Description |
|-------|----------|-------------|
| `type` | yes | `string`, `boolean`, or `array` |
| `required` | yes | Whether the user must provide a value |
| `default` | no | Default value (for optional fields) |
| `description` | no | Human-readable description |

### `requires` field

Declares dependencies on other plugins. Validation fails at generate-time if a required plugin is not installed.

```yaml
requires:
  - "@builtin/agent-manager-acp"
```

### `assets` field

Declares directories bundled with the plugin. These directories are packaged alongside the plugin and extracted during generation. In templates, use `{{ asset "name" }}` to reference the extracted path.

```yaml
assets:
  - agent-manager/
contributes:
  runtime:
    extra_builds:
      - "COPY {{ asset \"agent-manager\" }}/ /opt/src/"
```

### `contributes` fields

| Field | Description |
|-------|-------------|
| `runtime.extra_builds` | Lines appended to the Dockerfile after the base install |
| `runtime.volumes` | Volume mount specs for docker-compose |
| `gateway.services` | Services the gateway intercepts. Each entry has a `url` and a list of `middlewares` |
| `gateway.services[].middlewares[].custom` | Path to a Go middleware file, relative to the plugin directory |
| `gateway.routes` | HTTP route handlers registered on the gateway (see below) |
| `sidecar.services` | Additional Docker containers that run alongside the agent (see below) |

All `contributes` fields support Go `text/template` syntax. See **Template Context** below for available variables and functions.

### `contributes.gateway.routes`

Registers HTTP route handlers directly on the gateway. Routes are automatically namespaced under `/plugins/{plugin-name}/` to prevent conflicts between plugins.

```yaml
contributes:
  gateway:
    routes:
      - path: "/callback"
        handler: "./handlers/callback.go"
```

| Field | Description |
|-------|-------------|
| `path` | Relative path (prefixed automatically with `/plugins/{plugin-name}`) |
| `handler` | Path to a Go handler file, relative to the plugin directory |

The handler file is a Go template (same as middleware) with access to `{{ .path }}` (the full namespaced path) and `{{ .options }}`. It must self-register using `gateway.RegisterRoute()` in an `init()` function:

```go
package custom

import (
    "net/http"
    "github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func init() {
    gateway.RegisterRoute(gateway.RouteDef{
        Path:    "{{ .path }}",
        Handler: myHandler,
    })
}

func myHandler(w http.ResponseWriter, r *http.Request) {
    // Handle the request
}
```

### `contributes.sidecar.services`

Defines additional Docker containers that run alongside the agent. Each sidecar is a separate compose service. Sidecars automatically get `depends_on: { agent: { condition: service_healthy } }` so they start only after the agent is healthy.

The agent service has a network alias of `agent`, so sidecars can reach it by hostname.

```yaml
contributes:
  sidecar:
    services:
      my-adapter:
        build: ./my-adapter
        environment:
          AGENT_URL: "ws://agent:3100/acp"
```

Each entry under `services` follows Docker Compose service syntax (build, image, environment, ports, volumes, etc.).

## Template Context

All `contributes` fields support Go `text/template` syntax. The template receives the following data:

### Variables

| Expression | Description | Example |
|------------|-------------|---------|
| `{{ .plugin.options.<field> }}` | User-provided plugin option (with defaults applied) | `{{ .plugin.options.port }}` |
| `{{ .agent.name }}` | Agent name from `agent.yaml` | `{{ .agent.name }}` |
| `{{ .agent.runtime.image }}` | Agent base image | `{{ .agent.runtime.image }}` |
| `{{ .agent.core_version }}` | Core version | `{{ .agent.core_version }}` |
| `{{ .agent.gateway }}` | Gateway config block | `{{ .agent.gateway }}` |

`.agent` is the full agent config (`agent.yaml`) as a nested map. Any field in the config is accessible — the examples above are just the most common ones.

**Example — per-agent named volume (no naming conflicts in fleet mode):**

```yaml
contributes:
  runtime:
    volumes:
      - "{{ .agent.name }}-home:/home/agent"
```

**Example — conditional behavior based on runtime image:**

```yaml
contributes:
  runtime:
    extra_builds:
      - "RUN echo 'agent={{ .agent.name }} image={{ .agent.runtime.image }}'"
```

### Functions

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON` | Serializes any value to a JSON string | `{{ toJSON .plugin.options.my_config }}` |
| `asset` | Returns the extracted path of a declared asset | `{{ asset "agent-manager" }}` |
| `index` | Access map keys containing special characters | `{{ index .plugin.options "my-key" }}` |

**`toJSON` example:**

```yaml
extra_builds:
  - "RUN echo '{{ toJSON .plugin.options.commands }}' > /etc/config.json"
```

This is useful for passing structured option values (objects, arrays) into config files during the Docker build.

**`asset` example:**

```yaml
assets:
  - agent-manager/
contributes:
  runtime:
    extra_builds:
      - "COPY {{ asset \"agent-manager\" }}/ /opt/src/"
```

## Writing a Gateway Middleware

Gateway middlewares are Go files compiled into the gateway binary during Docker build (not during CLI build). Users do not need Go installed.

A middleware implements the `sdk.Middleware` interface:

```go
//go:build ignore

package main

import (
    "net/http"
    "github.com/donbader/agent-sandbox/core/sdk"
)

type MyAuthMiddleware struct {
    token string
}

func (m *MyAuthMiddleware) HandleRequest(req *http.Request) error {
    req.Header.Set("Authorization", "Bearer "+m.token)
    return nil
}

func New(config map[string]any) sdk.Middleware {
    return &MyAuthMiddleware{
        token: sdk.EnvOrString(config, "token"),
    }
}
```

- The `//go:build ignore` tag prevents the Go toolchain from compiling the file directly — the gateway build system handles it.
- The `New` function is the entry point. `config` receives the plugin's resolved options.
- `sdk.EnvOrString` resolves `${ENV_VAR}` references to actual environment variable values at runtime.

See `core/plugins/github-pat/middlewares/` for a working example.

## Testing a Plugin

1. Create a minimal `agent.yaml` that uses your plugin:

```yaml
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
installations:
  - plugin: ./plugins/my-plugin
    options:
      token: "${MY_TOKEN}"
```

2. Run generate and inspect the output:

```bash
flox activate -- agent-sandbox generate -C ./testdata/my-plugin-test/
```

3. Check `.build/` for correctness:
   - `Dockerfile` — verify your `extra_builds` lines appear in the right order
   - `docker-compose.yml` — verify volumes are declared correctly
   - `config.yaml` — verify gateway service + middleware entries are present

4. For full end-to-end validation (requires Docker):

```bash
flox activate -- agent-sandbox compose up --build
```

Use `//go:build integration` on tests that require Docker. Run with `go test -tags integration ./...`.

## Example: Credential Injection Plugin

**Goal:** Inject a Bearer token into requests to `https://api.example.com`.

**1. Create the plugin directory:**

```
core/plugins/example-auth/
  plugin.yaml
  middlewares/
    example-auth.go
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
          - custom: "./middlewares/example-auth.go"
```

**3. Write the middleware** (`middlewares/example-auth.go`) following the pattern in the section above.

**4. Use in agent.yaml:**

```yaml
installations:
  - plugin: "@builtin/example-auth"
    options:
      token: "${EXAMPLE_TOKEN}"
```

**5. Verify:** run `agent-sandbox generate` and confirm `config.yaml` contains the `api.example.com` service entry with the middleware wired in.
