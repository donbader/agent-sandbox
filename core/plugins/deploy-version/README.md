# @builtin/deploy-version

Injects the agent-sandbox core version as a container environment variable at compose level (no image rebuild needed — just restart).

## Usage

```yaml
installations:
  - plugin: "@builtin/deploy-version"
    options:
      core_version_key: CORE_VERSION   # default
```

## What it does

At `agent-sandbox generate` time, reads the resolved core binary version and injects it as a docker-compose environment variable.

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `core_version_key` | string | `CORE_VERSION` | Env var name for the core binary version |

## Note

Config repo versioning is intentionally left to the config repo itself (as a local plugin), since format preferences vary per project.
