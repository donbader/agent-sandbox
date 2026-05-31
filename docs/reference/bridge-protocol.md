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

The bridge spawns the agent's ACP adapter as a child process and communicates via stdio:

```
┌─ Agent Container ────────────────────────────────────────┐
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │  Bridge (Node.js)                                │    │
│  │                                                  │    │
│  │  ┌─────────────┐     ┌────────────────────────┐ │    │
│  │  │  Telegram   │     │  AcpAgent              │ │    │
│  │  │  Channel    │────►│  (ClientSideConnection)│ │    │
│  │  └─────────────┘     └───────────┬────────────┘ │    │
│  │                                  │ stdio         │    │
│  └──────────────────────────────────┼───────────────┘    │
│                                     │                    │
│  ┌──────────────────────────────────▼───────────────┐    │
│  │  ACP Adapter (e.g., codex-acp)                   │    │
│  │  → wraps Codex CLI into ACP protocol             │    │
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
Group chat ─────────┘     │ Routes:      │     │ Sessions:   │
                          │ alice→sess1  │     │ sess1       │
                          │ bob→sess2    │     │ sess2       │
                          │ group→sess3  │     │ sess3       │
                          └──────────────┘     └─────────────┘
```

Each chat maps to a separate ACP session. The agent maintains independent context per session.

### Per-Agent Bots

Each agent gets its own bot. No routing ambiguity:

```
Agent: coder    → Bot: @MyCoderBot     (TELEGRAM_BOT_TOKEN_001)
Agent: reviewer → Bot: @MyReviewerBot  (TELEGRAM_BOT_TOKEN_002)
```

## Bridge Implementation

### AcpAgent Class

```typescript
import * as acp from "@agentclientprotocol/sdk";

class AcpAgent {
  // Spawn ACP adapter, initialize connection, create session
  async start(): Promise<void>;

  // Send prompt, collect response chunks, return full text
  async prompt(text: string): Promise<string>;

  // Register callback for streaming chunks
  onChunk(callback: (text: string) => void): void;

  // Graceful shutdown
  async stop(): Promise<void>;
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

The bridge reads its ACP command from bridge-config.json:

```json
{
  "acp_command": ["npx", "@zed-industries/codex-acp"],
  "cwd": "/workspace",
  "approve_all": true
}
```

Or from environment variable: `BRIDGE_ACP_COMMAND="npx @zed-industries/codex-acp"`

## Channel Provider Interface

Channel providers handle platform-specific messaging:

```typescript
export interface Channel {
  start(): Promise<void>;
  stop(): Promise<void>;
}
```

The channel receives messages from the platform, calls `acpAgent.prompt(text)`, and sends the response back.

### Telegram Channel Behavior

1. **Connect** — Long-poll Telegram API using dummy token (real token injected by gateway)
2. **Filter** — Check `allowed_users` and `groups` config
3. **Route** — Map chat ID to ACP session (create if new)
4. **Forward** — Call `acpAgent.prompt(text)` with user message
5. **Respond** — Send agent's response back to Telegram chat
6. **Error** — On agent crash, send error message and restart

## Error Handling

| Scenario | Bridge Behavior |
|----------|----------------|
| Agent adapter crashes | Auto-restart after 2s delay, create new session |
| Prompt timeout | Return timeout error to channel |
| Rate limit (Telegram) | Queue messages, respect backoff |
| Unauthorized user | Silently ignore (log for audit) |
| Bridge restart | Create fresh sessions (stateless) |

## Security

- Agent adapter runs inside the sandbox container (no internet access except via gateway)
- All API keys injected by gateway MITM (agent never sees real credentials)
- Bridge uses dummy tokens — gateway rewrites to real ones
- ACP adapter inherits the sandbox's network restrictions
