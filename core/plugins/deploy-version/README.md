# @builtin/deploy-version

Injects project and core version information as container environment variables at compose level (no image rebuild needed — just restart).

## Usage

```yaml
installations:
  - plugin: "@builtin/deploy-version"
    options:
      config_version_key: CONFIG_VERSION   # default
      core_version_key: CORE_VERSION       # default
```

## What it does

At `agent-sandbox generate` time:

1. Runs `git describe --tags --always` in your config repo → `CONFIG_VERSION`
2. Reads the resolved core binary version → `CORE_VERSION`

These are injected as docker-compose environment variables (not baked into the Docker image), so updating only requires regenerate + restart.

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `config_version_key` | string | `CONFIG_VERSION` | Env var name for the config repo git version |
| `core_version_key` | string | `CORE_VERSION` | Env var name for the core binary version |

## Template features used

- `{{ call .plugin.gitDescribe }}` — plugin-scoped computed function (executes `scripts/git-describe.sh`)
- `{{ .generator.core_version }}` — framework-provided value (always available to all plugins)

## Example output

In the generated docker-compose.yml:
```yaml
environment:
  CONFIG_VERSION: "v1.2.3"
  CORE_VERSION: "v1.41.0"
```

## Adding custom computed functions

This plugin demonstrates the `functions:` pattern. To add a new computed value:

1. Write a shell script in `scripts/` (receives `PROJECT_DIR` and `CORE_VERSION` as env vars)
2. Declare it in `plugin.yaml` under `functions:`
3. Reference it via `{{ call .plugin.<name> }}` in the contributes template
