# Streaming Reply Design

**Date:** 2025-06-02  
**Status:** Approved  
**Scope:** Phase 6 — Integrations & Hardening

## Overview

Live-stream agent responses to Telegram by editing messages as content arrives, using ACP protocol notifications natively. Three content types (thinking, tool calls, response text) are rendered across two message slots (ephemeral draft + persistent main message).

## Goals

- Immediate perceived responsiveness — user sees activity within milliseconds of the agent starting
- Clean separation of thinking (transient) from tool calls + response (persistent)
- Graceful handling of long responses, tool use pauses, and rate limits
- Zero configuration — adaptive behavior based on response characteristics
- ACP-native — channel consumes protocol notifications directly, no custom abstraction

## Non-Goals

- Custom UI beyond Telegram's native message rendering
- Streaming for non-Telegram channels (future work, same pattern)
- Showing full tool results (truncated preview only)

---

## Architecture

### Message Slots

| Slot | Telegram API | Content | Lifecycle |
|------|--------------|---------|-----------|
| **Thinking draft** | `sendMessageDraft(draft_id)` | `🧠` + agent reasoning | Ephemeral (30s auto-disappear). Re-sent before timeout. Appended across multiple thinking rounds. |
| **Main message** | `sendMessage` → `editMessageText` | Tool calls + transient result previews + response text | Persistent. Created lazily on first tool call or response text. Edited as content grows. Overflows to new message at 4096 chars. |

### ACP Notification Mapping

The channel subscribes to `session/update` notifications via an `onSessionUpdate` option passed to the ACP client's `prompt()` method. The channel handles these notification types:

| `sessionUpdate` | Handler | Target |
|-----------------|---------|--------|
| `agent_thought_chunk` | `pushThinking(text)` | Thinking draft |
| `tool_call` | `toolStart(toolCallId, title, status)` | Main message |
| `tool_call_update` | `toolUpdate(toolCallId, status, content)` | Main message |
| `agent_message_chunk` | `pushText(text)` | Main message |

All other notification types are ignored (passed through silently).

---

## State Machine: StreamController

```
IDLE → BUFFERING → STREAMING → (OVERFLOW → STREAMING)
                                        ↓
                             prompt resolves → FINALIZE
```

### States

**IDLE** — No active prompt.

**BUFFERING** — Prompt started, first chunks arriving.
- A 300ms timer runs for `agent_message_chunk` only
- If the prompt completes within 300ms → send as single message (today's behavior)
- If 300ms elapses with text buffered → send first message, enter STREAMING
- `agent_thought_chunk` or `tool_call` as first event → enter streaming mode immediately (always indicates multi-step behavior)

**STREAMING** — Actively editing messages on throttle ticks.
- Accumulates content into internal buffer
- Edits the main message on each throttle tick (~1s interval)
- Skips edit if content hasn't changed since last sent (dirty check)
- On overflow (approaching 4096 chars) → finalize current message, send new one, stay in STREAMING

**FINALIZE** — Prompt resolved (`end_turn`).
- Compare final formatted content against last edit sent
- If different → one final `editMessageText`
- Clean up timers, clear state

---

## Content Rendering

### Main Message Format

Tool calls and response text are interleaved in arrival order:

```
🔨 read_file src/config/app.ts ✅
🔨 grep "TODO" src/ ✅
```
Found 3 matches in 2 files
```

Here's what I found in your config:
The `app.ts` file defines...

🔨 write_file src/config/app.ts ✅

I've updated the configuration to use...
```

### Tool Call Lifecycle

A tool line progresses through states:

```
🔨 <title> ⏳          ← tool_call received (status: in_progress)
🔨 <title> ✅          ← tool_call_update (status: completed)
🔨 <title> ❌          ← tool_call_update (status: failed)
```

The `title` field from the ACP `ToolCall` notification provides both the tool name and description (e.g., "Read file src/config/app.ts").

### Tool Result Previews

When a `tool_call_update` arrives with `status: completed`:

1. Extract a preview: last 100 characters of the tool output
2. Display wrapped in triple-backtick code block below the tool line
3. Start a removal timer: **2 seconds after the next content appends below** (new tool call or response text), the preview is removed via edit

If nothing new appends below, the preview stays visible indefinitely (until something does).

```
🔨 read_file src/config/app.ts ✅
```
...port: 5432, host: "localhost", pool: { max: 10, min: 2 } }
```
```

### Thinking Draft

1. First `agent_thought_chunk` → `sendMessageDraft(chat_id, draft_id=<prompt_id>, text="")`
   - Empty text shows Telegram's native "Thinking..." placeholder
   - Then immediately update with `"🧠 <chunk text>"`
2. More chunks → update draft with accumulated thinking (animated via same `draft_id`)
3. Approaching 30s with no new event → re-send draft with `"🧠 Still thinking..."`
4. New thinking round (after tool use) → append to same draft
5. Draft auto-disappears when new message arrives or 30s elapses

---

## Throttle & Rate Limiting

### Edit Throttle

- Minimum 1000ms between `editMessageText` calls per chat
- If no new chunks arrived since last edit → skip tick (no wasted API call)
- First message send is immediate (perceived responsiveness)

### Telegram Rate Limit Handling

- **429 response:** pause edit timer, wait `retry_after` seconds, then resume
- **`retry_after` > 30s:** treat as permanent failure for editing — stop editing current message, buffer remaining content, send as complete messages when prompt resolves (graceful degradation)
- **Network errors:** exponential backoff, max 3 retries per edit. All retries fail → skip that edit, next tick retries with accumulated content
- **400 "message not modified":** swallow silently (dirty check missed, harmless)

---

## Overflow Handling

When formatted HTML content would exceed Telegram's 4096 character limit:

1. Determine the last clean split point in the current content (reuse existing `splitMessage` logic)
2. Do a final `editMessageText` on the current message with content up to the split point
3. Call `sendMessage` with the overflow content → capture new `message_id`
4. Continue streaming edits on the new message
5. Can overflow multiple times for very long responses

Edge case: if a single chunk is enormous (>4096 chars), split immediately into multiple messages using the existing split logic.

---

## Adaptive Entry (300ms Window)

The 300ms buffer prevents unnecessary edit overhead for short responses:

- Agent responds with 3 words → sent as a single message (no edits at all)
- Agent streams a paragraph → first message sent after 300ms, then edited as more arrives

This applies only to `agent_message_chunk`. If the first ACP notification is `agent_thought_chunk` or `tool_call`, streaming mode is entered immediately — those always indicate multi-step behavior that benefits from live feedback.

---

## Typing Indicator

- Sent on prompt start (during BUFFERING)
- Dropped once first visible content is sent (either thinking draft or main message)
- The live-updating message itself signals "still working"

---

## Error Cases

| Scenario | Behavior |
|----------|----------|
| Tool fails | `🔨 <title> ❌` (from `tool_call_update` with `status: failed`) |
| Prompt errors (agent crash, ACP disconnect) | Send accumulated content as final message, stop |
| `retry_after` > 30s | Stop editing, send remainder as complete messages on prompt completion |
| Network failure (all retries exhausted) | Skip that edit, next tick retries with more content |
| Single chunk > 4096 chars | Split immediately using `splitMessage` logic |

---

## Interface Changes

### AcpAgent.prompt() — New Signature

```typescript
interface PromptOptions {
  onSessionUpdate?: (update: SessionUpdate) => void
}

// Before: prompt(sessionId, text) → string
// After:  prompt(sessionId, text, options?) → string
async prompt(sessionId: string, text: string, options?: PromptOptions): Promise<string>
```

When `onSessionUpdate` is provided, each `session/update` notification is forwarded to the callback as it arrives. The method still returns the final assembled text (backwards compatible for non-streaming callers).

### StreamController — New Class

```typescript
class StreamController {
  constructor(opts: {
    chatId: number
    bot: Bot
    rateLimiter: RateLimiter
    throttleMs?: number       // default 1000
    maxRetryAfter?: number    // default 30s
    draftId?: number          // unique ID for thinking draft
  })

  // Called by onSessionUpdate handler
  pushThinking(text: string): void
  toolStart(toolCallId: string, title: string, status?: string): void
  toolUpdate(toolCallId: string, status: string, content?: ToolCallContent[]): void
  pushText(text: string): void

  // Called when prompt resolves/fails
  finalize(): Promise<void>
  abort(error: Error): Promise<void>
}
```

---

## Files Changed

| File | Change |
|------|--------|
| `channel-manager/src/acp-client.ts` | Add `onSessionUpdate` option to `prompt()`, forward notifications instead of buffering internally |
| `channel-manager/src/channels/telegram/channel.ts` | Wire StreamController into `processMessage`, handle notification dispatch |
| `channel-manager/src/channels/telegram/stream-controller.ts` | **New** — state machine, dual message slot management, throttle, overflow, tool result preview lifecycle |
| `channel-manager/src/channels/telegram/rate-limiter.ts` | Minor: ensure edit throttle interval is configurable (may already work) |
| `channel-manager/src/formatter/telegram.ts` | No changes needed — `formatMarkdown` + `closeOpenTags` already handle partial content |

---

## Testing Strategy

- **Unit tests for StreamController:** mock bot API, fire sequences of notifications, assert correct API calls (sendMessageDraft, sendMessage, editMessageText) with correct timing
- **Throttle tests:** verify edits are batched at ~1s intervals, skipped when content unchanged
- **Overflow tests:** verify message split at 4096 boundary, new message created
- **Tool result preview tests:** verify preview appears, removal timer starts on next content, removed after 2s
- **Thinking draft tests:** verify re-send before 30s timeout, append across rounds
- **Rate limit tests:** verify pause/resume on 429, fallback on excessive retry_after
- **Adaptive entry tests:** verify short responses bypass streaming, long responses enter it
- **Integration test:** end-to-end with real ACP adapter, verify message sequence in Telegram (manual or with test bot)
