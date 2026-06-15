# @builtin/deploy-version

Injects project and core version information as compose-level environment variables. Values are computed at `generate` time and written directly into the docker-compose.yml — no `.env` file or Docker build args needed.

## Usage

```yaml
installations:
  - plugin: "@builtin/deploy-version"
```

## What it does

At `agent-sandbox generate` time:

1. Runs `git rev-parse --short HEAD` in the config repo → `CONFIG_VERSION`
2. Uses the resolved core binary version → `CORE_VERSION`

Both are injected as docker-compose environment variables. To update, regenerate and restart (no image rebuild needed).

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `config_version_key` | string | `CONFIG_VERSION` | Env var name for the config repo git hash |
| `core_version_key` | string | `CORE_VERSION` | Env var name for the core binary version |

## Example output

In the generated docker-compose.yml:
```yaml
environment:
  CONFIG_VERSION: "abcdef1"
  CORE_VERSION: "v1.42.0"
```

## Reading in your app

```typescript
const configVersion = process.env.CONFIG_VERSION ?? "unknown";
const coreVersion = process.env.CORE_VERSION ?? "unknown";
```
