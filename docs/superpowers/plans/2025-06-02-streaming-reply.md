# Streaming Reply Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live-stream agent responses to Telegram by editing messages as content arrives, using ACP protocol notifications natively.

**Architecture:** StreamController state machine receives ACP `session/update` notifications (thinking, tool calls, response text) and manages two Telegram message slots: an ephemeral thinking draft (`sendMessageDraft`) and a persistent main message (tool calls + response text, edited on a throttle). The ACP client is modified to forward notifications instead of buffering internally.

**Tech Stack:** TypeScript, grammy (Telegram bot framework), @agentclientprotocol/sdk, vitest

---

## File Structure

| File | Responsibility |
|------|---------------|
| `channel-manager/src/acp-client.ts` | **Modify** — Add `onSessionUpdate` option to `prompt()`, forward notifications to caller |
| `channel-manager/src/safe-prompt.ts` | **Modify** — Add streaming-aware variant that passes `onSessionUpdate` through |
| `internal/plugins/telegram/channel/stream-controller.ts` | **Create** — State machine: BUFFERING → STREAMING → FINALIZE, manages both message slots |
| `internal/plugins/telegram/channel/channel.ts` | **Modify** — Wire StreamController into `processMessage` |
| `internal/plugins/telegram/channel/stream-controller.test.ts` | **Create** — Unit tests for StreamController |
| `channel-manager/src/acp-client.test.ts` | **Modify** — Add tests for `onSessionUpdate` forwarding |

---

### Task 1: Extend AcpAgent.prompt() to Forward Session Updates

**Files:**
- Modify: `channel-manager/src/acp-client.ts:51-74` (BridgeClient.sessionUpdate)
- Modify: `channel-manager/src/acp-client.ts:190-215` (AcpAgent.prompt)
- Test: `channel-manager/src/acp-client.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// In channel-manager/src/acp-client.test.ts — add to existing tests
import { describe, it, expect, vi } from "vitest";

describe("AcpAgent.prompt() with onSessionUpdate", () => {
  it("forwards session update notifications to the callback", async () => {
    // This test verifies the prompt signature accepts onSessionUpdate
    // and that BridgeClient forwards all notifications to it
    const { BridgeClient } = await import("./acp-client.js");
    const client = new BridgeClient();

    const updates: any[] = [];
    client.setSessionUpdateCallback((update) => updates.push(update));

    // Simulate agent_thought_chunk
    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "agent_thought_chunk",
        content: { type: "text", text: "thinking..." },
      },
    } as any);

    // Simulate tool_call
    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "tool_call",
        toolCallId: "tc1",
        title: "Read file",
        status: "in_progress",
      },
    } as any);

    // Simulate agent_message_chunk (should still collect AND forward)
    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "agent_message_chunk",
        content: { type: "text", text: "response" },
      },
    } as any);

    expect(updates).toHaveLength(3);
    expect(updates[0].sessionUpdate).toBe("agent_thought_chunk");
    expect(updates[1].sessionUpdate).toBe("tool_call");
    expect(updates[2].sessionUpdate).toBe("agent_message_chunk");
  });

  it("still collects agent_message_chunk for return value", async () => {
    const { BridgeClient } = await import("./acp-client.js");
    const client = new BridgeClient();

    const chunks: string[] = [];
    client.setChunkCallback((text) => chunks.push(text));
    client.setSessionUpdateCallback(() => {}); // also forwarding

    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "agent_message_chunk",
        content: { type: "text", text: "hello " },
      },
    } as any);

    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "agent_message_chunk",
        content: { type: "text", text: "world" },
      },
    } as any);

    expect(chunks).toEqual(["hello ", "world"]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd channel-manager && npx vitest run src/acp-client.test.ts`
Expected: FAIL — `setSessionUpdateCallback` does not exist

- [ ] **Step 3: Implement BridgeClient.setSessionUpdateCallback**

In `channel-manager/src/acp-client.ts`, add to `BridgeClient`:

```typescript
export class BridgeClient implements acp.Client {
  private chunkCallback: ((text: string) => void) | null = null;
  private commandsCallback: ((commands: AgentCommand[]) => void) | null = null;
  private sessionUpdateCallback: ((update: any) => void) | null = null;

  setChunkCallback(cb: ((text: string) => void) | null): void {
    this.chunkCallback = cb;
  }

  setCommandsCallback(cb: ((commands: AgentCommand[]) => void) | null): void {
    this.commandsCallback = cb;
  }

  setSessionUpdateCallback(cb: ((update: any) => void) | null): void {
    this.sessionUpdateCallback = cb;
  }

  // ... requestPermission unchanged ...

  async sessionUpdate(params: acp.SessionNotification): Promise<void> {
    const { update } = params;

    // Forward ALL updates to the session update callback
    this.sessionUpdateCallback?.(update);

    // Still collect agent_message_chunk for backwards compat (prompt return value)
    if (
      update.sessionUpdate === "agent_message_chunk" &&
      update.content.type === "text"
    ) {
      this.chunkCallback?.(update.content.text);
    } else if (update.sessionUpdate === "available_commands_update") {
      const cmds = (update as any).availableCommands as Array<{
        name: string;
        description?: string;
        input?: { hint?: string } | null;
      }>;
      if (Array.isArray(cmds)) {
        const agentCommands: AgentCommand[] = cmds.map((c) => ({
          name: c.name,
          description: c.description ?? "",
          inputHint: c.input?.hint,
        }));
        log.info({ count: agentCommands.length }, "received agent commands");
        this.commandsCallback?.(agentCommands);
      }
    }
  }
}
```

- [ ] **Step 4: Add onSessionUpdate option to AcpAgent.prompt()**

```typescript
export interface PromptOptions {
  onSessionUpdate?: (update: any) => void;
}

/**
 * Sends a prompt to the agent and returns the full response text.
 * Collects all agent_message_chunk updates until the prompt completes.
 * If onSessionUpdate is provided, all session/update notifications are forwarded.
 */
async prompt(sessionId: string, text: string, options?: PromptOptions): Promise<string> {
  if (!this.connection) {
    throw new Error("ACP agent not started");
  }

  const chunks: string[] = [];

  return new Promise<string>((resolve, reject) => {
    this.acpHandler.setChunkCallback((chunk) => chunks.push(chunk));
    this.acpHandler.setSessionUpdateCallback(options?.onSessionUpdate ?? null);
    this.pendingReject = reject;

    this.connection!.prompt({
      sessionId,
      prompt: [{ type: "text", text }],
    })
      .then(() => {
        this.pendingReject = null;
        this.acpHandler.setChunkCallback(null);
        this.acpHandler.setSessionUpdateCallback(null);
        resolve(chunks.join(""));
      })
      .catch((err: unknown) => {
        this.pendingReject = null;
        this.acpHandler.setChunkCallback(null);
        this.acpHandler.setSessionUpdateCallback(null);
        reject(err instanceof Error ? err : new Error(String(err)));
      });
  });
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd channel-manager && npx vitest run src/acp-client.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add channel-manager/src/acp-client.ts channel-manager/src/acp-client.test.ts
git commit -m "feat(acp-client): add onSessionUpdate forwarding to prompt()"
```

---

### Task 2: Create StreamController — Core State Machine

**Files:**
- Create: `internal/plugins/telegram/channel/stream-controller.ts`
- Create: `internal/plugins/telegram/channel/stream-controller.test.ts`

- [ ] **Step 1: Write failing test — BUFFERING state with short response**

```typescript
// internal/plugins/telegram/channel/stream-controller.test.ts
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { StreamController, type StreamControllerDeps } from "./stream-controller.js";

function createMockDeps(): StreamControllerDeps & { calls: any[] } {
  const calls: any[] = [];
  return {
    calls,
    chatId: 123,
    sendMessage: vi.fn(async (text: string, opts?: any) => {
      calls.push({ type: "sendMessage", text, opts });
      return 1001; // message_id
    }),
    editMessage: vi.fn(async (messageId: number, text: string, opts?: any) => {
      calls.push({ type: "editMessage", messageId, text, opts });
    }),
    sendDraft: vi.fn(async (draftId: number, text: string) => {
      calls.push({ type: "sendDraft", draftId, text });
    }),
  };
}

describe("StreamController", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("BUFFERING state — adaptive entry", () => {
    it("sends short response as single message (no streaming)", async () => {
      const deps = createMockDeps();
      const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

      ctrl.pushText("Short response.");
      ctrl.finalize();

      // Should send a single message, no edits
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);
      expect(deps.sendMessage).toHaveBeenCalledWith(
        expect.stringContaining("Short response."),
        expect.anything(),
      );
      expect(deps.editMessage).not.toHaveBeenCalled();
    });

    it("enters streaming after 300ms buffer expires", async () => {
      const deps = createMockDeps();
      const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

      ctrl.pushText("First chunk ");

      // Advance past buffer window
      await vi.advanceTimersByTimeAsync(300);

      // Should have sent the first message
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);

      // More text arrives — should edit
      ctrl.pushText("second chunk ");
      await vi.advanceTimersByTimeAsync(1000);

      expect(deps.editMessage).toHaveBeenCalled();
    });

    it("enters streaming immediately on tool_call", async () => {
      const deps = createMockDeps();
      const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

      ctrl.toolStart("tc1", "Read file src/main.ts", "in_progress");

      // Should send main message immediately (no 300ms wait)
      expect(deps.sendMessage).toHaveBeenCalledTimes(1);
      expect(deps.sendMessage).toHaveBeenCalledWith(
        expect.stringContaining("🔨"),
        expect.anything(),
      );
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/plugins/telegram/channel && npx vitest run stream-controller.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Implement StreamController skeleton — states and pushText/finalize**

```typescript
// internal/plugins/telegram/channel/stream-controller.ts
import { formatMarkdown, closeOpenTags, splitMessage, MAX_MESSAGE_LENGTH } from "./formatter/telegram.js";

export interface StreamControllerDeps {
  chatId: number;
  sendMessage(text: string, opts?: { parse_mode?: string }): Promise<number>;
  editMessage(messageId: number, text: string, opts?: { parse_mode?: string }): Promise<void>;
  sendDraft(draftId: number, text: string): Promise<void>;
}

export interface StreamControllerOptions {
  bufferMs?: number;
  throttleMs?: number;
  maxRetryAfterMs?: number;
  draftId?: number;
}

type State = "BUFFERING" | "STREAMING" | "DONE";

interface ToolEntry {
  toolCallId: string;
  title: string;
  status: string; // "in_progress" | "completed" | "failed"
  resultPreview?: string;
  resultTimer?: ReturnType<typeof setTimeout>;
}

export class StreamController {
  private state: State = "BUFFERING";
  private deps: StreamControllerDeps;
  private opts: Required<StreamControllerOptions>;

  // Content buffers
  private textBuffer = "";
  private tools: ToolEntry[] = [];
  private contentParts: Array<{ type: "tool" | "text"; value: string }> = [];

  // Message tracking
  private mainMessageId: number | null = null;
  private lastSentHtml = "";

  // Timers
  private bufferTimer: ReturnType<typeof setTimeout> | null = null;
  private throttleTimer: ReturnType<typeof setInterval> | null = null;

  // Thinking draft
  private thinkingBuffer = "";
  private draftActive = false;
  private draftRefreshTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(deps: StreamControllerDeps, options?: StreamControllerOptions) {
    this.deps = deps;
    this.opts = {
      bufferMs: options?.bufferMs ?? 300,
      throttleMs: options?.throttleMs ?? 1000,
      maxRetryAfterMs: options?.maxRetryAfterMs ?? 30000,
      draftId: options?.draftId ?? 1,
    };
  }

  // --- Public API ---

  pushThinking(text: string): void {
    if (this.state === "DONE") return;
    this.thinkingBuffer += text;
    this.sendOrUpdateDraft();
  }

  toolStart(toolCallId: string, title: string, status?: string): void {
    if (this.state === "DONE") return;

    const entry: ToolEntry = {
      toolCallId,
      title,
      status: status ?? "in_progress",
    };
    this.tools.push(entry);
    this.contentParts.push({ type: "tool", value: toolCallId });

    // Tool calls always trigger immediate streaming
    if (this.state === "BUFFERING") {
      this.cancelBufferTimer();
      this.enterStreaming();
    }

    this.scheduleResultRemoval();
    this.editMainMessage();
  }

  toolUpdate(toolCallId: string, status: string, content?: any[]): void {
    if (this.state === "DONE") return;

    const entry = this.tools.find((t) => t.toolCallId === toolCallId);
    if (!entry) return;

    entry.status = status;

    // Extract result preview (last 100 chars)
    if (content && content.length > 0 && (status === "completed" || status === "failed")) {
      const textContent = content.find((c: any) => c.type === "content" && c.content?.type === "text");
      if (textContent) {
        const fullText = textContent.content.text as string;
        entry.resultPreview = fullText.length > 100 ? fullText.slice(-100) : fullText;
      }
    }

    this.editMainMessage();
  }

  pushText(text: string): void {
    if (this.state === "DONE") return;

    this.textBuffer += text;

    if (this.state === "BUFFERING") {
      // Start buffer timer on first text chunk
      if (!this.bufferTimer) {
        this.bufferTimer = setTimeout(() => {
          this.enterStreaming();
        }, this.opts.bufferMs);
      }
      return;
    }

    // Already streaming — throttle handles edits
    this.scheduleResultRemoval();
  }

  async finalize(): Promise<void> {
    if (this.state === "DONE") return;

    this.cancelBufferTimer();
    this.cancelThrottleTimer();
    this.cancelDraftRefresh();

    if (this.state === "BUFFERING") {
      // Never entered streaming — send as single message
      const content = this.buildMainContent();
      if (content) {
        const html = this.formatForTelegram(content);
        await this.deps.sendMessage(html, { parse_mode: "HTML" });
      }
    } else {
      // Final edit if dirty
      await this.doFinalEdit();
    }

    this.state = "DONE";
  }

  async abort(_error: Error): Promise<void> {
    this.cancelBufferTimer();
    this.cancelThrottleTimer();
    this.cancelDraftRefresh();

    // Send whatever we have
    const content = this.buildMainContent();
    if (content && !this.mainMessageId) {
      const html = this.formatForTelegram(content);
      await this.deps.sendMessage(html, { parse_mode: "HTML" });
    } else if (content && this.mainMessageId) {
      await this.doFinalEdit();
    }

    this.state = "DONE";
  }

  // --- Private: State transitions ---

  private async enterStreaming(): Promise<void> {
    this.state = "STREAMING";
    const content = this.buildMainContent();
    if (content) {
      const html = this.formatForTelegram(content);
      this.mainMessageId = await this.deps.sendMessage(html, { parse_mode: "HTML" });
      this.lastSentHtml = html;
    }
    this.startThrottleTimer();
  }

  // --- Private: Content building ---

  private buildMainContent(): string {
    let content = "";

    // Interleave tools and text based on contentParts ordering
    // For simplicity, render all tools first (in order), then pending text
    for (const entry of this.tools) {
      const icon = entry.status === "completed" ? "✅" : entry.status === "failed" ? "❌" : "⏳";
      content += `🔨 ${entry.title} ${icon}\n`;
      if (entry.resultPreview) {
        content += "```\n" + entry.resultPreview + "\n```\n";
      }
    }

    if (this.textBuffer) {
      if (this.tools.length > 0) content += "\n";
      content += this.textBuffer;
    }

    return content;
  }

  private formatForTelegram(content: string): string {
    const html = formatMarkdown(content);
    return closeOpenTags(html);
  }

  // --- Private: Edit logic ---

  private async editMainMessage(): Promise<void> {
    if (!this.mainMessageId) {
      // First content — send the message
      const content = this.buildMainContent();
      if (content) {
        const html = this.formatForTelegram(content);
        this.mainMessageId = await this.deps.sendMessage(html, { parse_mode: "HTML" });
        this.lastSentHtml = html;
      }
      return;
    }

    const content = this.buildMainContent();
    const html = this.formatForTelegram(content);

    if (html === this.lastSentHtml) return; // skip if unchanged

    // Check overflow
    if (html.length > MAX_MESSAGE_LENGTH) {
      await this.handleOverflow(html);
      return;
    }

    await this.deps.editMessage(this.mainMessageId, html, { parse_mode: "HTML" });
    this.lastSentHtml = html;
  }

  private async doFinalEdit(): Promise<void> {
    if (!this.mainMessageId) return;

    // Remove any remaining result previews for final message
    for (const entry of this.tools) {
      if (entry.resultTimer) {
        clearTimeout(entry.resultTimer);
        entry.resultTimer = undefined;
      }
      entry.resultPreview = undefined;
    }

    const content = this.buildMainContent();
    const html = this.formatForTelegram(content);

    if (html !== this.lastSentHtml) {
      await this.deps.editMessage(this.mainMessageId, html, { parse_mode: "HTML" });
      this.lastSentHtml = html;
    }
  }

  // --- Private: Overflow ---

  private async handleOverflow(html: string): Promise<void> {
    // Finalize current message at split boundary
    const segments = splitMessage(html);
    if (segments.length < 2) return;

    // Edit current message with first segment
    await this.deps.editMessage(this.mainMessageId!, segments[0], { parse_mode: "HTML" });

    // Send overflow as new message
    this.mainMessageId = await this.deps.sendMessage(segments[1], { parse_mode: "HTML" });
    this.lastSentHtml = segments[1];

    // If there are more segments (unlikely in streaming), send them
    for (let i = 2; i < segments.length; i++) {
      this.mainMessageId = await this.deps.sendMessage(segments[i], { parse_mode: "HTML" });
      this.lastSentHtml = segments[i];
    }
  }

  // --- Private: Tool result removal ---

  private scheduleResultRemoval(): void {
    // When new content appends below a tool result, schedule its removal in 2s
    for (const entry of this.tools) {
      if (entry.resultPreview && !entry.resultTimer) {
        entry.resultTimer = setTimeout(() => {
          entry.resultPreview = undefined;
          entry.resultTimer = undefined;
          if (this.state === "STREAMING") {
            this.editMainMessage();
          }
        }, 2000);
      }
    }
  }

  // --- Private: Thinking draft ---

  private async sendOrUpdateDraft(): Promise<void> {
    const text = `🧠 ${this.thinkingBuffer}`;
    await this.deps.sendDraft(this.opts.draftId, text);
    this.draftActive = true;
    this.resetDraftRefresh();
  }

  private resetDraftRefresh(): void {
    this.cancelDraftRefresh();
    // Re-send before 30s timeout to keep draft alive
    this.draftRefreshTimer = setTimeout(async () => {
      if (this.state === "DONE") return;
      if (this.draftActive) {
        const text = this.thinkingBuffer
          ? `🧠 ${this.thinkingBuffer}`
          : "🧠 Still thinking...";
        await this.deps.sendDraft(this.opts.draftId, text);
        this.resetDraftRefresh();
      }
    }, 25000); // refresh at 25s (before 30s timeout)
  }

  // --- Private: Timers ---

  private startThrottleTimer(): void {
    this.throttleTimer = setInterval(() => {
      this.editMainMessage();
    }, this.opts.throttleMs);
  }

  private cancelBufferTimer(): void {
    if (this.bufferTimer) {
      clearTimeout(this.bufferTimer);
      this.bufferTimer = null;
    }
  }

  private cancelThrottleTimer(): void {
    if (this.throttleTimer) {
      clearInterval(this.throttleTimer);
      this.throttleTimer = null;
    }
  }

  private cancelDraftRefresh(): void {
    if (this.draftRefreshTimer) {
      clearTimeout(this.draftRefreshTimer);
      this.draftRefreshTimer = null;
    }
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd internal/plugins/telegram/channel && npx vitest run stream-controller.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/telegram/channel/stream-controller.ts internal/plugins/telegram/channel/stream-controller.test.ts
git commit -m "feat(telegram): add StreamController state machine"
```

---

### Task 3: StreamController Tests — Thinking Draft

**Files:**
- Modify: `internal/plugins/telegram/channel/stream-controller.test.ts`

- [ ] **Step 1: Write tests for thinking draft behavior**

```typescript
describe("Thinking draft", () => {
  it("sends draft on first thinking chunk", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushThinking("Let me analyze ");

    expect(deps.sendDraft).toHaveBeenCalledWith(1, "🧠 Let me analyze ");
  });

  it("updates draft with accumulated thinking", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushThinking("Let me ");
    ctrl.pushThinking("think about this...");

    expect(deps.sendDraft).toHaveBeenLastCalledWith(1, "🧠 Let me think about this...");
  });

  it("refreshes draft before 30s timeout", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushThinking("Analyzing...");
    expect(deps.sendDraft).toHaveBeenCalledTimes(1);

    // Advance to 25s — should refresh
    await vi.advanceTimersByTimeAsync(25000);

    expect(deps.sendDraft).toHaveBeenCalledTimes(2);
    expect(deps.sendDraft).toHaveBeenLastCalledWith(1, "🧠 Analyzing...");
  });

  it("shows 'Still thinking...' on refresh if no new content", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    // Send empty thinking (just to activate draft)
    ctrl.pushThinking("");

    // Advance to refresh
    await vi.advanceTimersByTimeAsync(25000);

    // thinkingBuffer is "" so it shows "Still thinking..."
    expect(deps.sendDraft).toHaveBeenLastCalledWith(1, "🧠 Still thinking...");
  });
});
```

- [ ] **Step 2: Run tests**

Run: `cd internal/plugins/telegram/channel && npx vitest run stream-controller.test.ts`
Expected: PASS (implementation already handles this)

- [ ] **Step 3: Commit**

```bash
git add internal/plugins/telegram/channel/stream-controller.test.ts
git commit -m "test(telegram): add thinking draft tests for StreamController"
```

---

### Task 4: StreamController Tests — Tool Calls & Result Previews

**Files:**
- Modify: `internal/plugins/telegram/channel/stream-controller.test.ts`

- [ ] **Step 1: Write tests for tool lifecycle and result preview removal**

```typescript
describe("Tool calls", () => {
  it("shows tool with ⏳ on start", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.toolStart("tc1", "Read file src/main.ts", "in_progress");

    expect(deps.sendMessage).toHaveBeenCalledWith(
      expect.stringContaining("🔨 Read file src/main.ts ⏳"),
      expect.anything(),
    );
  });

  it("updates tool to ✅ on completion", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.toolStart("tc1", "Read file src/main.ts", "in_progress");
    ctrl.toolUpdate("tc1", "completed", [
      { type: "content", content: { type: "text", text: "file contents here" } },
    ]);

    expect(deps.editMessage).toHaveBeenCalledWith(
      1001,
      expect.stringContaining("✅"),
      expect.anything(),
    );
  });

  it("shows ❌ on tool failure", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.toolStart("tc1", "Read file src/missing.ts", "in_progress");
    ctrl.toolUpdate("tc1", "failed");

    expect(deps.editMessage).toHaveBeenCalledWith(
      1001,
      expect.stringContaining("❌"),
      expect.anything(),
    );
  });

  it("shows truncated result preview (last 100 chars)", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    const longResult = "x".repeat(200);
    ctrl.toolStart("tc1", "Read file", "in_progress");
    ctrl.toolUpdate("tc1", "completed", [
      { type: "content", content: { type: "text", text: longResult } },
    ]);

    // Should contain last 100 chars
    expect(deps.editMessage).toHaveBeenCalledWith(
      1001,
      expect.stringContaining("x".repeat(100)),
      expect.anything(),
    );
  });

  it("removes result preview 2s after next content appends", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.toolStart("tc1", "Read file", "in_progress");
    ctrl.toolUpdate("tc1", "completed", [
      { type: "content", content: { type: "text", text: "some result" } },
    ]);

    // Result preview is showing
    expect(deps.editMessage).toHaveBeenCalledWith(
      1001,
      expect.stringContaining("some result"),
      expect.anything(),
    );

    // New text arrives — triggers 2s removal timer
    ctrl.pushText("Here is my response");
    await vi.advanceTimersByTimeAsync(1000); // throttle tick

    // After 2s total, result should be gone
    await vi.advanceTimersByTimeAsync(2000);

    // Find the last editMessage call — should NOT contain "some result"
    const lastEdit = deps.editMessage.mock.calls[deps.editMessage.mock.calls.length - 1];
    expect(lastEdit[1]).not.toContain("some result");
  });
});
```

- [ ] **Step 2: Run tests**

Run: `cd internal/plugins/telegram/channel && npx vitest run stream-controller.test.ts`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/plugins/telegram/channel/stream-controller.test.ts
git commit -m "test(telegram): add tool call and result preview tests"
```

---

### Task 5: StreamController Tests — Throttle, Overflow, and Error Handling

**Files:**
- Modify: `internal/plugins/telegram/channel/stream-controller.test.ts`

- [ ] **Step 1: Write tests for throttle and overflow**

```typescript
describe("Throttle and edit batching", () => {
  it("batches edits at throttle interval", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushText("First ");
    await vi.advanceTimersByTimeAsync(300); // enter streaming
    deps.editMessage.mockClear();

    // Multiple chunks arrive rapidly
    ctrl.pushText("second ");
    ctrl.pushText("third ");
    ctrl.pushText("fourth ");

    // No edit yet (under throttle interval)
    expect(deps.editMessage).not.toHaveBeenCalled();

    // After throttle tick
    await vi.advanceTimersByTimeAsync(1000);
    expect(deps.editMessage).toHaveBeenCalledTimes(1);
    expect(deps.editMessage).toHaveBeenCalledWith(
      1001,
      expect.stringContaining("second"),
      expect.anything(),
    );
  });

  it("skips edit when content unchanged", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushText("Hello");
    await vi.advanceTimersByTimeAsync(300); // enter streaming
    deps.editMessage.mockClear();

    // No new content
    await vi.advanceTimersByTimeAsync(1000);
    expect(deps.editMessage).not.toHaveBeenCalled();
  });
});

describe("Overflow", () => {
  it("sends new message when content exceeds 4096 chars", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    // Send initial content
    ctrl.pushText("Start ");
    await vi.advanceTimersByTimeAsync(300); // enter streaming

    // Push enough text to overflow
    ctrl.pushText("x".repeat(4200));
    await vi.advanceTimersByTimeAsync(1000); // throttle tick triggers overflow

    // Should have edited current message AND sent a new one
    expect(deps.editMessage).toHaveBeenCalled();
    expect(deps.sendMessage).toHaveBeenCalledTimes(2); // initial + overflow
  });
});

describe("Finalize", () => {
  it("does final edit if content differs from last sent", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushText("Hello ");
    await vi.advanceTimersByTimeAsync(300); // enter streaming

    ctrl.pushText("world");
    // Don't wait for throttle — finalize immediately
    await ctrl.finalize();

    // Should have done a final edit with complete content
    expect(deps.editMessage).toHaveBeenCalledWith(
      1001,
      expect.stringContaining("world"),
      expect.anything(),
    );
  });

  it("skips final edit if content matches last sent", async () => {
    const deps = createMockDeps();
    const ctrl = new StreamController(deps, { bufferMs: 300, throttleMs: 1000 });

    ctrl.pushText("Hello");
    await vi.advanceTimersByTimeAsync(300); // enter streaming

    // Wait for throttle tick (content gets sent)
    await vi.advanceTimersByTimeAsync(1000);
    deps.editMessage.mockClear();

    // Finalize with no new content
    await ctrl.finalize();
    expect(deps.editMessage).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run tests**

Run: `cd internal/plugins/telegram/channel && npx vitest run stream-controller.test.ts`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/plugins/telegram/channel/stream-controller.test.ts
git commit -m "test(telegram): add throttle, overflow, and finalize tests"
```

---

### Task 6: Wire StreamController into Channel

**Files:**
- Modify: `internal/plugins/telegram/channel/channel.ts:174-207` (processMessage function)

- [ ] **Step 1: Write a failing integration test**

Add to `internal/plugins/telegram/channel/channel.test.ts`:

```typescript
describe("streaming replies", () => {
  it("uses StreamController for streaming responses", async () => {
    // Override agent.prompt to simulate streaming via onSessionUpdate
    agent.prompt.mockImplementation(
      async (sessionId: string, text: string, options?: any) => {
        // Simulate thinking
        options?.onSessionUpdate?.({
          sessionUpdate: "agent_thought_chunk",
          content: { type: "text", text: "Let me think..." },
        });

        // Simulate tool call
        options?.onSessionUpdate?.({
          sessionUpdate: "tool_call",
          toolCallId: "tc1",
          title: "Read file",
          status: "in_progress",
        });

        // Simulate tool completion
        options?.onSessionUpdate?.({
          sessionUpdate: "tool_call_update",
          toolCallId: "tc1",
          status: "completed",
          content: [{ type: "content", content: { type: "text", text: "file data" } }],
        });

        // Simulate response chunks
        options?.onSessionUpdate?.({
          sessionUpdate: "agent_message_chunk",
          content: { type: "text", text: "Here is your answer." },
        });

        return "Here is your answer.";
      },
    );

    messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello" }));
    await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

    // Verify prompt was called with onSessionUpdate option
    expect(agent.prompt).toHaveBeenCalledWith(
      "test-session-123",
      "hello",
      expect.objectContaining({ onSessionUpdate: expect.any(Function) }),
    );
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/plugins/telegram/channel && npx vitest run channel.test.ts`
Expected: FAIL — prompt still called with only 2 args (no options)

- [ ] **Step 3: Update processMessage to use StreamController**

Replace the current `processMessage` in `channel.ts`:

```typescript
import { StreamController } from "./stream-controller.js";

async function processMessage(
  chatId: string,
  text: string,
  messageId: number,
): Promise<void> {
  // Ack
  if (ackEmoji) {
    ackMessage(chatId, messageId);
  }

  // Typing indicator
  sendTyping(chatId);

  const sessionId = await getOrCreateSession(chatId);

  // Command routing — all forwarded to agent (wrapper handles these)
  const cleanText = text.startsWith("/") ? normalizeCommand(text) : text;

  // Create StreamController for this response
  const stream = new StreamController(
    {
      chatId: Number(chatId),
      sendMessage: async (html, opts) => {
        await rateLimiter.acquire(chatId);
        const msg = await withRetry(async () =>
          bot.api.sendMessage(Number(chatId), html, {
            parse_mode: opts?.parse_mode ?? "HTML",
            link_preview_options: { is_disabled: true },
          }),
        );
        return msg?.message_id ?? 0;
      },
      editMessage: async (msgId, html, opts) => {
        await rateLimiter.acquire(chatId);
        await withRetry(async () =>
          bot.api.editMessageText(Number(chatId), msgId, html, {
            parse_mode: opts?.parse_mode ?? "HTML",
            link_preview_options: { is_disabled: true },
          }),
        );
      },
      sendDraft: async (draftId, draftText) => {
        await withRetry(async () =>
          (bot.api as any).sendMessageDraft(Number(chatId), {
            draft_id: draftId,
            text: draftText,
            parse_mode: "HTML",
          }),
        );
      },
    },
    { throttleMs: 1000, bufferMs: 300 },
  );

  try {
    await agent.prompt(sessionId, cleanText, {
      onSessionUpdate(update: any) {
        switch (update.sessionUpdate) {
          case "agent_thought_chunk":
            if (update.content?.type === "text") {
              stream.pushThinking(update.content.text);
            }
            break;
          case "tool_call":
            stream.toolStart(update.toolCallId, update.title, update.status);
            break;
          case "tool_call_update":
            stream.toolUpdate(update.toolCallId, update.status, update.content);
            break;
          case "agent_message_chunk":
            if (update.content?.type === "text") {
              stream.pushText(update.content.text);
            }
            break;
        }
      },
    });
    await stream.finalize();
  } catch (err) {
    await stream.abort(err instanceof Error ? err : new Error(String(err)));
    log.error({ chatId, error: (err as Error).message }, "prompt failed");
  }
}

/** Normalize a /command@botname into /command args */
function normalizeCommand(text: string): string {
  const spaceIdx = text.indexOf(" ");
  const cmd = spaceIdx === -1 ? text.slice(1) : text.slice(1, spaceIdx);
  const cleanCmd = cmd.split("@")[0];
  const args = spaceIdx === -1 ? "" : text.slice(spaceIdx + 1).trim();
  return args ? `/${cleanCmd} ${args}` : `/${cleanCmd}`;
}
```

- [ ] **Step 4: Remove old sendMessage-based response path**

The old `sendMessage(chatId, result.ok ? result.response : result.error)` calls are replaced by StreamController. Remove the `safePrompt` import since error handling is now inline.

- [ ] **Step 5: Run all channel tests**

Run: `cd internal/plugins/telegram/channel && npx vitest run`
Expected: PASS — existing tests may need mock updates for the new `prompt()` signature

- [ ] **Step 6: Fix any test failures from signature change**

The existing channel tests mock `agent.prompt` returning a string. Now it's called with 3 args. Update mock expectations: `agent.prompt` should accept the third options arg and still resolve to a string.

- [ ] **Step 7: Run full test suite**

Run: `cd channel-manager && npx vitest run`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/plugins/telegram/channel/channel.ts internal/plugins/telegram/channel/channel.test.ts
git commit -m "feat(telegram): wire StreamController into processMessage"
```

---

### Task 7: Handle sendMessageDraft in grammy

**Files:**
- Modify: `internal/plugins/telegram/channel/channel.ts` (sendDraft implementation)

The grammy library may not have native support for `sendMessageDraft` (added in Bot API 10.0, May 2026). We need to call it via grammy's raw API method.

- [ ] **Step 1: Check grammy's raw API access pattern**

grammy provides `bot.api.raw.sendMessageDraft` or `bot.api.callApi("sendMessageDraft", params)` for newer API methods. Verify the correct approach in the grammy docs or types.

- [ ] **Step 2: Implement sendDraft using grammy's raw API**

Update the `sendDraft` dep in channel.ts:

```typescript
sendDraft: async (draftId, draftText) => {
  await withRetry(async () =>
    bot.api.callApi("sendMessageDraft", {
      chat_id: Number(chatId),
      draft_id: draftId,
      text: draftText,
      parse_mode: "HTML",
    }),
  );
},
```

If `callApi` isn't available, use:

```typescript
sendDraft: async (draftId, draftText) => {
  await withRetry(async () =>
    (bot.api as any).raw.sendMessageDraft({
      chat_id: Number(chatId),
      draft_id: draftId,
      text: draftText,
      parse_mode: "HTML",
    }),
  );
},
```

- [ ] **Step 3: Write a unit test for draft sending**

```typescript
it("calls sendMessageDraft API for thinking content", async () => {
  // Simulate agent that only thinks then responds
  agent.prompt.mockImplementation(async (_sid: string, _text: string, opts?: any) => {
    opts?.onSessionUpdate?.({
      sessionUpdate: "agent_thought_chunk",
      content: { type: "text", text: "Thinking..." },
    });
    opts?.onSessionUpdate?.({
      sessionUpdate: "agent_message_chunk",
      content: { type: "text", text: "Answer" },
    });
    return "Answer";
  });

  messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "question" }));
  await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

  // Verify sendMessageDraft was called (via bot.api mock)
  // The exact assertion depends on how the mock is set up for callApi
});
```

- [ ] **Step 4: Run tests**

Run: `cd internal/plugins/telegram/channel && npx vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/telegram/channel/channel.ts internal/plugins/telegram/channel/channel.test.ts
git commit -m "feat(telegram): implement sendMessageDraft via grammy raw API"
```

---

### Task 8: Update safePrompt for Streaming Compatibility

**Files:**
- Modify: `channel-manager/src/safe-prompt.ts`

The current `safePrompt` wraps `agent.prompt(sessionId, text)`. It needs to pass through the options.

- [ ] **Step 1: Write failing test**

```typescript
// In channel-manager/src/safe-prompt.test.ts — add test
it("passes options through to agent.prompt", async () => {
  const mockAgent = {
    prompt: vi.fn().mockResolvedValue("response"),
  };
  const onSessionUpdate = vi.fn();
  const result = await safePrompt(mockAgent as any, "s1", "hello", { onSessionUpdate });
  expect(mockAgent.prompt).toHaveBeenCalledWith("s1", "hello", { onSessionUpdate });
  expect(result).toEqual({ ok: true, response: "response" });
});
```

- [ ] **Step 2: Update safePrompt signature**

```typescript
import type { PromptOptions } from "./acp-client.js";

export async function safePrompt(
  agent: AcpAgent,
  sessionId: string,
  text: string,
  options?: PromptOptions,
): Promise<{ ok: true; response: string } | { ok: false; error: string }> {
  try {
    const response = await agent.prompt(sessionId, text, options);
    return { ok: true, response };
  } catch (err: unknown) {
    log.error({ error: err, sessionId }, "agent prompt failed");
    return { ok: false, error: "⚠️ Agent unavailable. Try again shortly." };
  }
}
```

- [ ] **Step 3: Run tests**

Run: `cd channel-manager && npx vitest run src/safe-prompt.test.ts`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add channel-manager/src/safe-prompt.ts channel-manager/src/safe-prompt.test.ts
git commit -m "feat(safe-prompt): pass PromptOptions through to agent.prompt()"
```

---

### Task 9: Update RateLimiter for Edit Throttle

**Files:**
- Modify: `internal/plugins/telegram/channel/delivery/rate-limiter.ts`
- Modify: `internal/plugins/telegram/channel/delivery/rate-limiter.test.ts`

The current rate limiter enforces 100ms between sends. For streaming edits, we need to support different intervals per operation type, or at minimum allow callers to construct with a different interval.

- [ ] **Step 1: Verify current RateLimiter is sufficient**

The existing `RateLimiter(100)` in channel.ts enforces 100ms between *sends*. For edits during streaming, the StreamController handles its own 1000ms throttle internally. The rate limiter just prevents API overload on rapid sequential calls.

This means the existing rate limiter is actually fine as-is. The StreamController's throttle timer provides the 1s edit interval, and the rate limiter adds a 100ms floor to prevent burst if multiple messages fire close together.

- [ ] **Step 2: No code changes needed — document this decision**

The rate limiter stays at 100ms. StreamController owns the 1s edit throttle. These are complementary:
- StreamController throttle: "don't edit more than once per second"
- Rate limiter: "don't fire any API call within 100ms of the previous one for the same chat"

- [ ] **Step 3: Commit (skip if no changes)**

No commit needed for this task.

---

### Task 10: Integration Smoke Test

**Files:**
- None (manual verification)

- [ ] **Step 1: Run full test suite**

```bash
cd channel-manager && npx vitest run
cd internal/plugins/telegram/channel && npx vitest run
```

Expected: All tests pass.

- [ ] **Step 2: Type check**

```bash
cd channel-manager && npx tsc --noEmit
```

Expected: No type errors.

- [ ] **Step 3: Verify import paths are correct**

The channel at `internal/plugins/telegram/channel/channel.ts` imports from `"./stream-controller.js"`. Ensure the new file is at the right path and exports `StreamController`.

- [ ] **Step 4: Commit any remaining fixes**

```bash
git add -A
git commit -m "chore: fix any remaining type/import issues from streaming integration"
```
