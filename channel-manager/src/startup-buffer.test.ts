import { describe, it, expect, vi, beforeEach } from "vitest";
import { StartupBuffer } from "./startup-buffer.js";

describe("StartupBuffer", () => {
  it("buffers items when not ready", () => {
    const buf = new StartupBuffer<string>();
    expect(buf.push("msg1")).toBe(true);
    expect(buf.push("msg2")).toBe(true);
    expect(buf.ready).toBe(false);
  });

  it("returns false on push when ready (pass-through mode)", () => {
    const buf = new StartupBuffer<string>();
    buf.flush(); // mark ready
    expect(buf.push("msg")).toBe(false);
    expect(buf.ready).toBe(true);
  });

  it("flush returns buffered items and marks ready", () => {
    const buf = new StartupBuffer<string>();
    buf.push("a");
    buf.push("b");
    const items = buf.flush();
    expect(items).toEqual(["a", "b"]);
    expect(buf.ready).toBe(true);
  });

  it("flush discards stale items (>30s old)", () => {
    const buf = new StartupBuffer<string>();
    const now = Date.now();

    // Mock Date.now for push
    vi.spyOn(Date, "now").mockReturnValue(now - 31_000);
    buf.push("stale");

    vi.spyOn(Date, "now").mockReturnValue(now);
    buf.push("fresh");

    const items = buf.flush();
    expect(items).toEqual(["fresh"]);

    vi.restoreAllMocks();
  });

  it("flush clears the buffer", () => {
    const buf = new StartupBuffer<string>();
    buf.push("x");
    buf.flush();
    // Second flush should return empty
    const items = buf.flush();
    expect(items).toEqual([]);
  });
});
