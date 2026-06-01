import { createLogger } from "./logger.js";

export type ChatId = string;

export interface BufferedMessage {
  chatId: ChatId;
  text: string;
  receivedAt: number;
}

export interface StartupBufferOptions {
  /** Max age of a buffered message in ms. Older messages are discarded on flush. Default: 30000 (30s) */
  maxAgeMs?: number;
}

/**
 * Buffers incoming messages while the agent is starting up.
 * Once ready() is called, flushes all buffered messages to the handler.
 * After ready, messages pass through immediately (no buffering).
 */
export class StartupBuffer {
  private buffer: BufferedMessage[] = [];
  private isReady = false;
  private handler: ((chatId: ChatId, text: string) => void) | null = null;
  private maxAgeMs: number;
  private log = createLogger("startup-buffer");

  constructor(options?: StartupBufferOptions) {
    this.maxAgeMs = options?.maxAgeMs ?? 30_000;
  }

  /** Set the handler that receives messages (both buffered and live). */
  onMessage(handler: (chatId: ChatId, text: string) => void): void {
    this.handler = handler;
  }

  /** Called when a message arrives from the channel. */
  push(chatId: ChatId, text: string): void {
    if (this.isReady) {
      this.handler?.(chatId, text);
      return;
    }
    this.buffer.push({ chatId, text, receivedAt: Date.now() });
    this.log.debug({ chatId, buffered: this.buffer.length }, "message buffered");
  }

  /** Mark the buffer as ready — flush all valid messages. */
  ready(): void {
    if (this.isReady) return;
    this.isReady = true;
    const now = Date.now();
    const valid = this.buffer.filter(m => (now - m.receivedAt) <= this.maxAgeMs);
    const discarded = this.buffer.length - valid.length;
    if (discarded > 0) {
      this.log.warn({ discarded, maxAgeMs: this.maxAgeMs }, "discarded stale messages");
    }
    if (valid.length > 0) {
      this.log.info({ count: valid.length }, "flushing buffered messages");
    }
    this.buffer = [];
    for (const msg of valid) {
      this.handler?.(msg.chatId, msg.text);
    }
  }

  /** Get current buffer size (for testing/status). */
  get size(): number {
    return this.buffer.length;
  }

  /** Whether the buffer has been marked ready. */
  get buffering(): boolean {
    return !this.isReady;
  }
}
