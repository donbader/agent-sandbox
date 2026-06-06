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

Feature plugins are embedded in the CLI binary via `go:embed`. Rebuild the CLI after adding or modifying a plugin.

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
| `runtime.volumes` | Volume mount specs for docker-compose. Supports Go template expressions using `{{ .options.<field> }}` |
| `gateway.services` | Services the gateway intercepts. Each entry has a `url` and a list of `middlewares` |
| `gateway.services[].middlewares[].custom` | Path to a Go middleware file, relative to the plugin directory |
| `sidecar.services` | Additional Docker containers that run alongside the agent (see below) |

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

## Template Functions

All plugin template contexts (e.g. `contributes.runtime.extra_builds`) support Go text/template syntax with these additional functions:

| Function | Description | Example |
|----------|-------------|---------|
| `toJSON` | Serializes any value to a JSON string | `{{ toJSON .options.my_config }}` |
| `asset` | Returns the extracted path of a declared asset | `{{ asset "agent-manager" }}` |

**`toJSON` example:**

```yaml
extra_builds:
  - "RUN echo '{{ toJSON .options.my_config }}' > /etc/my-config.json"
```

This is useful for passing structured option values (objects, arrays) into config files during the Docker build.

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
runtime: codex
features:
  - plugin: my-plugin
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
features:
  - plugin: example-auth
    token: "${EXAMPLE_TOKEN}"
```

**5. Verify:** run `agent-sandbox generate` and confirm `config.yaml` contains the `api.example.com` service entry with the middleware wired in.
