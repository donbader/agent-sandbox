# V1Config Schema Generation + Old Config Cleanup

## Summary

Remove the obsolete `AgentConfig` struct and its `Load()` function. Unify on `V1Config` as the single config model. Generate `.build/schema.json` from V1Config struct tags during the `generate` command using `invopop/jsonschema`.

## Context

The codebase has two competing config structs:
- `AgentConfig` — an old simplified format with `runtime: codex` (flat string) and `gateway: bool`. Never used by the generator.
- `V1Config` — the real format matching the [V1 Architecture Redesign spec](./2025-06-04-v1-architecture-redesign.md). Used by `LoadV1()` and the generate pipeline.

The `init` command produces `AgentConfig`-shaped yaml that the `generate` command cannot parse. The `# yaml-language-server: $schema=.build/schema.json` comment references a file that is never created.

## Changes

### 1. Remove `AgentConfig` and associated code

- Delete `AgentConfig` struct from `internal/config/config.go`
- Delete `Load()` function (the one that returns `*AgentConfig`)
- Rename `LoadV1()` to `Load()` — it becomes the only config loader
- Rename `V1Config` to `Config` (it's the only format now)
- Remove or update any tests that reference `AgentConfig` or old `Load()`

### 2. Fix `init` command

Update the init template in `cmd/agent-sandbox/main.go` to emit V1-shaped yaml:

```yaml
# yaml-language-server: $schema=.build/schema.json
name: my-agent
core_version: v1.0.0
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
gateway:
  services: []
installations: []
```

### 3. Add JSON Schema struct tags to Config

Annotate the config structs with `jsonschema` tags for documentation and validation:

```go
type Config struct {
    Name        string          `yaml:"name" jsonschema:"required,description=Agent instance name"`
    LogLevel    string          `yaml:"log_level" jsonschema:"enum=info,enum=debug,description=Logging verbosity"`
    CoreVersion string          `yaml:"core_version" jsonschema:"description=Core version to use for generation"`
    Runtime     RuntimeConfig   `yaml:"runtime" jsonschema:"required,description=Agent container configuration"`
    Gateway     GatewayConfig   `yaml:"gateway" jsonschema:"description=Transparent egress proxy configuration"`
    Installations []Installation `yaml:"installations" jsonschema:"description=Plugins to install"`
}

type RuntimeConfig struct {
    Image       string   `yaml:"image" jsonschema:"required,description=Base image or @builtin preset"`
    ExtraBuilds []string `yaml:"extra_builds" jsonschema:"description=Additional Dockerfile instructions"`
    Entrypoint  []string `yaml:"entrypoint" jsonschema:"description=Container CMD override"`
    Volumes     []string `yaml:"volumes" jsonschema:"description=Named or bind mount volumes"`
}

type GatewayConfig struct {
    Services []GatewayServiceEntry `yaml:"services" jsonschema:"description=External services proxied through gateway"`
}

type GatewayServiceEntry struct {
    URL         string            `yaml:"url" jsonschema:"required,description=Service endpoint: HTTPS URL (https://api.example.com) or internal host:port (sidecar:8080)"`
    Network     string            `yaml:"network" jsonschema:"description=Compose network to attach (optional, defaults to sandbox network)"`
    Headers     map[string]string `yaml:"headers" jsonschema:"description=Headers injected on every proxied request"`
    Middlewares []MiddlewareEntry `yaml:"middlewares" jsonschema:"description=Custom middleware chain"`
}

type MiddlewareEntry struct {
    Custom string `yaml:"custom" jsonschema:"description=Path to custom middleware .go file"`
}

type Installation struct {
    Plugin  string         `yaml:"plugin" jsonschema:"required,description=Plugin name"`
    Source  string         `yaml:"source" jsonschema:"description=Plugin source (local path or remote URL)"`
    Options map[string]any `yaml:"options" jsonschema:"description=Plugin-specific options"`
}
```

### 4. Schema generation in `generate` pipeline

Add a step at the end of the generate pipeline:

```go
import "github.com/invopop/jsonschema"

func generateSchema(outDir string) error {
    reflector := &jsonschema.Reflector{
        DoNotReference: true,
    }
    schema := reflector.Reflect(&config.Config{})
    schema.Title = "agent-sandbox configuration"
    schema.Description = "Configuration schema for agent-sandbox agent.yaml"

    data, err := json.MarshalIndent(schema, "", "  ")
    if err != nil {
        return fmt.Errorf("marshal schema: %w", err)
    }
    return os.WriteFile(filepath.Join(outDir, "schema.json"), data, 0644)
}
```

This runs after all other `.build/` artifacts are generated.

### 5. Dependency

Add `github.com/invopop/jsonschema` to `go.mod`.

## Scope Exclusions

- No migration tooling for old `AgentConfig` format
- No standalone `agent-sandbox schema` subcommand
- No runtime validation against schema (existing yaml parsing + Go type system handles this)
- Plugin option schema validation is a separate concern (per V1 arch spec)

## Testing

- Existing V1Config tests continue to work (with renamed types)
- Add test: `generate` produces a valid JSON Schema in `.build/schema.json`
- Add test: generated schema validates the example `agent.yaml` files
- Remove tests that reference `AgentConfig` or old `Load()`
