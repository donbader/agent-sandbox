import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync, existsSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { SessionStore } from "./session-store.js";

function makeTempDir(): string {
  return mkdtempSync(join(tmpdir(), "session-store-test-"));
}

describe("SessionStore", () => {
  let dir: string;
  let store: SessionStore;

  beforeEach(() => {
    dir = makeTempDir();
    store = new SessionStore({ dir });
  });

  afterEach(() => {
    store.flushSync();
    rmSync(dir, { recursive: true, force: true });
  });

  // --- getSessionId / setSessionId / deleteSessionId ---

  it("getSessionId returns undefined for unknown chatId", () => {
    expect(store.getSessionId("chat1")).toBeUndefined();
  });

  it("setSessionId stores and getSessionId retrieves", () => {
    store.setSessionId("chat1", "sess-abc");
    expect(store.getSessionId("chat1")).toBe("sess-abc");
  });

  it("deleteSessionId removes the mapping", () => {
    store.setSessionId("chat1", "sess-abc");
    store.deleteSessionId("chat1");
    expect(store.getSessionId("chat1")).toBeUndefined();
  });

  // --- getAllActive ---

  it("getAllActive returns all active mappings", () => {
    store.setSessionId("chat1", "sess-1");
    store.setSessionId("chat2", "sess-2");
    expect(store.getAllActive()).toEqual({ chat1: "sess-1", chat2: "sess-2" });
  });

  it("getAllActive returns a copy — mutations do not affect the store", () => {
    store.setSessionId("chat1", "sess-1");
    const active = store.getAllActive();
    active["chat2"] = "sess-injected";
    expect(store.getSessionId("chat2")).toBeUndefined();
  });

  // --- addToHistory / getHistory ---

  it("getHistory returns empty array for unknown chatId", () => {
    expect(store.getHistory("chat1")).toEqual([]);
  });

  it("addToHistory adds an entry with sessionId, createdAt, and touchedAt", () => {
    const before = new Date().toISOString();
    store.addToHistory("chat1", "sess-1");
    const after = new Date().toISOString();
    const history = store.getHistory("chat1");
    expect(history).toHaveLength(1);
    expect(history[0].sessionId).toBe("sess-1");
    expect(history[0].createdAt >= before).toBe(true);
    expect(history[0].createdAt <= after).toBe(true);
    expect(history[0].touchedAt).toBe(history[0].createdAt);
  });

  it("addToHistory does not add duplicate sessionIds", () => {
    store.addToHistory("chat1", "sess-1");
    store.addToHistory("chat1", "sess-1");
    expect(store.getHistory("chat1")).toHaveLength(1);
  });

  it("addToHistory stores optional label", () => {
    store.addToHistory("chat1", "sess-1", "My Label");
    expect(store.getHistory("chat1")[0].label).toBe("My Label");
  });

  // --- touchSession ---

  it("touchSession updates touchedAt to a later timestamp", async () => {
    store.addToHistory("chat1", "sess-1");
    const originalTouchedAt = store.getHistory("chat1")[0].touchedAt;
    await new Promise((r) => setTimeout(r, 5));
    store.touchSession("chat1", "sess-1");
    const updatedTouchedAt = store.getHistory("chat1")[0].touchedAt;
    expect(updatedTouchedAt > originalTouchedAt).toBe(true);
  });

  it("touchSession does nothing for an unknown session", () => {
    expect(() => store.touchSession("chat1", "unknown-sess")).not.toThrow();
  });

  // --- setLabel ---

  it("setLabel sets the label on a history entry", () => {
    store.addToHistory("chat1", "sess-1");
    store.setLabel("chat1", "sess-1", "Renamed");
    expect(store.getHistory("chat1")[0].label).toBe("Renamed");
  });

  it("setLabel does nothing for an unknown session", () => {
    expect(() => store.setLabel("chat1", "unknown-sess", "label")).not.toThrow();
  });

  // --- maxHistory eviction ---

  // --- findByPrefix ---

  it("findByPrefix returns the matching entry when exactly one matches", () => {
    store.addToHistory("chat1", "abcdef12-1234-5678-abcd-ef1234567890");
    store.addToHistory("chat1", "12345678-1234-5678-abcd-ef1234567890");
    const entry = store.findByPrefix("chat1", "abcdef");
    expect(entry).not.toBeNull();
    expect(entry!.sessionId).toBe("abcdef12-1234-5678-abcd-ef1234567890");
  });

  it("findByPrefix returns null when no entries match", () => {
    store.addToHistory("chat1", "abcdef12-1234-5678-abcd-ef1234567890");
    expect(store.findByPrefix("chat1", "zzz")).toBeNull();
  });

  it("findByPrefix returns null when multiple entries match (ambiguous)", () => {
    store.addToHistory("chat1", "abcdef12-1234-5678-abcd-ef1234567890");
    store.addToHistory("chat1", "abcdef99-1234-5678-abcd-ef1234567890");
    expect(store.findByPrefix("chat1", "abcdef")).toBeNull();
  });

  it("findByPrefix returns null for unknown chatId", () => {
    expect(store.findByPrefix("unknown-chat", "abc")).toBeNull();
  });

  // --- maxHistory eviction ---

  it("evicts oldest entries when maxHistory is exceeded", () => {
    const smallStore = new SessionStore({ dir, maxHistory: 3 });
    smallStore.addToHistory("chat1", "sess-1");
    smallStore.addToHistory("chat1", "sess-2");
    smallStore.addToHistory("chat1", "sess-3");
    smallStore.addToHistory("chat1", "sess-4");
    smallStore.flushSync();
    const history = smallStore.getHistory("chat1");
    expect(history).toHaveLength(3);
    expect(history.map((e) => e.sessionId)).not.toContain("sess-1");
    expect(history.map((e) => e.sessionId)).toContain("sess-4");
  });

  // --- persistence ---

  it("persists session map to disk and reads back on a new instance", () => {
    store.setSessionId("chat1", "sess-abc");
    const store2 = new SessionStore({ dir });
    expect(store2.getSessionId("chat1")).toBe("sess-abc");
    store2.flushSync();
  });

  it("persists history to disk after flushSync and reads back on a new instance", () => {
    store.addToHistory("chat1", "sess-1", "label1");
    store.flushSync();
    const store2 = new SessionStore({ dir });
    const history = store2.getHistory("chat1");
    expect(history).toHaveLength(1);
    expect(history[0].sessionId).toBe("sess-1");
    expect(history[0].label).toBe("label1");
    store2.flushSync();
  });

  // --- atomic write ---

  it("leaves no .tmp file after writing the session map", () => {
    store.setSessionId("chat1", "sess-abc");
    expect(existsSync(join(dir, "session-map.json.tmp"))).toBe(false);
    expect(existsSync(join(dir, "session-map.json"))).toBe(true);
  });

  // --- flushSync ---

  it("flushSync forces immediate write of pending history", () => {
    store.addToHistory("chat1", "sess-1");
    store.flushSync();
    const historyPath = join(dir, "session-history.json");
    expect(existsSync(historyPath)).toBe(true);
    const data = JSON.parse(readFileSync(historyPath, "utf-8")) as Record<string, unknown[]>;
    expect(data["chat1"]).toHaveLength(1);
  });

  it("flushSync is idempotent when there are no pending writes", () => {
    store.addToHistory("chat1", "sess-1");
    store.flushSync();
    expect(() => store.flushSync()).not.toThrow();
  });
});
