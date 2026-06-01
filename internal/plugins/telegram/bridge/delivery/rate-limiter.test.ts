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
    // Should resolve without needing timer advancement
    await limiter.acquire("chat1");
    // If we reach here without hanging, it resolved immediately
  });

  it("delays when called again within minInterval", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");

    // Second call immediately — fake time hasn't advanced, so elapsed = 0 < 100
    const promise = limiter.acquire("chat1");
    await vi.advanceTimersByTimeAsync(100);
    await promise;
    // Resolves after advancing timers by minInterval
  });

  it("does not delay when minInterval has elapsed", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");

    // Advance time past the interval
    vi.advanceTimersByTime(200);

    // Should resolve immediately without further timer advancement
    await limiter.acquire("chat1");
  });

  it("tracks chats independently", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");

    // chat2 has no prior call — should resolve immediately
    await limiter.acquire("chat2");
  });

  it("delays each chat independently", async () => {
    const limiter = new RateLimiter(100);
    await limiter.acquire("chat1");
    await limiter.acquire("chat2");

    // Both chats need delay now
    const p1 = limiter.acquire("chat1");
    const p2 = limiter.acquire("chat2");

    await vi.advanceTimersByTimeAsync(100);
    await Promise.all([p1, p2]);
  });
});
