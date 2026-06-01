import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { SessionManager } from "./session-manager.js";
import { SessionStore } from "./session-store.js";
import type * as acp from "@agentclientprotocol/sdk";

function makeTempDir(): string {
  return mkdtempSync(join(tmpdir(), "session-manager-test-"));
}

type MockConnection = {
  newSession: ReturnType<typeof vi.fn>;
  loadSession: ReturnType<typeof vi.fn>;
};

function makeConnection(overrides: Partial<MockConnection> = {}): acp.ClientSideConnection {
  const mock: MockConnection = {
    newSession: vi.fn().mockResolvedValue({ sessionId: "new-sess-1" }),
    loadSession: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
  return mock as unknown as acp.ClientSideConnection;
}

describe("SessionManager", () => {
  let dir: string;
  let store: SessionStore;
  let connection: acp.ClientSideConnection;
  let manager: SessionManager;

  beforeEach(() => {
    dir = makeTempDir();
    store = new SessionStore({ dir });
    connection = makeConnection();
    manager = new SessionManager({ getConnection: () => connection, cwd: "/tmp/test", store });
  });

  afterEach(() => {
    store.flushSync();
    rmSync(dir, { recursive: true, force: true });
  });

  const conn = () => connection as unknown as MockConnection;

  // --- getSession ---

  it("getSession creates a new session for an unknown chat", async () => {
    const sessionId = await manager.getSession("chat1");
    expect(sessionId).toBe("new-sess-1");
    expect(conn().newSession).toHaveBeenCalledWith({ cwd: "/tmp/test", mcpServers: [] });
  });

  it("getSession returns the cached session on subsequent calls", async () => {
    await manager.getSession("chat1");
    const sessionId = await manager.getSession("chat1");
    expect(sessionId).toBe("new-sess-1");
    expect(conn().newSession).toHaveBeenCalledTimes(1);
  });

  it("getSession resumes a persisted session via loadSession", async () => {
    store.setSessionId("chat1", "persisted-sess");
    const sessionId = await manager.getSession("chat1");
    expect(sessionId).toBe("persisted-sess");
    expect(conn().loadSession).toHaveBeenCalledWith({ sessionId: "persisted-sess" });
    expect(conn().newSession).not.toHaveBeenCalled();
  });

  it("getSession falls back to a new session when loadSession fails", async () => {
    store.setSessionId("chat1", "stale-sess");
    const failingConnection = makeConnection({
      loadSession: vi.fn().mockRejectedValue(new Error("session not found")),
    });
    const failManager = new SessionManager({ getConnection: () => failingConnection, cwd: "/tmp/test", store });
    const sessionId = await failManager.getSession("chat1");
    expect(sessionId).toBe("new-sess-1");
    expect((failingConnection as unknown as MockConnection).newSession).toHaveBeenCalled();
  });

  // --- createSession ---

  it("createSession calls newSession and stores the result", async () => {
    const sessionId = await manager.createSession("chat1");
    expect(sessionId).toBe("new-sess-1");
    expect(store.getSessionId("chat1")).toBe("new-sess-1");
    expect(store.getHistory("chat1")).toHaveLength(1);
  });

  it("createSession adds the session to history", async () => {
    await manager.createSession("chat1");
    const history = store.getHistory("chat1");
    expect(history[0].sessionId).toBe("new-sess-1");
  });

  // --- resetSession ---

  it("resetSession deletes the old session and creates a new one", async () => {
    await manager.createSession("chat1");
    conn().newSession.mockResolvedValueOnce({ sessionId: "new-sess-2" });
    const newSessionId = await manager.resetSession("chat1");
    expect(newSessionId).toBe("new-sess-2");
    expect(store.getSessionId("chat1")).toBe("new-sess-2");
  });

  it("resetSession removes the old session from the in-memory cache", async () => {
    await manager.createSession("chat1");
    conn().newSession.mockResolvedValueOnce({ sessionId: "new-sess-2" });
    await manager.resetSession("chat1");
    // The new session should be cached, not the old one
    expect(manager.getSessionId("chat1")).toBe("new-sess-2");
  });

  // --- resumeSession ---

  it("resumeSession calls loadSession and updates the in-memory cache", async () => {
    await manager.resumeSession("chat1", "existing-sess");
    expect(conn().loadSession).toHaveBeenCalledWith({ sessionId: "existing-sess" });
    expect(manager.getSessionId("chat1")).toBe("existing-sess");
    expect(store.getSessionId("chat1")).toBe("existing-sess");
  });

  it("resumeSession touches the session in history", async () => {
    store.addToHistory("chat1", "existing-sess");
    const before = store.getHistory("chat1")[0].touchedAt;
    await new Promise(r => setTimeout(r, 5));
    await manager.resumeSession("chat1", "existing-sess");
    const after = store.getHistory("chat1")[0].touchedAt;
    expect(after > before).toBe(true);
  });

  it("resumeSession works when agent does not support loadSession", async () => {
    const connWithoutLoad = { newSession: vi.fn().mockResolvedValue({ sessionId: "new-sess-1" }) } as unknown as acp.ClientSideConnection;
    const mgr = new SessionManager({ getConnection: () => connWithoutLoad, cwd: "/tmp/test", store });
    await expect(mgr.resumeSession("chat1", "existing-sess")).resolves.toBeUndefined();
    expect(mgr.getSessionId("chat1")).toBe("existing-sess");
  });

  // --- hasSession / getSessionId ---

  it("hasSession returns false before any session is created", () => {
    expect(manager.hasSession("chat1")).toBe(false);
  });

  it("hasSession returns true after a session is created", async () => {
    await manager.createSession("chat1");
    expect(manager.hasSession("chat1")).toBe(true);
  });

  it("getSessionId returns undefined before a session is created", () => {
    expect(manager.getSessionId("chat1")).toBeUndefined();
  });

  it("getSessionId returns the session id after creation", async () => {
    await manager.createSession("chat1");
    expect(manager.getSessionId("chat1")).toBe("new-sess-1");
  });
});
