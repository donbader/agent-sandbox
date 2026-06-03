import type { ToolCallContent, ToolCallStatus } from "@agentclientprotocol/sdk";
import { formatMarkdown, closeOpenTags, splitMessage, MAX_MESSAGE_LENGTH } from "./formatter/telegram.js";
import { createLogger } from "../logger.js";

const log = createLogger("stream-controller");

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
  id: string;
  title: string;
  status: ToolCallStatus;
  resultPreview?: string;
}

const STATUS_ICONS: Record<string, string> = {
  in_progress: "⏳",
  completed: "✅",
  failed: "❌",
};

const DRAFT_REFRESH_MS = 25_000;

/**
 * Manages the lifecycle of streaming responses to Telegram.
 * State machine: BUFFERING → STREAMING → DONE
 */
export class StreamController {
  private state: State = "BUFFERING";
  private deps: StreamControllerDeps;
  private bufferMs: number;
  private throttleMs: number;
  private draftId: number;

  // Content
  private textChunks: string[] = [];
  private tools: ToolEntry[] = [];
  private thinkingBuffer = "";
  private thinkingDirty = false;
  private contentDirty = false;

  // Message tracking
  private messageId: number | null = null;
  private lastSentHtml = "";
  private overflowing = false;

  // Timers
  private bufferTimer: ReturnType<typeof setTimeout> | null = null;
  private throttleTimer: ReturnType<typeof setInterval> | null = null;
  private draftRefreshTimer: ReturnType<typeof setInterval> | null = null;
  private resultRemovalTimers: Map<string, ReturnType<typeof setTimeout>> = new Map();

  constructor(deps: StreamControllerDeps, opts?: StreamControllerOptions) {
    this.deps = deps;
    this.bufferMs = opts?.bufferMs ?? 300;
    this.throttleMs = opts?.throttleMs ?? 1000;
    this.draftId = opts?.draftId ?? 1;
  }

  // --- Public methods ---

  pushThinking(text: string): void {
    if (this.state === "DONE") return;

    this.thinkingBuffer += text;
    this.thinkingDirty = true;
    this.sendThinkingDraft();

    if (this.state === "BUFFERING") {
      this.enterStreaming();
    }

    this.ensureDraftRefresh();
  }

  toolStart(toolCallId: string, title: string, status?: ToolCallStatus): void {
    if (this.state === "DONE") return;

    this.contentDirty = true;
    this.tools.push({
      id: toolCallId,
      title,
      status: status ?? "in_progress",
    });

    if (this.state === "BUFFERING") {
      this.cancelBufferTimer();
      this.enterStreaming();
    }
  }

  toolUpdate(toolCallId: string, status: ToolCallStatus, content?: ToolCallContent[]): void {
    if (this.state === "DONE") return;

    const tool = this.tools.find((t) => t.id === toolCallId);
    if (!tool) return;

    tool.status = status ?? tool.status;
    this.contentDirty = true;

    if (content && content.length > 0) {
      const textItem = content.find((c): c is ToolCallContent & { type: "content" } =>
        c.type === "content" && c.content.type === "text",
      );
      if (textItem && textItem.content.type === "text") {
        const fullText = textItem.content.text;
        tool.resultPreview = fullText.length > 100 ? fullText.slice(-100) : fullText;
        this.scheduleResultRemoval(tool);
      }
    }
  }

  pushText(text: string): void {
    if (this.state === "DONE") return;

    this.textChunks.push(text);
    this.contentDirty = true;

    if (this.state === "BUFFERING" && !this.bufferTimer) {
      this.startBufferTimer();
    }
  }

  async finalize(): Promise<void> {
    if (this.state === "DONE") return;

    if (this.state === "BUFFERING") {
      this.cancelBufferTimer();
      // Short response — send as single message
      const content = this.renderContent();
      if (content) {
        const html = this.formatForSend(content);
        await this.deps.sendMessage(html, { parse_mode: "HTML" });
      }
      this.cleanup();
      return;
    }

    // STREAMING → DONE: final edit if dirty
    await this.flushEdit();
    this.cleanup();
  }

  async abort(_error: Error): Promise<void> {
    if (this.state === "DONE") return;

    if (this.state === "BUFFERING") {
      this.cancelBufferTimer();
      const content = this.renderContent();
      if (content) {
        const html = this.formatForSend(content);
        await this.deps.sendMessage(html, { parse_mode: "HTML" });
      }
    } else {
      await this.flushEdit();
    }

    this.cleanup();
  }

  // --- Private: State transitions ---

  private startBufferTimer(): void {
    this.bufferTimer = setTimeout(() => {
      this.bufferTimer = null;
      this.enterStreaming();
    }, this.bufferMs);
  }

  private cancelBufferTimer(): void {
    if (this.bufferTimer) {
      clearTimeout(this.bufferTimer);
      this.bufferTimer = null;
    }
  }

  private enterStreaming(): void {
    this.state = "STREAMING";
    this.safeAsync(this.sendInitialMessage());
    this.startThrottleTimer();
  }

  private cleanup(): void {
    this.state = "DONE";
    this.cancelBufferTimer();
    if (this.throttleTimer) {
      clearInterval(this.throttleTimer);
      this.throttleTimer = null;
    }
    if (this.draftRefreshTimer) {
      clearInterval(this.draftRefreshTimer);
      this.draftRefreshTimer = null;
    }
    for (const timer of this.resultRemovalTimers.values()) {
      clearTimeout(timer);
    }
    this.resultRemovalTimers.clear();
  }

  // --- Private: Message sending ---

  private safeAsync(p: Promise<unknown>): void {
    p.catch((err: unknown) => {
      log.warn({ error: (err as Error).message ?? err }, "async operation failed");
    });
  }

  private async sendInitialMessage(): Promise<void> {
    const content = this.renderContent();
    if (!content) return;

    const html = this.formatForSend(content);
    this.lastSentHtml = html;
    this.messageId = await this.deps.sendMessage(html, { parse_mode: "HTML" });
  }

  private startThrottleTimer(): void {
    this.throttleTimer = setInterval(() => {
      this.tickThrottle();
    }, this.throttleMs);
  }

  private tickThrottle(): void {
    if (this.state !== "STREAMING") return;
    if (!this.messageId || this.overflowing) return;

    // Fast dirty check: skip render if nothing changed since last tick
    if (!this.contentDirty) return;
    this.contentDirty = false;

    const content = this.renderContent();
    if (!content) return;

    const html = this.formatForSend(content);

    // Dirty check against last sent
    if (html === this.lastSentHtml) return;

    // Check for overflow
    if (html.length > MAX_MESSAGE_LENGTH) {
      this.handleOverflow(html);
      return;
    }

    this.lastSentHtml = html;
    this.safeAsync(this.deps.editMessage(this.messageId, html, { parse_mode: "HTML" }));
  }

  private async flushEdit(): Promise<void> {
    if (this.messageId === null) return;

    const content = this.renderContent();
    if (!content) return;

    const html = this.formatForSend(content);

    if (html === this.lastSentHtml) return;

    this.lastSentHtml = html;
    await this.deps.editMessage(this.messageId, html, { parse_mode: "HTML" });
  }

  private handleOverflow(html: string): void {
    const segments = splitMessage(html);
    if (segments.length <= 1) {
      // Can't split further, just send as-is
      if (this.messageId !== null) {
        this.lastSentHtml = html;
        this.safeAsync(this.deps.editMessage(this.messageId, html, { parse_mode: "HTML" }));
      }
      return;
    }

    this.overflowing = true;

    // Finalize current message with first segment
    if (this.messageId !== null) {
      this.safeAsync(this.deps.editMessage(this.messageId, segments[0], { parse_mode: "HTML" }));
    }

    // Send remaining as new message, continue streaming into it
    const remaining = segments.slice(1).join("");
    this.lastSentHtml = remaining;
    this.safeAsync(
      this.deps.sendMessage(remaining, { parse_mode: "HTML" }).then((id) => {
        this.messageId = id;
        this.overflowing = false;
      }),
    );
  }

  // --- Private: Thinking draft ---

  private sendThinkingDraft(): void {
    const text = this.thinkingBuffer || "Still thinking...";
    this.safeAsync(this.deps.sendDraft(this.draftId, `🧠 ${text}`));
  }

  private ensureDraftRefresh(): void {
    if (this.draftRefreshTimer) return;
    this.draftRefreshTimer = setInterval(() => {
      if (this.state === "DONE") return;
      if (this.thinkingDirty) {
        this.thinkingDirty = false;
        this.sendThinkingDraft();
      } else {
        this.safeAsync(this.deps.sendDraft(this.draftId, "🧠 Still thinking..."));
      }
    }, DRAFT_REFRESH_MS);
  }

  // --- Private: Content rendering ---

  private scheduleResultRemoval(tool: ToolEntry): void {
    if (this.resultRemovalTimers.has(tool.id)) return;
    const timer = setTimeout(() => {
      this.resultRemovalTimers.delete(tool.id);
      tool.resultPreview = undefined;
      this.contentDirty = true;
      if (this.state === "STREAMING") {
        this.tickThrottle();
      }
    }, 2000);
    this.resultRemovalTimers.set(tool.id, timer);
  }

  private renderContent(): string {
    const parts: string[] = [];

    // Tool lines
    for (const tool of this.tools) {
      const icon = STATUS_ICONS[tool.status] ?? "⏳";
      let line = `🔨 ${tool.title} ${icon}`;
      if (tool.resultPreview) {
        line += `\n\`\`\`\n${tool.resultPreview}\n\`\`\``;
      }
      parts.push(line);
    }

    // Text content
    const text = this.textChunks.join("");
    if (text) {
      if (parts.length > 0) parts.push(""); // blank line separator
      parts.push(text);
    }

    return parts.join("\n");
  }

  private formatForSend(content: string): string {
    const html = formatMarkdown(content);
    return closeOpenTags(html);
  }
}
