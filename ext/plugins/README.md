# External Plugins

This directory holds external plugins that are not bundled with the CLI binary.
Each plugin has its own version and release cycle.

## Structure

```
ext/plugins/
  <plugin-name>/
    feature.yaml          ← plugin metadata and config schema
    gateway/              ← optional Go gateway handler (compiled during Docker build)
    bridge/               ← optional TypeScript bridge plugin (copied into image)
    CHANGELOG.md          ← version history
```

## Resolution Order

1. `ext/plugins/<name>/` in the user's project directory
2. Embedded core plugins (shipped with CLI binary)

External plugins override embedded ones with the same name.

## Versioning

Each plugin is versioned independently. Future releases will support:
- Plugin registry for discovery
- Version pinning in agent.yaml
- Automatic fetching during `agent-sandbox generate`

## Creating a Plugin

```yaml
# ext/plugins/my-feature/feature.yaml
name: my-feature
description: What this plugin does

config_schema:
  properties:
    api_key:
      type: string
      description: API key for the service
```

See `internal/plugins/custom-runtime/feature.yaml` for a complete example.
