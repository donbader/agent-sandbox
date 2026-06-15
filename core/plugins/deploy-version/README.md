# @builtin/deploy-version

Injects project and core version information into the agent container.

- **Config version** — git commit hash, computed at Docker build time (reads .git from build context)
- **Core version** — agent-sandbox binary version, injected as compose environment variable

## Usage

```yaml
installations:
  - plugin: "@builtin/deploy-version"
```

## How it works

At `docker compose up --build`:
1. The Dockerfile COPYs `.git` from the build context into a temp location
2. Runs `git rev-parse --short HEAD` to get the commit hash
3. Writes it to `/etc/deploy-version` (configurable)
4. Removes `.git` from the image

The core version is injected as a compose-level environment variable (`CORE_VERSION`).

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `version_file` | string | `/etc/deploy-version` | File path where git hash is written at build time |
| `core_version_key` | string | `CORE_VERSION` | Env var name for the core binary version |

## Reading version in your app

```typescript
import { readFileSync } from "node:fs";

const configVersion = readFileSync("/etc/deploy-version", "utf-8").trim();
const coreVersion = process.env.CORE_VERSION ?? "unknown";
```

## Requirements

- Build context must include `.git` directory (don't exclude it in .dockerignore)
- `git` must be available in the build image (included in `@builtin/pi` and other presets)
