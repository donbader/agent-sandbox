# Agent Manager ACP Protocol

## Overview

The agent-manager-acp plugin spawns and manages the ACP agent process, performing handshake and authentication on behalf of clients, then proxying ACP over HTTP and WebSocket. Channel adapters connect as sidecars via WebSocket, bridging external messaging platforms (Telegram, Slack, etc.) to the agent through ACP.

```
User ←→ [Platform] ←→ [Channel Adapter] ←→ WS ←→ [agent-manager-acp] ←→ stdio ←→ [ACP Agent]
         Telegram      telegram-adapter              core plugin             codex-acp → Codex
```

## ACP (Agent Client Protocol)

ACP is a JSON-RPC 2.0 protocol over stdio for communicating with AI coding agents. It's the industry standard — supported by Codex, Claude Code, Pi, Gemini, Copilot, and others.

- **Spec**: https://agentclientprotocol.com
- **TypeScript SDK**: `@agentclientprotocol/sdk`
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

### ACP Adapters per Runtime

| Runtime | ACP Command | Package |
|---------|-------------|---------|
| Codex | `npx @zed-industries/codex-acp` | npm |
| Claude Code | `npx @agentclientprotocol/claude-agent-acp` | npm |
| Pi | `npx pi-acp` | npm |
| Gemini | `gemini --acp` | native |
| Copilot | `copilot --acp --stdio` | native |

## Architecture

### Two-Part Design

The old monolithic "channel manager" is now split into two concerns:

| Component | Role | Runs as |
|-----------|------|---------|
| **agent-manager-acp** | Spawns agent, handshake, auth, proxy ACP over HTTP/WS | Core plugin (in-container process) |
| **Channel adapters** | Bridge external platforms to ACP | Sidecars (connect to agent-manager via WebSocket) |

### Process Model

```
┌─ Agent Container ───────────────────────────────────────────────────┐
│                                                                     │
│  [agent-manager-acp]  (core plugin, listens on :3100)               │
│    ├── Spawns ACP agent process via stdio                           │
│    ├── Performs initialize + auth handshake                         │
│    ├── Exposes HTTP/WS endpoints for clients                        │
│    ├── Caches initialize result                                     │
│    ├── Injects mcpServers into session/new                          │
│    └── Broadcasts agent responses to all connected clients          │
│         │                                                           │
│         │ stdio pipe                                                │
│         ▼                                                           │
│  [ACP Agent]  (e.g., codex-acp → Codex)                            │
│                                                                     │
│  [Channel Adapter: telegram]  ──── WS ────┐                        │
│  [Channel Adapter: slack]     ──── WS ────┼──► agent-manager :3100  │
│  [Channel Adapter: discord]   ──── WS ────┘                        │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Multi-Client, Single Agent

Multiple channel adapters share one agent connection. Different sessions run concurrently:

```
Telegram DM @alice ──┐
                     │     ┌──────────────────┐       ┌─────────────┐
Telegram DM @bob ───┼────►│ telegram-adapter  │──WS──►│             │
                     │     └──────────────────┘       │  agent-mgr  │──stdio──► codex-acp
Slack #general ─────┘     ┌──────────────────┐       │             │
                    ──────►│  slack-adapter   │──WS──►│  sessions:  │
                           └──────────────────┘       │   alice→s1  │
                                                      │   bob→s2    │
                                                      │   general→s3│
                                                      └─────────────┘
```

- Each chat/channel maps to a separate ACP session
- Different sessions can be processed concurrently (async, non-blocking)
- Same session is serial (one prompt at a time, conversationally correct)

## ACP Lifecycle (Agent-Manager Handles)

The agent-manager performs the full ACP handshake with the agent on startup, then intercepts and simplifies requests from clients.

### Startup Sequence (agent-manager → agent, via stdio)

```jsonc
// 1. agent-manager → agent: Initialize
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1","clientCapabilities":{}}}

// agent → agent-manager: Initialize response (cached)
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"1","agentCapabilities":{}}}

// 2. agent-manager → agent: Authenticate
{"jsonrpc":"2.0","id":2,"method":"auth/authenticate","params":{"id":"api-key","secret":"<from OPENAI_API_KEY env>"}}

// agent → agent-manager: Auth success
{"jsonrpc":"2.0","id":2,"result":{"status":"authenticated"}}
```

### Client Request Interception

When a client (channel adapter or HTTP caller) connects:

| Client sends | Agent-manager behavior |
|---|---|
| `initialize` | Returns cached result immediately (does NOT forward to agent) |
| `auth/authenticate` | Returns success immediately (does NOT forward to agent) |
| `session/new` | Injects `mcpServers: []` into params, then forwards to agent |
| Any other method | Forwards directly to agent, broadcasts response to connected clients |

### Full Session Flow (client → agent-manager → agent)

```jsonc
// 1. Client → agent-manager: Initialize (intercepted)
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1","clientCapabilities":{}}}
// agent-manager → Client: Cached response
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"1","agentCapabilities":{}}}

// 2. Client → agent-manager: Auth (intercepted)
{"jsonrpc":"2.0","id":2,"method":"auth/authenticate","params":{"id":"api-key","secret":"anything"}}
// agent-manager → Client: Success
{"jsonrpc":"2.0","id":2,"result":{"status":"authenticated"}}

// 3. Client → agent-manager: Create session
{"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/workspace"}}
// agent-manager injects mcpServers, forwards:
// {"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/workspace","mcpServers":[]}}
// agent → agent-manager → Client: Session created
{"jsonrpc":"2.0","id":3,"result":{"sessionId":"abc-123"}}

// 4. Client → agent-manager: Send prompt (forwarded as-is)
{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"abc-123","prompt":[{"type":"text","text":"Fix the bug"}]}}

// agent → agent-manager → Client: Streaming updates (notifications, no id)
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"abc-123","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Looking at..."}}}}

// agent → agent-manager → Client: Turn completed
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"abc-123","update":{"sessionUpdate":"turn_completed"}}}

// agent → agent-manager → Client: Prompt complete
{"jsonrpc":"2.0","id":4,"result":{"stopReason":"end_turn"}}
```

### Headless Permission Handling

The agent-manager auto-approves all tool permissions (headless, no user to ask):

```jsonc
// agent → agent-manager: Permission request
{"jsonrpc":"2.0","id":5,"method":"client/requestPermission","params":{"toolCall":{"title":"Read file"},"options":[{"optionId":"allow","name":"Allow","kind":"allow"}]}}

// agent-manager → agent: Auto-approve
{"jsonrpc":"2.0","id":5,"result":{"outcome":{"outcome":"selected","optionId":"allow"}}}
```

## HTTP Endpoints (Agent-Manager)

The agent-manager exposes the following endpoints on its configured port:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check: `{"status":"ok","agent_running":true/false}` |
| `POST` | `/acp` | JSON-RPC request. `initialize` returns synchronously; other methods return `202 Accepted` |
| `GET` | `/acp` | SSE stream for agent notifications |
| `WS` | `/acp` | Full-duplex WebSocket (preferred by channel adapters) |

### Port Configuration

The agent-manager listens on port **3100** by default. Configurable via:

- Environment variable: `AGENT_MANAGER_PORT=3100`
- Plugin option: `port: 3100` in agent.yaml

## Channel Adapter Protocol

Channel adapters are sidecars that connect to the agent-manager over WebSocket and bridge external messaging platforms to ACP.

### Connection Flow

```
1. Connect to ws://agent:<port>/acp (default port 3100)
2. Send initialize → receive cached agent capabilities
3. Call session/new with {cwd: "..."} (mcpServers injected by manager)
4. Send session/prompt with prompt array
5. Receive session/update notifications:
   - agent_message_chunk (streaming text)
   - turn_completed
   - available_commands_update
6. Receive prompt result (stopReason: "end_turn")
```

### Adapter Responsibilities

| Concern | Channel Adapter | Agent-Manager |
|---------|----------------|---------------|
| Platform connection (bot API, webhooks) | ✅ | ❌ |
| User ACL / filtering | ✅ | ❌ |
| Platform UX (typing, reactions, formatting) | ✅ | ❌ |
| Session mapping (chatId → sessionId) | ✅ | ❌ |
| Agent process lifecycle | ❌ | ✅ |
| ACP handshake + auth | ❌ | ✅ |
| mcpServers injection | ❌ | ✅ |
| Permission auto-approval | ❌ | ✅ |
| Broadcasting responses | ❌ | ✅ |

### Example: Telegram Adapter

1. **Connect** — WebSocket to `ws://agent:3100/acp`
2. **Initialize** — Send `initialize`, get cached capabilities
3. **Poll Telegram** — Long-poll via grammy (dummy token, real one injected by gateway)
4. **Filter** — Check `allowed_users` ACL
5. **Ack** — React with 👀 emoji on message receipt
6. **Typing** — Send typing indicator while agent works
7. **Session** — Get or create ACP session for this chatId via `session/new`
8. **Prompt** — Send `session/prompt` with user message
9. **Stream** — Collect `session/update` notifications (agent_message_chunk)
10. **Format** — Convert markdown to Telegram HTML, split at 4096 char limit
11. **Respond** — Send formatted response with retry + rate limiting

## Agent-Provided Commands

Agents declare commands dynamically via ACP `available_commands_update` session notification:

```jsonc
// Agent → agent-manager → all clients (after session/new or session/load)
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

Channel adapters register these as platform-native UI (e.g., Telegram `setMyCommands`). All agent commands are forwarded via `session/prompt` — the agent handles them internally.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Agent process crashes | agent-manager auto-restarts, new session on next request |
| Prompt fails | Return JSON-RPC error to client; adapter sends error to user |
| WebSocket disconnect | Adapter reconnects with backoff |
| Health check fails | Orchestrator can restart container |
| Agent not ready | agent-manager buffers or returns 503 until handshake completes |

## Security

- Agent process runs inside the sandbox container (no internet except via gateway)
- All API keys injected by gateway MITM (agent never sees real credentials)
- Channel adapters use dummy tokens — gateway rewrites to real ones
- ACP agent inherits the sandbox's network restrictions
- Agent-manager only accepts connections from within the container network
