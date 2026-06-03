import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { RateLimiter } from "./rate-limiter.js";

describe("RateLimiter", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("resolves immediately when no previous call for a chat", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");
  });

  it("delays when called again within minInterval", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");

    const promise = limiter.acquire("chat1");
    await vi.advanceTimersByTimeAsync(100);
    await promise;
  });

  it("does not delay when minInterval has elapsed", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");

    vi.advanceTimersByTime(200);

    await limiter.acquire("chat1");
  });

  it("tracks chats independently", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");

    await limiter.acquire("chat2");
  });

  it("delays each chat independently", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");
    await limiter.acquire("chat2");

    const p1 = limiter.acquire("chat1");
    const p2 = limiter.acquire("chat2");

    await vi.advanceTimersByTimeAsync(100);
    await Promise.all([p1, p2]);
  });

  it("evicts oldest entries when maxEntries exceeded", async () => {
    const limiter = new RateLimiter(100, 3);

    vi.advanceTimersByTime(100);
    await limiter.acquire("chat1");
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat2");
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat3");

    expect(limiter.size).toBe(3);

    // Adding a 4th should evict chat1 (oldest)
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat4");

    expect(limiter.size).toBe(3);

    // chat1 was evicted — should resolve immediately (no prior record)
    await limiter.acquire("chat1");
  });

  it("refreshes LRU position on re-access", async () => {
    const limiter = new RateLimiter(100, 3);

    vi.advanceTimersByTime(100);
    await limiter.acquire("chat1");
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat2");
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat3");

    // Re-access chat1 — moves it to most-recent position
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat1");

    // Adding chat4 should now evict chat2 (oldest after chat1 was refreshed)
    vi.advanceTimersByTime(100);
    await limiter.acquire("chat4");

    expect(limiter.size).toBe(3);

    // chat2 was evicted — resolves immediately
    await limiter.acquire("chat2");
  });
});
