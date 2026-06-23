# agent-manager-acp

ACP (Agent Client Protocol) proxy that spawns an agent process and exposes it over stdio for ACP clients like OpenACP.

## How It Works

When spawned by a parent process (e.g., OpenACP):

1. Reads `config.json` for the `acp_command` and working directory
2. Spawns the configured agent as a child process
3. Communicates with it via ACP over stdio (JSON-RPC 2.0)
4. Performs the ACP handshake (initialize + authenticate)
5. Relays ACP messages between parent (stdin/stdout) and agent child process

## Usage

```yaml
installations:
  - plugin: "@builtin/agent-manager-acp"
    options:
      acp_command: ["codex-acp"]
```

## Options

| Option | Type | Required | Default | Description |
|--------|------|----------|---------|-------------|
| `acp_command` | array | yes | — | Command to spawn the agent via ACP over stdio |

### Common `acp_command` values

| Runtime | Command |
|---------|---------|
| Codex | `["codex-acp"]` |
| Claude Code | `["claude-agent-acp"]` |
| Pi | `["pi-acp"]` |

## What It Contributes

- **Runtime (build):** Copies agent-manager TypeScript source, compiles it, writes `config.json` with the ACP command and working directory

## Architecture

```
OpenACP / ACP Client (parent process)
    │ ACP over stdio (JSON-RPC 2.0)
    ▼
Agent Manager (spawned as subprocess)
    │ ACP over stdio (JSON-RPC 2.0)
    ▼
Agent Process (codex-acp, claude-agent-acp, pi-acp, etc.)
    │ HTTPS (transparent proxy)
    ▼
Gateway (MITM + credential injection)
```

## Stdio Relay

The agent-manager acts as a transparent relay between its parent's stdin/stdout and the agent child process, with these interceptions:

- **initialize**: Returns cached init result (agent already initialized at startup)
- **auth/authenticate**: Returns empty success (auth handled at startup)
- **session/new**: Injects `cwd` from config
- **/restart**: Restarts the agent process
- **Error surfacing**: Detects auth errors, rate limits, and upstream failures from agent stderr

## Protocol

The parent process communicates via ndjson (newline-delimited JSON-RPC 2.0) on stdin/stdout. All agent logs go to stderr to keep the stdio channel clean for ACP traffic.

## Dependencies

This plugin is required by channel adapters. If a plugin declares `requires: ["@builtin/agent-manager-acp"]` and this plugin is not installed, generation fails with an error.
