import { describe, it, expect, vi } from "vitest";
import { StartupBuffer } from "./startup-buffer.js";

describe("StartupBuffer", () => {
  it("buffers messages before ready", () => {
    const buf = new StartupBuffer();
    const received: [string, string][] = [];
    buf.onMessage((chatId, text) => received.push([chatId, text]));

    buf.push("chat1", "hello");
    buf.push("chat2", "world");

    expect(received).toHaveLength(0);
    expect(buf.size).toBe(2);
  });

  it("flushes buffered messages in order on ready", () => {
    const buf = new StartupBuffer();
    const received: [string, string][] = [];
    buf.onMessage((chatId, text) => received.push([chatId, text]));

    buf.push("chat1", "first");
    buf.push("chat1", "second");
    buf.push("chat2", "third");
    buf.ready();

    expect(received).toEqual([
      ["chat1", "first"],
      ["chat1", "second"],
      ["chat2", "third"],
    ]);
    expect(buf.size).toBe(0);
  });

  it("passes messages through immediately after ready", () => {
    const buf = new StartupBuffer();
    const received: [string, string][] = [];
    buf.onMessage((chatId, text) => received.push([chatId, text]));

    buf.ready();
    buf.push("chat1", "live message");

    expect(received).toEqual([["chat1", "live message"]]);
  });

  it("discards stale messages on flush", () => {
    vi.useFakeTimers();
    const buf = new StartupBuffer({ maxAgeMs: 1000 });
    const received: [string, string][] = [];
    buf.onMessage((chatId, text) => received.push([chatId, text]));

    buf.push("chat1", "stale");
    vi.advanceTimersByTime(2000);
    buf.push("chat1", "fresh");
    buf.ready();

    expect(received).toEqual([["chat1", "fresh"]]);
    vi.useRealTimers();
  });

  it("ready() is idempotent", () => {
    const buf = new StartupBuffer();
    const received: [string, string][] = [];
    buf.onMessage((chatId, text) => received.push([chatId, text]));

    buf.push("chat1", "msg");
    buf.ready();
    buf.ready();

    expect(received).toHaveLength(1);
  });

  it("size returns current buffer length", () => {
    const buf = new StartupBuffer();
    expect(buf.size).toBe(0);
    buf.push("chat1", "a");
    expect(buf.size).toBe(1);
    buf.push("chat1", "b");
    expect(buf.size).toBe(2);
  });

  it("buffering is true before ready and false after", () => {
    const buf = new StartupBuffer();
    expect(buf.buffering).toBe(true);
    buf.ready();
    expect(buf.buffering).toBe(false);
  });
});
