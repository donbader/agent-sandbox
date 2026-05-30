# Bridge Protocol

## Overview

Channels connect AI agents to messaging platforms. Every channel speaks **ACP (Agent Client Protocol)** on the agent side and a platform-specific API on the user side.

```
User ←→ [Messaging Platform] ←→ [Channel Provider] ←→ [Agent]
              Telegram API            ACP protocol
                                      (ndJSON over Unix socket)
```

## ACP (Agent Client Protocol)

ACP is a multi-session protocol for communicating with AI agents. It uses newline-delimited JSON (ndJSON) over Unix sockets.

### Why ACP?

| Feature | ACP | Pi RPC | Raw CLI |
|---------|-----|--------|---------|
| Multi-session | ✅ | ❌ (single session) | ❌ |
| Structured tool calls | ✅ | ✅ | ❌ |
| Streaming responses | ✅ | ✅ | ⚠️ (stdout parsing) |
| Session resume | ✅ | ❌ | ⚠️ (--resume flag) |
| Standard protocol | ✅ | ❌ (proprietary) | ❌ |

### ACP Message Types

```jsonl
// Client → Agent: Start session
{"type":"session.start","session_id":"abc123","config":{}}

// Client → Agent: Send message
{"type":"message.send","session_id":"abc123","content":"Fix the bug in auth.ts"}

// Agent → Client: Text response (streaming)
{"type":"message.delta","session_id":"abc123","delta":"Looking at auth.ts..."}

// Agent → Client: Tool use
{"type":"tool.use","session_id":"abc123","tool":"file_read","input":{"path":"src/auth.ts"}}

// Client → Agent: Tool result
{"type":"tool.result","session_id":"abc123","output":"...file contents..."}

// Agent → Client: Message complete
{"type":"message.complete","session_id":"abc123"}

// Client → Agent: End session
{"type":"session.end","session_id":"abc123"}
```

## Channel Architecture

### Channel Inside Sandbox

The channel provider runs inside the agent container alongside the agent. This means:
- Channel's platform API access goes through the transparent proxy (egress controlled)
- Channel communicates with agent via local Unix socket (no network hop)
- Channel manages its own platform credentials (bot token read from env)

```
┌─ Agent Container ────────────────────────────────────────┐
│                                                          │
│  ┌─────────┐    Unix Socket    ┌─────────────────────┐   │
│  │  Agent  │◄─────────────────►│  Channel Provider   │   │
│  │ (codex) │   /ipc/agent.sock │  (Telegram bot)     │   │
│  └─────────┘                   └──────────┬──────────┘   │
│                                           │              │
│                              egress (proxy handles it)   │
│                                           │              │
└───────────────────────────────────────────┼──────────────┘
                                            │
                                     ┌──────▼──────┐
                                     │ Egress Proxy │
                                     │  (no auth    │
                                     │  for TG —    │
                                     │  bot token   │
                                     │  injected    │
                                     │  by proxy)   │
                                     └──────┬──────┘
                                            │
                                            ▼
                                   api.telegram.org
```

Note: The proxy injects the bot token into Telegram API requests (URL path rewrite). The channel provider uses a dummy token — it never sees the real one.

### Multi-Session Routing

One channel instance handles multiple concurrent conversations:

```
Telegram DM @alice ──┐
                     │     ┌──────────────┐     ┌─────────┐
Telegram DM @bob ───┼────►│   Channel    │────►│  Agent  │
                     │     │   Provider   │     │         │
Group chat ─────────┘     │              │     │ ACP     │
                          │ Routes:      │     │ sessions│
                          │ alice→sess1  │     │         │
                          │ bob→sess2    │     │         │
                          │ group→sess3  │     │         │
                          └──────────────┘     └─────────┘
```

Each chat maps to a separate ACP session. The agent maintains independent context per session.

### Per-Agent Bots

Each agent gets its own bot. No routing ambiguity:

```
Agent: coder    → Bot: @MyCoderBot     (TELEGRAM_BOT_TOKEN_001)
Agent: reviewer → Bot: @MyReviewerBot  (TELEGRAM_BOT_TOKEN_002)
```

Users talk to different bots to reach different agents.

## Channel Provider Interface

Channel providers must implement:

```typescript
export interface ChannelProvider {
    // Start the channel (connect to platform, begin listening)
    start(send: SendFunc): Promise<void>;

    // Stop the channel gracefully
    stop(): Promise<void>;

    // Send a streaming response chunk to a chat
    sendResponse(chatId: string, text: string): Promise<void>;

    // Mark a response as complete
    completeResponse(chatId: string): Promise<void>;
}

export type SendFunc = (chatId: string, text: string) => void;
```

The channels-bridge runtime registers commands at startup (e.g., `/oauth`, `/status`, `/cancel`). The channel provider only handles dispatch — it doesn't know about OAuth or any specific command logic.

### Telegram Provider Behavior

1. **Connect** — Long-poll Telegram API using dummy token (real token injected by proxy)
2. **Filter** — Check `allowed_users` and `groups` config
3. **Route** — Map chat ID to ACP session (create if new)
4. **Forward** — Send ACP messages to agent, relay responses back
5. **Stream** — Edit Telegram message as agent streams response
6. **Commands** — Handle `/oauth`, `/reset`, `/status`

## OAuth UX via Channel

When an agent needs access to an OAuth-protected service (e.g., Notion), the user authorizes through the chat:

```
User:  /oauth notion
Bot:   🔗 Authorize Notion access:
       https://api.notion.com/v1/oauth/authorize?client_id=xxx&redirect_uri=...
       
       Click the link, authorize, then paste the callback URL here.

User:  /oauth callback https://redirect.example.com/callback?code=abc123&state=xyz

Bot:   ✅ Notion connected! Your agent can now access Notion.
```

### OAuth Flow Internals

```
1. /oauth notion
   → Channel initiates dynamic client registration with MCP server
   → Channel generates state token
   → Channel constructs authorization URL
   → Channel sends URL to user

2. /oauth callback <url>
   → Channel extracts code + state from URL
   → Channel validates state token
   → Channel sends (code, provider) to proxy
   → Proxy's mcp-oauth provider:
     - Exchanges code for access_token + refresh_token
     - Stores tokens
     - Schedules auto-refresh
   → Channel confirms to user

3. Future requests to mcp.notion.com
   → Proxy matches egress rule with mcp-oauth provider
   → Provider injects: Authorization: Bearer <fresh_token>
```

## Protocol Adapters

For agents that don't speak ACP natively, adapters translate between protocols:

### Pi RPC → ACP Adapter

```
Channel ←→ [pi-rpc-to-acp adapter] ←→ Pi Agent (stdin/stdout)
  ACP          translates              Pi RPC JSON
```

### Claude Code → ACP Adapter

```
Channel ←→ [claude-headless-to-acp adapter] ←→ Claude Code CLI
  ACP          translates                      stream-json output
```

The adapter:
1. Receives ACP `message.send`
2. Spawns/resumes `claude -p` with the message
3. Parses stream-json output
4. Emits ACP `message.delta` and `tool.use` events
5. Emits `message.complete` when Claude finishes

## Error Handling

| Scenario | Channel Behavior |
|----------|----------------|
| Agent crashes | Send error message to user, attempt restart |
| Session timeout | Notify user, offer to start new session |
| Rate limit (Telegram) | Queue messages, respect backoff |
| Unauthorized user | Silently ignore (log for audit) |
| Channel restart | Resume existing sessions from ACP session IDs |
| OAuth token expired | Proxy auto-refreshes; if refresh fails, prompt user to re-authorize |
