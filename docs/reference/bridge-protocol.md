# Bridge Protocol

## Overview

The bridge connects AI agents to messaging platforms. It acts as an **ACP client**, spawning the agent's ACP adapter as a subprocess and communicating via JSON-RPC 2.0 over stdio.

```
User ←→ [Messaging Platform] ←→ [Bridge (ACP Client)] ←→ [ACP Adapter] ←→ [Agent]
              Telegram API         @agentclientprotocol/sdk    codex-acp       Codex
```

## ACP (Agent Client Protocol)

ACP is a JSON-RPC 2.0 protocol over stdio for communicating with AI coding agents. It's the industry standard — supported by Codex, Claude Code, Pi, Gemini, Copilot, and others.

- **Spec**: https://agentclientprotocol.com
- **TypeScript SDK**: `@agentclientprotocol/sdk` (used by our bridge)
- **Protocol version**: 1

### Why ACP?

| Feature | ACP | Custom JSON Lines | Raw CLI |
|---------|-----|-------------------|---------|
| Multi-session | ✅ | ❌ | ❌ |
| Structured tool calls | ✅ | ❌ | ❌ |
| Streaming responses | ✅ | ⚠️ | ⚠️ (stdout parsing) |
| Session resume | ✅ | ❌ | ⚠️ (--resume flag) |
| Standard protocol | ✅ | ❌ (proprietary) | ❌ |
| Works with any agent | ✅ | ❌ (custom per agent) | ❌ |

### ACP Lifecycle (JSON-RPC 2.0)

```jsonc
// 1. Client → Agent: Initialize
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1","clientCapabilities":{}}}

// Agent → Client: Initialize response
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"1","agentCapabilities":{}}}

// 2. Client → Agent: Create session
{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/workspace","mcpServers":[]}}

// Agent → Client: Session created
{"jsonrpc":"2.0","id":2,"result":{"sessionId":"abc-123"}}

// 3. Client → Agent: Send prompt
{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"abc-123","prompt":[{"type":"text","text":"Fix the bug"}]}}

// Agent → Client: Streaming updates (notifications, no id)
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"abc-123","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Looking at..."}}}}

// Agent → Client: Permission request (request, has id)
{"jsonrpc":"2.0","id":4,"method":"client/requestPermission","params":{"toolCall":{"title":"Read file"},"options":[{"optionId":"allow","name":"Allow","kind":"allow"}]}}

// Client → Agent: Auto-approve (headless mode)
{"jsonrpc":"2.0","id":4,"result":{"outcome":{"outcome":"selected","optionId":"allow"}}}

// Agent → Client: Prompt complete
{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}

// 4. Client → Agent: Resume session (optional, agent must support)
{"jsonrpc":"2.0","id":5,"method":"session/load","params":{"sessionId":"abc-123"}}

// Agent → Client: Session loaded
{"jsonrpc":"2.0","id":5,"result":{"sessionId":"abc-123"}}
```

### ACP Adapters per Runtime

| Runtime | ACP Command | Package |
|---------|-------------|---------|
| Codex | `npx @zed-industries/codex-acp` | npm |
| Claude Code | `npx @agentclientprotocol/claude-agent-acp` | npm |
| Pi | `npx pi-acp` | npm |
| Gemini | `gemini --acp` | native |
| Copilot | `copilot --acp --stdio` | native |

## Bridge Architecture

### Bridge as ACP Client

The bridge runs inside the agent container, spawning the ACP adapter as a child process:

```
┌─ Agent Container ────────────────────────────────────────┐
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Bridge (Node.js)                                │    │
│  │                                                  │    │
│  │  ┌─────────────┐  ┌──────────────┐  ┌────────┐  │    │
│  │  │  Channel    │  │ StartupBuffer│  │ Ext.   │  │    │
│  │  │  (Telegram) │─►│              │─►│Registry│  │    │
│  │  └─────────────┘  └──────┬───────┘  └────────┘  │    │
│  │                          │                       │    │
│  │  ┌───────────────────────▼────────────────────┐  │    │
│  │  │  SessionManager (per-chat → sessionId)     │  │    │
│  │  └───────────────────────┬────────────────────┘  │    │
│  │                          │                       │    │
│  │  ┌───────────────────────▼────────────────────┐  │    │
│  │  │  AcpAgent (ClientSideConnection)           │  │    │
│  │  └───────────────────────┬────────────────────┘  │    │
│  │                          │ stdio                  │    │
│  └──────────────────────────┼───────────────────────┘    │
│                             │                            │
│  ┌──────────────────────────▼───────────────────────┐    │
│  │  ACP Adapter (e.g., codex-acp)                   │    │
│  └──────────────────────────────────────────────────┘    │
│                                                          │
│                              egress (default route → gateway)
└──────────────────────────────────────────────────────────┘
```

### Multi-Session Routing

One bridge instance handles multiple concurrent conversations via ACP sessions:

```
Telegram DM @alice ──┐
                     │     ┌──────────────┐     ┌─────────────┐
Telegram DM @bob ───┼────►│   Bridge     │────►│  ACP Agent  │
                     │     │              │     │             │
Group chat ─────────┘     │ SessionMgr:  │     │ Sessions:   │
                          │ alice→sess1  │     │ sess1       │
                          │ bob→sess2    │     │ sess2       │
                          │ group→sess3  │     │ sess3       │
                          └──────────────┘     └─────────────┘
```

Each chat maps to a separate ACP session. The SessionManager handles:
- Lazy session creation (first message from a chat)
- Session resume via `loadSession` (after bridge restart)
- Session persistence to disk (survives container restarts)

### Per-Agent Bots

Each agent gets its own bot. No routing ambiguity:

```
Agent: coder    → Bot: @MyCoderBot     (TELEGRAM_BOT_TOKEN_001)
Agent: reviewer → Bot: @MyReviewerBot  (TELEGRAM_BOT_TOKEN_002)
```

## Bridge Implementation

### Message Flow

```
Channel.onMessage(chatId, text)
  → StartupBuffer.push(chatId, text)        // buffer during startup
    → Command routing (if /command)          // core commands handled here
    → SessionManager.getSession(chatId)      // get or create session
      → AcpAgent.prompt(sessionId, text)     // send to agent
        → Channel.sendMessage(chatId, response)
```

### AcpAgent Class

```typescript
class AcpAgent {
  // Spawn ACP adapter, initialize connection
  async start(): Promise<void>;

  // Send prompt to a specific session, collect response chunks
  async prompt(sessionId: string, text: string): Promise<string>;

  // Get the current connection (survives auto-restart)
  getConnection(): ClientSideConnection | null;

  // Whether the agent has an active connection
  isReady(): boolean;

  // Kill and restart the agent process
  async reset(): Promise<void>;

  // Abort current operation (SIGTERM)
  abort(): void;

  // Graceful shutdown
  stop(): void;
}
```

### SessionManager

Manages per-chat ACP sessions on a single connection:

```typescript
class SessionManager {
  // Get or create a session for a chat (tries loadSession first)
  async getSession(chatId: string): Promise<string>;

  // Create a fresh session
  async createSession(chatId: string): Promise<string>;

  // Reset: delete old session, create new
  async resetSession(chatId: string): Promise<string>;

  // Resume a specific existing session by ID
  async resumeSession(chatId: string, sessionId: string): Promise<void>;
}
```

### SessionStore (Persistence)

Persists session state to disk for crash recovery:

```typescript
class SessionStore {
  // Active session mapping (chatId → sessionId)
  getSessionId(chatId): string | undefined;
  setSessionId(chatId, sessionId): void;

  // Session history (max 20 per chat)
  addToHistory(chatId, sessionId, label?): void;
  getHistory(chatId): SessionHistoryEntry[];
  findByPrefix(chatId, prefix): SessionHistoryEntry | null;

  // Atomic writes, debounced history persistence
  flushSync(): void;  // call on shutdown
}
```

Storage: `/var/lib/bridge/sessions/` (session-map.json + session-history.json)

### StartupBuffer

Buffers messages while the agent is starting up:

```typescript
class StartupBuffer {
  push(chatId, text): void;   // buffer or pass-through
  ready(): void;              // flush buffered messages (discard stale >30s)
  onMessage(handler): void;   // set the downstream handler
}
```

### Headless Permission Handling

Since the bridge runs headless (no user to ask), it auto-approves all tool permissions:

```typescript
async requestPermission(params: acp.RequestPermissionRequest) {
  const allowOption = params.options.find(o => o.kind === "allow");
  return {
    outcome: {
      outcome: "selected",
      optionId: allowOption?.optionId ?? params.options[0].optionId,
    },
  };
}
```

### Configuration

The bridge reads its config from bridge-config.json (generated by CLI):

```json
{
  "channel": "telegram",
  "acp_command": ["npx", "@zed-industries/codex-acp"],
  "cwd": "/workspace",
  "bot_token": "dummy:token",
  "allowed_users": [123456789]
}
```

## Bridge Commands

Core commands handled directly by the bridge (never forwarded to agent):

| Command | Description |
|---------|-------------|
| `/new` | Start a new conversation session |
| `/stop` | Abort current operation (SIGTERM) |
| `/resume [N\|id]` | Browse/switch session history |
| `/label <name>` | Tag current session with a name |
| `/version` | Show bridge version |
| `/sh <cmd>` | Execute shell command in bridge container |
| `/diagnose` | Show diagnostic info |
| `/help` | List all available commands |

### Command Resolution Order

```
1. Core commands (bridge handles directly)
2. Agent-provided commands (future: declared via ACP initialize)
3. Unknown → error message
```

### Extension System

Commands and lifecycle hooks are registered via the `BridgeExtension` interface:

```typescript
interface BridgeExtension {
  name: string;
  commands?: Record<string, CommandDef>;
  onBoot?(ctx: ExtensionContext): Promise<void>;
  onTurnStart?(ctx: ExtensionContext, chatId: string): void;
  onTurnEnd?(ctx: ExtensionContext, chatId: string): void;
}
```

Built-in extensions: `commands` (core commands), `perf-tracker` (/perf), `event-logger` (JSONL logs).

## Channel Provider Interface

Channel providers handle platform-specific messaging:

```typescript
export interface Channel {
  onMessage(handler: (chatId: string, text: string) => void): void;
  sendMessage(chatId: string, text: string): void;
  start(): Promise<void>;
  stop(): void;
  registerCommands?(commands: { name: string; description: string }[]): Promise<void>;
}
```

### Telegram Channel Behavior

1. **Connect** — Long-poll Telegram API using dummy token (real token injected by gateway)
2. **Filter** — Check `allowed_users` config
3. **Ack** — React with 👀 emoji on message receipt
4. **Typing** — Send typing indicator while agent works
5. **Forward** — Message flows through StartupBuffer → SessionManager → AcpAgent
6. **Format** — Convert markdown to Telegram HTML, split at 4096 char limit
7. **Respond** — Send formatted response back to Telegram chat
8. **Rate limit** — Queue messages, respect Telegram API backoff

## Error Handling

| Scenario | Bridge Behavior |
|----------|----------------|
| Agent adapter crashes | Auto-restart, sessions resume via loadSession |
| Prompt timeout | Return timeout error to channel |
| Rate limit (Telegram) | Queue messages, respect backoff |
| Unauthorized user | Silently ignore (log for audit) |
| Bridge restart | Resume sessions from SessionStore (persistent) |
| Agent not ready | Buffer messages in StartupBuffer (discard after 30s) |

## Security

- Agent adapter runs inside the sandbox container (no internet access except via gateway)
- All API keys injected by gateway MITM (agent never sees real credentials)
- Bridge uses dummy tokens — gateway rewrites to real ones
- ACP adapter inherits the sandbox's network restrictions
- `/sh` executes in bridge container (same security boundary as agent)
