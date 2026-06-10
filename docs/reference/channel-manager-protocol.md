# Agent Manager ACP Protocol

## Overview

The agent-manager-acp plugin manages the ACP agent process lifecycle. OpenACP spawns agent-manager via stdio, agent-manager spawns the ACP agent (e.g. pi-acp) via stdio. All communication is ndjson (newline-delimited JSON) over stdin/stdout.

```
User ←→ [Platform] ←→ [OpenACP] ←→ stdio ←→ [agent-manager] ←→ stdio ←→ [ACP Agent]
         Telegram      orchestrator            core plugin             codex-acp → Codex
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

| Component | Role | Runs as |
|-----------|------|---------|
| **agent-manager** | Spawns agent, handshake, auth, proxies ACP | Stdio child process of OpenACP |
| **OpenACP** | Bridges external platforms to ACP via agent-manager | Orchestrator process |

### Process Model

```
┌─ Agent Container ───────────────────────────────────────────────────┐
│                                                                     │
│  [OpenACP]                                                          │
│    │                                                                │
│    │ stdio (ndjson)                                                 │
│    ▼                                                                │
│  [agent-manager]                                                    │
│    ├── Spawns ACP agent process via stdio                           │
│    ├── Performs initialize + auth handshake                         │
│    ├── Caches initialize result                                     │
│    ├── Injects mcpServers into session/new                          │
│    └── Forwards responses back to OpenACP via stdout                │
│         │                                                           │
│         │ stdio pipe                                                │
│         ▼                                                           │
│  [ACP Agent]  (e.g., codex-acp → Codex)                            │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Multi-Client, Single Agent

Multiple channels share one agent connection through OpenACP. Different sessions run concurrently:

```
Telegram DM @alice ──┐
                     │     ┌──────────┐       ┌───────────────┐       ┌─────────────┐
Telegram DM @bob ───┼────►│  OpenACP │─stdio─►│ agent-manager │─stdio─►│ codex-acp  │
                     │     └──────────┘       │               │       └─────────────┘
Slack #general ─────┘                         │  sessions:    │
                                              │   alice→s1    │
                                              │   bob→s2      │
                                              │   general→s3  │
                                              └───────────────┘
```

- Each chat/channel maps to a separate ACP session
- Different sessions can be processed concurrently (async, non-blocking)
- Same session is serial (one prompt at a time, conversationally correct)

## ACP Lifecycle (Agent-Manager Handles)

The agent-manager performs the full ACP handshake with the agent on startup, then intercepts and simplifies requests from OpenACP.

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

When OpenACP sends a request over stdin:

| OpenACP sends | Agent-manager behavior |
|---|---|
| `initialize` | Returns cached result immediately (does NOT forward to agent) |
| `auth/authenticate` | Returns success immediately (does NOT forward to agent) |
| `session/new` | Injects `mcpServers: []` into params, then forwards to agent |
| Any other method | Forwards directly to agent, writes response to stdout |

### Full Session Flow (OpenACP → agent-manager → agent)

```jsonc
// 1. OpenACP → agent-manager: Initialize (intercepted)
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1","clientCapabilities":{}}}
// agent-manager → OpenACP: Cached response
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"1","agentCapabilities":{}}}

// 2. OpenACP → agent-manager: Auth (intercepted)
{"jsonrpc":"2.0","id":2,"method":"auth/authenticate","params":{"id":"api-key","secret":"anything"}}
// agent-manager → OpenACP: Success
{"jsonrpc":"2.0","id":2,"result":{"status":"authenticated"}}

// 3. OpenACP → agent-manager: Create session
{"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/workspace"}}
// agent-manager injects mcpServers, forwards:
// {"jsonrpc":"2.0","id":3,"method":"session/new","params":{"cwd":"/workspace","mcpServers":[]}}
// agent → agent-manager → OpenACP: Session created
{"jsonrpc":"2.0","id":3,"result":{"sessionId":"abc-123"}}

// 4. OpenACP → agent-manager: Send prompt (forwarded as-is)
{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"abc-123","prompt":[{"type":"text","text":"Fix the bug"}]}}

// agent → agent-manager → OpenACP: Streaming updates (notifications, no id)
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"abc-123","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Looking at..."}}}}

// agent → agent-manager → OpenACP: Turn completed
{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"abc-123","update":{"sessionUpdate":"turn_completed"}}}

// agent → agent-manager → OpenACP: Prompt complete
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

## Communication Protocol

All communication between OpenACP and agent-manager uses ndjson (newline-delimited JSON) over stdin/stdout:

- OpenACP writes JSON-RPC requests to agent-manager's stdin (one JSON object per line)
- Agent-manager writes JSON-RPC responses and notifications to stdout (one JSON object per line)
- Agent-manager writes logs to stderr (keeps stdout clean for protocol traffic)

### OpenACP Responsibilities

| Concern | OpenACP | Agent-Manager |
|---------|---------|---------------|
| Platform connection (bot API, webhooks) | ✅ | ❌ |
| User ACL / filtering | ✅ | ❌ |
| Platform UX (typing, reactions, formatting) | ✅ | ❌ |
| Session mapping (chatId → sessionId) | ✅ | ❌ |
| Agent process lifecycle | ❌ | ✅ |
| ACP handshake + auth | ❌ | ✅ |
| mcpServers injection | ❌ | ✅ |
| Permission auto-approval | ❌ | ✅ |

## Agent-Provided Commands

Agents declare commands dynamically via ACP `available_commands_update` session notification:

```jsonc
// Agent → agent-manager → OpenACP (after session/new or session/load)
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

OpenACP registers these as platform-native UI (e.g., Telegram `setMyCommands`). All agent commands are forwarded via `session/prompt` — the agent handles them internally.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Agent process crashes | agent-manager auto-restarts, new session on next request |
| Prompt fails | Return JSON-RPC error to OpenACP; OpenACP sends error to user |
| Stdin/stdout pipe breaks | OpenACP restarts agent-manager |
| Agent not ready | agent-manager buffers requests until handshake completes |

## Security

- Agent process runs inside the sandbox container (no internet except via gateway)
- All API keys injected by gateway MITM (agent never sees real credentials)
- ACP agent inherits the sandbox's network restrictions
- No network ports exposed — communication is stdio only within the container
