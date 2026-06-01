import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { withRetry } from "./api-retry.js";

describe("withRetry", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns result on first successful call", async () => {
    const fn = vi.fn().mockResolvedValue("ok");
    const result = await withRetry(fn);
    expect(result).toBe("ok");
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("retries on 429 and succeeds after waiting Retry-After", async () => {
    let calls = 0;
    const fn = vi.fn().mockImplementation(async () => {
      calls++;
      if (calls === 1) {
        const err: any = new Error("Too Many Requests");
        err.response = { error_code: 429, parameters: { retry_after: 2 } };
        throw err;
      }
      return "ok";
    });

    const promise = withRetry(fn, { maxRetries: 3, baseDelay: 100 });
    await vi.advanceTimersByTimeAsync(2000);
    const result = await promise;

    expect(result).toBe("ok");
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("throws 429 error after exhausting retries", async () => {
    const err: any = new Error("Too Many Requests");
    err.response = { error_code: 429, parameters: { retry_after: 1 } };
    const fn = vi.fn().mockImplementation(async () => { throw err; });

    const promise = withRetry(fn, { maxRetries: 2, baseDelay: 100 });
    const assertion = expect(promise).rejects.toThrow("Too Many Requests");
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
    expect(fn).toHaveBeenCalledTimes(3); // initial + 2 retries
  });

  it("does not retry on 403 and returns undefined", async () => {
    const err: any = new Error("Forbidden");
    err.response = { error_code: 403 };
    const fn = vi.fn().mockRejectedValue(err);

    const result = await withRetry(fn);
    expect(result).toBeUndefined();
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("retries on ECONNRESET with exponential backoff", async () => {
    let calls = 0;
    const fn = vi.fn().mockImplementation(async () => {
      calls++;
      if (calls < 3) {
        const err: any = new Error("ECONNRESET");
        err.code = "ECONNRESET";
        throw err;
      }
      return "ok";
    });

    const promise = withRetry(fn, { maxRetries: 3, baseDelay: 100 });
    // First retry: 100ms, second retry: 200ms
    await vi.advanceTimersByTimeAsync(300);
    const result = await promise;

    expect(result).toBe("ok");
    expect(fn).toHaveBeenCalledTimes(3);
  });

  it("retries on socket hang up message", async () => {
    let calls = 0;
    const fn = vi.fn().mockImplementation(async () => {
      calls++;
      if (calls === 1) {
        throw new Error("socket hang up");
      }
      return "ok";
    });

    const promise = withRetry(fn, { maxRetries: 2, baseDelay: 100 });
    await vi.advanceTimersByTimeAsync(100);
    const result = await promise;

    expect(result).toBe("ok");
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("throws after exhausting retries on transient errors", async () => {
    const err: any = new Error("ETIMEDOUT");
    err.code = "ETIMEDOUT";
    const fn = vi.fn().mockImplementation(async () => { throw err; });

    const promise = withRetry(fn, { maxRetries: 2, baseDelay: 100 });
    const assertion = expect(promise).rejects.toThrow("ETIMEDOUT");
    // 100ms + 200ms backoff
    await vi.advanceTimersByTimeAsync(300);
    await assertion;
    expect(fn).toHaveBeenCalledTimes(3);
  });

  it("does not retry on non-transient, non-rate-limit errors", async () => {
    const fn = vi.fn().mockRejectedValue(new Error("some unexpected error"));

    await expect(withRetry(fn)).rejects.toThrow("some unexpected error");
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
