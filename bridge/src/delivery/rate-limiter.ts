/**
 * Per-chat rate limiter for Telegram API calls.
 * Enforces a minimum interval between calls to the same chat to avoid hitting
 * Telegram's per-chat edit rate limit (~1 edit/second).
 */
export class RateLimiter {
  private lastCall = new Map<string, number>();
  private readonly minInterval: number;

  constructor(minIntervalMs = 100) {
    this.minInterval = minIntervalMs;
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
    this.lastCall.set(chatId, Date.now());
  }
}
