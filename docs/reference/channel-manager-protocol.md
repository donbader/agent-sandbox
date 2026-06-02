# Bridge Protocol

## Overview

The bridge connects AI agents to messaging platforms via ACP (Agent Client Protocol). It's designed as a thin adapter layer — channels handle platform UX, the ACP wrapper enriches the agent, and the agent does all the real work.

```
User ←→ [Platform] ←→ [Channel] ←→ [AcpAgent] ←→ [ACP Wrapper] ←→ [ACP Adapter] ←→ [Agent]
         Telegram      grammy         SDK client    line filter      codex-acp        Codex
```

## ACP (Agent Client Protocol)

ACP is a JSON-RPC 2.0 protocol over stdio for communicating with AI coding agents. It's the industry standard — supported by Codex, Claude Code, Pi, Gemini, Copilot, and others.

- **Spec**: https://agentclientprotocol.com
- **TypeScript SDK**: `@agentclientprotocol/sdk` (used by bridge client)
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

## Architecture

### Process Model

Three processes in the same container, connected by stdio pipes:

```
┌─ Agent Container ─────────────────────────────────────────────────┐
│                                                                   │
│  [Process 1] Bridge (Node.js)                                     │
│    ├── Channel plugins (Telegram, Slack, etc.)                    │
│    ├── AcpAgent (ClientSideConnection → spawns process 2)         │
│    └── Session mapping (chatId → sessionId, in-memory)            │
│         │                                                         │
│         │ stdio pipe                                              │
│         ▼                                                         │
│  [Process 2] ACP Wrapper (line filter)                            │
│    ├── Intercepts /sh, /diagnose (wrapper commands)               │
│    ├── Tracks response time (perf)                                │
│    └── Everything else → transparent pass-through                 │
│         │                                                         │
│         │ stdio pipe                                              │
│         ▼                                                         │
│  [Process 3] ACP Adapter (e.g., codex-acp → Codex)               │
│                                                                   │
│                              egress (default route → gateway)     │
└───────────────────────────────────────────────────────────────────┘
```

### Multi-Channel, Single Agent

Multiple channels share one agent connection. Different sessions run concurrently:

```
Telegram DM @alice ──┐
                     │     ┌────────────────┐     ┌─────────────────┐
Telegram DM @bob ───┼────►│  Bridge        │────►│  ACP Wrapper    │──► codex-acp
                     │     │                │     │                 │
Slack #general ─────┘     │ sessions:      │     │ pass-through +  │
                          │  alice → s1    │     │ /sh, /diagnose  │
                          │  bob → s2      │     └─────────────────┘
                          │  general → s3  │
                          └────────────────┘
```

- Each chat/channel maps to a separate ACP session
- Different sessions can be processed concurrently (async, non-blocking)
- Same session is serial (one prompt at a time, conversationally correct)

### Per-Agent Bots

Each agent gets its own bot. No routing ambiguity:

```
Agent: coder    → Bot: @MyCoderBot     (TELEGRAM_BOT_TOKEN_001)
Agent: reviewer → Bot: @MyReviewerBot  (TELEGRAM_BOT_TOKEN_002)
```

## Bridge Implementation

### Responsibilities by Layer

| Layer | Responsibility | Doesn't do |
|-------|---------------|------------|
| **Channel** | Platform UX (ack, typing, formatting, ACL, bot menu) | Session management logic, command handling |
| **AcpAgent** | ACP client (spawn, connect, prompt, collect chunks) | Platform-specific anything |
| **ACP Wrapper** | Enriched commands (/sh, /diagnose, perf tracking) | Session management, platform UX |
| **ACP Adapter** | Agent commands, conversation, tools | Infrastructure concerns |

### Message Flow

```
Channel.onMessage(chatId, text)
  → getOrCreateSession(chatId)              // lazy session creation (in-memory Map)
  → agent.prompt(sessionId, text)           // ACP client → wrapper → agent
    → wrapper: intercept /sh, /diagnose?
      → yes: respond locally (never reaches agent)
      → no: forward to real agent
  → Channel.sendMessage(chatId, response)   // format + deliver
```

### AcpAgent Class

```typescript
class AcpAgent {
  start(): Promise<void>;                    // spawn wrapper + adapter, initialize ACP
  stop(): void;                              // graceful shutdown
  reset(): Promise<void>;                    // kill and restart
  abort(): void;                             // SIGTERM current operation
  isReady(): boolean;                        // connection active?
  getConnection(): ClientSideConnection;     // raw ACP connection
  prompt(sessionId: string, text: string): Promise<string>;  // prompt + collect chunks
  getAgentCommands(): AgentCommand[];        // last known commands from agent
  onCommandsUpdate(cb): void;               // subscribe to command changes
}
```

### Channel Interface

Channels are pure platform adapters — no ACP knowledge:

```typescript
export interface Channel {
  start(): Promise<void>;
  stop(): void;
}

export type ChannelFactory = (config: ChannelConfig, agent: AcpAgent) => Channel;
```

The channel receives the `AcpAgent` as a dependency and uses it to send prompts. Session management (chatId → sessionId mapping) lives in the channel as a simple in-memory Map.

### Headless Permission Handling

The bridge auto-approves all tool permissions (headless, no user to ask):

```typescript
async requestPermission(params) {
  const allowOption = params.options.find(o => o.kind === "allow");
  return {
    outcome: { outcome: "selected", optionId: allowOption?.optionId ?? params.options[0].optionId },
  };
}
```

### Configuration

Bridge config (generated by CLI):

```json
{
  "channel": "telegram",
  "acp_command": ["node", "/opt/bridge/dist/acp-wrapper.js", "--", "npx", "codex-acp"],
  "cwd": "/workspace",
  "bot_token": "dummy:token",
  "allowed_users": [123456789]
}
```

## ACP Wrapper (Enriched ACP Server)

The wrapper is a transparent ndjson line filter between bridge and agent. It enriches the standard ACP adapter with extra capabilities.

### Design Principles

- **Transparent pass-through** — all ACP messages forwarded untouched by default
- **Type-safe interception** — uses `import type` from ACP SDK (zero runtime dependency)
- **Auto-compatible** — new ACP methods pass through without code changes
- **Extracted logic** — bridge commands in separate testable module (`wrapper-commands.ts`)

### Wrapper Commands

Commands handled by the wrapper (never reach the real agent):

| Command | Description |
|---------|-------------|
| `/sh <cmd>` | Execute shell command in agent container |
| `/diagnose` | Show diagnostics: PID, memory, uptime, perf stats |

### How Interception Works

```
Bridge stdin → [parse JSON line]
  → method === "session/prompt" AND text starts with /sh or /diagnose?
    → yes: handle locally, write response to bridge stdout
    → no: forward line to agent stdin (untouched)

Agent stdout → [forward to bridge stdout]
  → track response timing for /diagnose perf stats
```

### Why Server-Side?

- `/sh` runs in the **agent container** (correct filesystem context)
- `/diagnose` reports **agent-side** metrics (memory, CPU, disk)
- Any ACP client (Telegram, Slack, CLI) automatically benefits
- No need to fork upstream ACP adapters

## Agent-Provided Commands

Agents declare commands dynamically via ACP `available_commands_update` session notification:

```jsonc
// Agent → Client (after session/new or session/load)
{"jsonrpc":"2.0","method":"session/update","params":{
  "sessionId":"abc-123",
  "update":{
    "sessionUpdate":"available_commands_update",
    "availableCommands":[
      {"name":"model","description":"Switch AI model","input":{"hint":"model name"}},
      {"name":"compact","description":"Compact conversation history"},
      {"name":"new","description":"Start fresh conversation"}
    ]
  }
}}
```

The channel registers these as bot menu items (Telegram `setMyCommands`). All agent commands are forwarded via `session/prompt` — the agent handles them internally.

### Command Resolution

```
/sh, /diagnose       → handled by ACP wrapper (never reaches agent)
/model, /new, etc.   → forwarded to agent via prompt (agent handles internally)
Plain text           → forwarded to agent via prompt
```

## Telegram Channel Behavior

1. **Connect** — Long-poll Telegram API via grammy (dummy token, real one injected by gateway)
2. **Filter** — Check `allowed_users` ACL
3. **Ack** — React with 👀 emoji on message receipt
4. **Typing** — Send typing indicator while agent works
5. **Session** — Get or create ACP session for this chatId
6. **Forward** — All messages (including /commands) sent as prompts to agent
7. **Format** — Convert markdown to Telegram HTML, split at 4096 char limit
8. **Respond** — Send formatted response with retry + rate limiting
9. **Bot menu** — Register agent-declared commands via `setMyCommands`

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Agent adapter crashes | AcpAgent auto-restarts, new session on next message |
| Prompt fails | Send error message to user ("⚠️ Agent unavailable") |
| Rate limit (Telegram) | Queue messages, respect backoff (429 retry-after) |
| Unauthorized user | Silently ignore (log for audit) |
| Agent not ready | Buffer messages (simple array, drain when ready) |

## Security

- Agent adapter runs inside the sandbox container (no internet except via gateway)
- All API keys injected by gateway MITM (agent never sees real credentials)
- Bridge uses dummy tokens — gateway rewrites to real ones
- ACP adapter inherits the sandbox's network restrictions
- `/sh` executes in agent container (same security boundary as the agent)
