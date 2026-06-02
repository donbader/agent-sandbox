/**
 * Generic startup buffer — queues messages while the agent is starting up.
 * Any channel can use this to avoid dropping messages during initialization.
 */

export interface BufferedItem<T = unknown> {
  data: T;
  timestamp: number;
}

const STALE_THRESHOLD_MS = 30_000;

export class StartupBuffer<T = unknown> {
  private buffer: BufferedItem<T>[] = [];
  private isReady = false;

  /** Queue an item if not ready, or return false if ready (caller should process directly). */
  push(data: T): boolean {
    if (this.isReady) return false;
    this.buffer.push({ data, timestamp: Date.now() });
    return true;
  }

  /** Mark as ready and return non-stale buffered items. Clears the buffer. */
  flush(): T[] {
    this.isReady = true;
    const staleThreshold = Date.now() - STALE_THRESHOLD_MS;
    const items = this.buffer
      .filter((item) => item.timestamp >= staleThreshold)
      .map((item) => item.data);
    this.buffer = [];
    return items;
  }

  /** Whether the buffer is in ready state (pass-through mode). */
  get ready(): boolean {
    return this.isReady;
  }
}
