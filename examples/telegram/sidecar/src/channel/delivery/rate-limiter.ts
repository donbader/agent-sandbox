/**
 * Per-chat rate limiter for Telegram API calls.
 * Enforces a minimum interval between calls to the same chat to avoid hitting
 * Telegram's per-chat edit rate limit (~1 edit/second).
 *
 * Uses LRU eviction to prevent unbounded memory growth from inactive chats.
 */

const DEFAULT_MAX_ENTRIES = 1000;

export class RateLimiter {
  private lastCall = new Map<string, number>();
  private readonly minInterval: number;
  private readonly maxEntries: number;

  constructor(minIntervalMs = 100, maxEntries = DEFAULT_MAX_ENTRIES) {
    this.minInterval = minIntervalMs;
    this.maxEntries = maxEntries;
  }

  async acquire(chatId: string): Promise<void> {
    const now = Date.now();
    const last = this.lastCall.get(chatId) ?? 0;
    const elapsed = now - last;
    if (elapsed < this.minInterval) {
      await new Promise<void>((resolve) =>
        setTimeout(resolve, this.minInterval - elapsed),
      );
    }
    // Delete and re-insert to maintain LRU order (Map preserves insertion order)
    this.lastCall.delete(chatId);
    this.lastCall.set(chatId, Date.now());
    this.evict();
  }

  /** Remove oldest entries when map exceeds maxEntries. */
  private evict(): void {
    while (this.lastCall.size > this.maxEntries) {
      const oldest = this.lastCall.keys().next().value;
      if (oldest !== undefined) {
        this.lastCall.delete(oldest);
      }
    }
  }

  /** Number of tracked chats (for testing). */
  get size(): number {
    return this.lastCall.size;
  }
}
