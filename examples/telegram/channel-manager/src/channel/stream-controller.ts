import type { ToolCallContent, ToolCallStatus } from "@agentclientprotocol/sdk";
import { formatMarkdown, closeOpenTags, splitMessage } from "./formatter/telegram.js";
import { createLogger } from "../logger.js";

const log = createLogger("stream-controller");

// === Types ===

export interface StreamControllerDeps {
  chatId: number;
  sendMessage(text: string, opts?: { parse_mode?: string }): Promise<number>;
  editMessage(messageId: number, text: string, opts?: { parse_mode?: string }): Promise<void>;
  sendDraft(draftId: number, text: string): Promise<void>;
}

export interface StreamControllerOptions {
  editIntervalMs?: number;
  draftDebounceMs?: number;
}

interface WorkSection {
  type: "thinking" | "tool";
  startedAt: number;
  endedAt?: number;
  content?: string;
  ended?: boolean;
  toolCallId?: string;
  title?: string;
  status?: ToolCallStatus;
  resultPreview?: string;
}

interface WorkState {
  startTime: number;
  sections: WorkSection[];
  renderTick: number;
}

// === Constants ===

const STATUS_ICONS: Record<string, string> = {
  in_progress: "⏳",
  completed: "✅",
  failed: "❌",
};

const TICK_DOTS = [".", "..", "..."];

/**
 * StreamController manages the lifecycle of a single agent response to Telegram.
 *
 * Pattern (from bridge-v2):
 * - Work indicator: a LIVE message showing timer + tools + thinking (edited in place)
 * - Draft: thinking content shown below the work indicator (debounced)
 * - Response: text accumulated silently, sent as a NEW message at finalize()
 */
export class StreamController {
  private deps: StreamControllerDeps;
  private editIntervalMs: number;
  private draftDebounceMs: number;

  // Work indicator state
  private workState: WorkState | null = null;
  private workMessageId: number | null = null;
  private workStarted = false;
  private workStartPromise: Promise<void> | null = null;

  // Throttle/debounce
  private lastEditTime = 0;
  private editTimer: ReturnType<typeof setInterval> | null = null;
  private draftTimer: ReturnType<typeof setTimeout> | null = null;
  private pendingDraftContent: string | null = null;

  // Response text buffer
  private textBuffer = "";

  // Outbound queue (serializes all Telegram API calls)
  private outboundQueue: Promise<void> = Promise.resolve();

  // Lifecycle
  private done = false;

  constructor(deps: StreamControllerDeps, opts?: StreamControllerOptions) {
    this.deps = deps;
    this.editIntervalMs = opts?.editIntervalMs ?? 2000;
    this.draftDebounceMs = opts?.draftDebounceMs ?? 1000;
  }

  // === Public API ===

  pushThinking(text: string): void {
    if (this.done) return;
    this.ensureWorkState();

    const thinking = this.lastThinking();
    if (thinking && !thinking.ended) {
      thinking.content = (thinking.content || "") + text;
    } else {
      this.workState!.sections.push({
        type: "thinking",
        startedAt: Date.now(),
        content: text,
        ended: false,
      });
    }

    this.scheduleDraft();
    this.ensureEditTimer();
  }

  toolStart(toolCallId: string, title: string, status?: ToolCallStatus): void {
    if (this.done) return;
    this.ensureWorkState();

    const thinking = this.lastThinking();
    if (thinking && !thinking.ended) {
      thinking.ended = true;
      thinking.endedAt = Date.now();
    }

    this.workState!.sections.push({
      type: "tool",
      startedAt: Date.now(),
      toolCallId,
      title,
      status: status ?? "in_progress",
    });

    this.ensureEditTimer();
  }

  toolUpdate(toolCallId: string, status: ToolCallStatus, content?: ToolCallContent[]): void {
    if (this.done) return;
    if (!this.workState) return;

    const tool = this.findTool(toolCallId);
    if (!tool) return;

    tool.status = status;

    if (status === "completed" || status === "failed") {
      tool.endedAt = Date.now();
    }

    if (content && content.length > 0) {
      const textItem = content.find(
        (c): c is ToolCallContent & { type: "content" } =>
          c.type === "content" && c.content.type === "text",
      );
      if (textItem && textItem.content.type === "text") {
        const fullText = textItem.content.text;
        tool.resultPreview = fullText.length > 100 ? fullText.slice(-100) : fullText;
      }
    }
  }

  pushText(text: string): void {
    if (this.done) return;
    this.textBuffer += text;
  }

  async finalize(): Promise<void> {
    if (this.done) return;
    this.done = true;

    await this.outboundQueue;
    await this.collapseWork();

    if (this.textBuffer) {
      try {
        const html = this.formatForSend(this.textBuffer);
        const segments = splitMessage(html);
        for (const segment of segments) {
          await this.deps.sendMessage(segment, { parse_mode: "HTML" });
        }
      } catch (err: unknown) {
        log.warn({ error: (err as Error).message ?? err }, "finalize send failed");
      }
    }

    this.cleanup();
  }

  async abort(error: Error): Promise<void> {
    if (this.done) return;
    this.done = true;

    await this.outboundQueue;
    await this.collapseWork();

    if (this.textBuffer) {
      try {
        const html = this.formatForSend(this.textBuffer);
        await this.deps.sendMessage(html, { parse_mode: "HTML" });
      } catch (err: unknown) {
        log.warn({ error: (err as Error).message ?? err }, "abort send failed");
      }
    }

    this.cleanup();
  }

  // --- Work indicator lifecycle ---

  private ensureWorkState(): void {
    if (!this.workState) {
      this.workState = { startTime: Date.now(), sections: [], renderTick: 0 };
      this.enqueue(() => this.startWorkMessage());
    }
  }

  private async startWorkMessage(): Promise<void> {
    if (this.workStarted) return;
    this.workStarted = true;
    try {
      const header = this.renderHeader();
      this.workMessageId = await this.deps.sendMessage(header, { parse_mode: "HTML" });
    } catch (err: unknown) {
      log.warn({ error: (err as Error).message ?? err }, "start work message failed");
    }
  }

  private renderHeader(): string {
    if (!this.workState) return "";
    const elapsed = Math.round((Date.now() - this.workState.startTime) / 1000);
    const thinkSecs = this.cumulativeTime("thinking");
    const toolSecs = this.cumulativeTime("tool");

    const parts = [`⏱ ${elapsed}s`];
    if (thinkSecs > 0 || this.workState.sections.some((s) => s.type === "thinking")) {
      parts.push(`🧠 ${thinkSecs}s`);
    }
    if (toolSecs > 0 || this.workState.sections.some((s) => s.type === "tool")) {
      parts.push(`🔧 ${toolSecs}s`);
    }
    return parts.join(" · ");
  }

  private renderContent(): string {
    if (!this.workState) return "";
    const lines: string[] = [];

    for (const s of this.workState.sections) {
      if (s.type === "tool") {
        const icon = STATUS_ICONS[s.status ?? "in_progress"] ?? TICK_DOTS[this.workState.renderTick % 3];
        let line = `🔨 ${s.title ?? "tool"} ${icon}`;
        if (s.resultPreview) {
          const tail = s.resultPreview.split("\n").filter((l) => l.trim()).slice(-2);
          for (const l of tail) {
            line += `\n  &gt; <code>${escapeHtml(l.slice(0, 80))}</code>`;
          }
        }
        lines.push(line);
      }
    }

    const thinking = this.lastThinking();
    if (thinking && !thinking.ended && thinking.content) {
      const thinkLines = thinking.content.split("\n").filter((l) => l.length > 0).slice(-4);
      lines.push(...thinkLines.map((l) => escapeHtml(l)));
    }

    return lines.join("\n");
  }

  private cumulativeTime(type: "thinking" | "tool"): number {
    if (!this.workState) return 0;
    let total = 0;
    for (const s of this.workState.sections) {
      if (s.type !== type) continue;
      if (s.endedAt && s.startedAt) total += s.endedAt - s.startedAt;
      else if (s.startedAt) total += Date.now() - s.startedAt;
    }
    return Math.round(total / 1000);
  }

  // --- Collapse ---

  private async collapseWork(): Promise<void> {
    if (!this.workState || !this.workStarted) return;

    this.clearEditTimer();
    this.clearDraftTimer();

    if (this.workMessageId === null) return;

    try {
      const collapsed = this.renderCollapsed();
      await this.deps.editMessage(this.workMessageId, collapsed, { parse_mode: "HTML" });
    } catch (err: unknown) {
      log.warn({ error: (err as Error).message ?? err }, "collapse edit failed");
    }
  }

  private renderCollapsed(): string {
    if (!this.workState) return "";
    const elapsed = Math.round((Date.now() - this.workState.startTime) / 1000);
    const thinkSecs = this.cumulativeTime("thinking");

    const parts: string[] = [];
    parts.push(`⏱ ${elapsed}s`);
    if (thinkSecs > 0) parts.push(`🧠 ${thinkSecs}s`);

    const toolLines: string[] = [];
    for (const s of this.workState.sections) {
      if (s.type === "tool") {
        const icon = s.status === "failed" ? "❌" : "✅";
        toolLines.push(`🔨 ${s.title ?? "tool"} ${icon}`);
      }
    }

    if (toolLines.length > 0) {
      return `${parts.join(" · ")}\n${toolLines.join("\n")}`;
    }
    return parts.join(" · ");
  }

  // --- Throttled edit + debounced draft ---

  private ensureEditTimer(): void {
    if (this.editTimer || this.done) return;
    this.editTimer = setInterval(() => {
      this.tickEdit();
    }, this.editIntervalMs);
    this.tickEdit();
  }

  private tickEdit(): void {
    if (this.done || !this.workState || this.workMessageId === null) return;
    this.workState.renderTick++;

    const header = this.renderHeader();
    const content = this.renderContent();
    const combined = content ? `${header}\n${content}` : header;

    this.enqueue(async () => {
      if (this.workMessageId === null || this.done) return;
      const now = Date.now();
      if (now - this.lastEditTime < this.editIntervalMs) return;
      this.lastEditTime = now;
      await this.deps.editMessage(this.workMessageId, combined, { parse_mode: "HTML" });
    });
  }

  private scheduleDraft(): void {
    if (this.done) return;
    const thinking = this.lastThinking();
    if (!thinking || !thinking.content) return;

    this.pendingDraftContent = `🧠 ${thinking.content}`;

    if (this.draftTimer) return;
    this.draftTimer = setTimeout(() => {
      this.draftTimer = null;
      if (this.pendingDraftContent !== null && this.workMessageId !== null) {
        const content = this.pendingDraftContent;
        this.pendingDraftContent = null;
        this.enqueue(async () => {
          if (this.workMessageId === null || this.done) return;
          await this.deps.sendDraft(this.workMessageId, content);
        });
      }
    }, this.draftDebounceMs);
  }

  private clearEditTimer(): void {
    if (this.editTimer) {
      clearInterval(this.editTimer);
      this.editTimer = null;
    }
  }

  private clearDraftTimer(): void {
    if (this.draftTimer) {
      clearTimeout(this.draftTimer);
      this.draftTimer = null;
      this.pendingDraftContent = null;
    }
  }

  // --- Helpers ---

  private lastThinking(): WorkSection | undefined {
    if (!this.workState) return undefined;
    for (let i = this.workState.sections.length - 1; i >= 0; i--) {
      if (this.workState.sections[i].type === "thinking") return this.workState.sections[i];
    }
    return undefined;
  }

  private findTool(toolCallId: string): WorkSection | undefined {
    if (!this.workState) return undefined;
    return this.workState.sections.find((s) => s.type === "tool" && s.toolCallId === toolCallId);
  }

  private enqueue(fn: () => Promise<void>): void {
    this.outboundQueue = this.outboundQueue.then(fn).catch((err: unknown) => {
      log.warn({ error: (err as Error).message ?? err }, "outbound queue error");
    });
  }

  private formatForSend(content: string): string {
    const html = formatMarkdown(content);
    return closeOpenTags(html);
  }

  private cleanup(): void {
    this.clearEditTimer();
    this.clearDraftTimer();
    this.workState = null;
  }
}

// --- Utility ---

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}
