# Logging Conventions

Structured logging standards for agent-sandbox. Both Go (gateway) and TypeScript (channel-manager) sides follow these conventions.

## Log Levels

| Level | When to use | Production visibility |
|-------|-------------|----------------------|
| `debug` | Per-operation tracing: HTTP fetches, TLS handshakes, per-request routing, internal state transitions. High volume. | Only with `LOG_LEVEL=debug` |
| `info` | Lifecycle milestones and meaningful state changes. Things an operator wants to see in production without drowning in noise. | Always visible (default level) |
| `warn` | Degraded but recoverable state. The system can continue, but something is wrong and may need attention. | Always visible |
| `error` | Operation failed with user-visible impact. Requires investigation. | Always visible |
| `fatal` | Process will exit. Unrecoverable. (TypeScript only — Go uses `slog.Error` + `os.Exit`.) | Always visible |

### Decision guide

Ask yourself: **"Would I want to see this in production logs without explicitly enabling debug mode?"**

- **Yes, always** → `info` (if positive/neutral) or `error` (if failure)
- **Only when debugging** → `debug`
- **System is degraded but working** → `warn`

### Common mistakes

| Wrong | Right | Why |
|-------|-------|-----|
| `info` for every HTTP request | `debug` for every HTTP request | Per-request tracing is high-volume noise at info level |
| `error` for expected failures (e.g., token file not yet created) | `warn` or `debug` | Reserve `error` for unexpected failures that need investigation |
| `debug` for "service started" | `info` for "service started" | Lifecycle events are always relevant |

## Component Naming

Every log line carries a `component` field identifying its source.

### TypeScript (pino)

```typescript
// Channel-manager internal modules
const log = createLogger("acp-client");
const log = createLogger("safe-prompt");

// Plugins receive a logger via init(), tagged by the plugin system
init(config, logger) {
  this.log = logger;  // component: "plugin:mcp-oauth"
}

// Sub-modules use child loggers
const discoveryLog = this.log.child("discovery");
// component: "plugin:mcp-oauth:discovery"
```

### Go (slog)

```go
// Currently uses bare slog.Info/Debug/Error without component tagging.
// Future: adopt slog.With(slog.String("component", "proxy")) pattern.
```

### Naming format

```
<scope>:<instance>:<submodule>
```

Examples:
- `plugin:mcp-oauth` — plugin instance
- `plugin:mcp-oauth:discovery` — sub-module within plugin
- `acp-client` — channel-manager internal
- `telegram` — channel implementation

## Field Conventions

### Always include

| Field | Type | When |
|-------|------|------|
| `component` | string | Every log line (automatic via `createLogger` / child loggers) |
| `error` | string | On error/warn — the error message, not the Error object |
| `provider` | string | When operating on a specific OAuth/MCP provider |
| `host` | string | When making network requests or handling connections |

### Common fields

| Field | Type | Usage |
|-------|------|-------|
| `chatId` | string | User/session identifier |
| `attempt` | number | Retry attempts |
| `retryIn` | string | Next retry delay |
| `isTimeout` | boolean | Whether failure was a timeout |
| `tokenFile` | string | Path to token file being read/written |
| `count` | number | Quantity of items (commands, plugins, etc.) |

### Field naming rules

- Use `camelCase` for field names
- Use descriptive names: `tokenEndpoint` not `endpoint`, `registrationEndpoint` not `url`
- Error fields contain the **message string**, not the full Error object (avoids serialization issues)
- Never log secret values — pino redacts keys matching `token`, `authorization`, `*.token`, `*.authorization`

## Plugin Logging

Plugins never create their own loggers. The plugin system injects a `PluginLogger` via `init()`:

```typescript
interface PluginLogger {
  debug(data: Record<string, unknown>, msg: string): void;
  info(data: Record<string, unknown>, msg: string): void;
  warn(data: Record<string, unknown>, msg: string): void;
  error(data: Record<string, unknown>, msg: string): void;
  child(subcomponent: string): PluginLogger;
}
```

### Why injection?

- Multiple instances of the same plugin type (e.g., two `mcp-oauth` plugins for different providers) each get a uniquely tagged logger
- Plugins don't depend on any logging library — they code against the interface
- The plugin system controls naming conventions centrally
- Testing is trivial: pass a mock logger

### Plugin logging pattern

```typescript
export class MyPlugin implements CommandPlugin {
  private log!: PluginLogger;

  init(config: Record<string, unknown>, logger: PluginLogger): void {
    this.log = logger;
    this.log.info({ providers: Object.keys(config) }, "plugin initialized");
  }

  private async doWork(): Promise<void> {
    const workLog = this.log.child("worker");
    workLog.debug({ taskId: "abc" }, "starting work");
    // ...
    workLog.error({ taskId: "abc", error: "connection refused" }, "work failed");
  }
}
```

### Passing loggers to helper functions

Helper functions (discovery, registration, etc.) accept a logger parameter:

```typescript
export async function discoverAuthServer(url: string, log: PluginLogger): Promise<Metadata> {
  log.debug({ url }, "fetching metadata");
  // ...
}
```

This keeps helpers testable (pass a mock) and ensures logs carry the correct component context.

## Configuration

| Side | Env Var | Default | Values |
|------|---------|---------|--------|
| TypeScript | `LOG_LEVEL` | `info` | `trace`, `debug`, `info`, `warn`, `error`, `fatal` |
| Go | `LOG_LEVEL` | `info` | `debug`, `info` (binary — known gap) |

Set in `agent.yaml`:
```yaml
log_level: debug
```

## Output Format

Both sides output **JSON to stdout**, one object per line:

```json
{"level":"info","time":1780442704733,"component":"plugin:mcp-oauth","providers":["notion"],"msg":"plugin initialized"}
{"level":"debug","time":1780442704734,"component":"plugin:mcp-oauth:discovery","wellKnownUrl":"https://mcp.notion.com/.well-known/oauth-authorization-server","msg":"fetching metadata"}
{"level":"error","time":1780442704800,"component":"plugin:mcp-oauth","provider":"notion","error":"timeout","isTimeout":true,"msg":"OAuth flow failed during setup"}
```

## Security

- **Key-based redaction** (pino): Fields named `token`, `authorization`, `*.token`, `*.authorization` are replaced with `[Redacted]`
- **Value-based redaction** (Go gateway): All log output is scanned for known secret strings collected from rewriter env vars at startup
- **Never log** full tokens, API keys, secrets, or authorization headers in messages or field values

## Known Gaps

These are acknowledged inconsistencies to address in future work:

1. **Go has no `component` field** — should adopt `slog.With(slog.String("component", ...))` pattern
2. **Go LOG_LEVEL is binary** — only checks `== "debug"`, should support `warn`/`error` filtering
3. **No correlation IDs** — cannot trace a single user request across gateway → channel-manager → agent
4. **TypeScript value-based redaction** — only has key-path redaction, not content scanning like Go
