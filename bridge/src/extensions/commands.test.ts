import { describe, it, expect, vi } from "vitest";
import commandsExtension from "./commands.js";
import type { ExtensionContext, AgentControl, SessionControl } from "../extension.js";
import type { SessionHistoryEntry } from "../session-store.js";

function makeSessionControl(overrides: Partial<SessionControl> = {}): SessionControl {
  return {
    getHistory: vi.fn().mockReturnValue([]),
    getActiveSessionId: vi.fn().mockReturnValue(undefined),
    resumeSession: vi.fn().mockResolvedValue(undefined),
    resetSession: vi.fn().mockResolvedValue("new-sess"),
    labelSession: vi.fn(),
    findByPrefix: vi.fn().mockReturnValue(null),
    ...overrides,
  };
}

function makeCtx(
  agentOverrides: Partial<AgentControl> = {},
  sessionOverrides: Partial<SessionControl> = {},
): ExtensionContext {
  return {
    sendMessage: vi.fn(),
    config: {},
    agent: {
      isReady: vi.fn().mockReturnValue(true),
      reset: vi.fn().mockResolvedValue(undefined),
      abort: vi.fn(),
      ...agentOverrides,
    },
    sessions: makeSessionControl(sessionOverrides),
  };
}

function makeEntry(sessionId: string, overrides: Partial<SessionHistoryEntry> = {}): SessionHistoryEntry {
  const now = new Date().toISOString();
  return { sessionId, createdAt: now, touchedAt: now, ...overrides };
}

// ---------------------------------------------------------------------------
// /new
// ---------------------------------------------------------------------------

describe("/new", () => {
  it("calls sessions.resetSession() and returns success message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.new.handler(ctx, "chat1", "");
    expect(ctx.sessions.resetSession).toHaveBeenCalledWith("chat1");
    expect(result).toBe("✨ New session started.");
  });

  it("returns error message when resetSession fails", async () => {
    const ctx = makeCtx({}, { resetSession: vi.fn().mockRejectedValue(new Error("boom")) });
    const result = await commandsExtension.commands!.new.handler(ctx, "chat1", "");
    expect(result).toBe("❌ Failed to reset session.");
  });
});

// ---------------------------------------------------------------------------
// /stop
// ---------------------------------------------------------------------------

describe("/stop", () => {
  it("calls agent.abort() and returns message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.stop.handler(ctx, "chat1", "");
    expect(ctx.agent.abort).toHaveBeenCalled();
    expect(result).toBe("⏹ Stopped.");
  });
});

// ---------------------------------------------------------------------------
// /resume
// ---------------------------------------------------------------------------

describe("/resume", () => {
  it("with no args and empty history returns empty history message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "");
    expect(result).toContain("No session history yet");
  });

  it("with no args and history returns session list", async () => {
    const entries = [makeEntry("aaaa1111-0000-0000-0000-000000000000")];
    const ctx = makeCtx({}, { getHistory: vi.fn().mockReturnValue(entries) });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "");
    expect(result).toContain("Sessions");
    expect(result).toContain("aaaa1111");
  });

  it("with numeric index resumes that session", async () => {
    const entries = [
      makeEntry("aaaa1111-0000-0000-0000-000000000000"),
      makeEntry("bbbb2222-0000-0000-0000-000000000000"),
    ];
    const ctx = makeCtx({}, { getHistory: vi.fn().mockReturnValue(entries) });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "1");
    expect(ctx.sessions.resumeSession).toHaveBeenCalledWith("chat1", "aaaa1111-0000-0000-0000-000000000000");
    expect(result).toContain("aaaa1111");
  });

  it("with numeric index out of range falls through to prefix search", async () => {
    const entries = [makeEntry("aaaa1111-0000-0000-0000-000000000000")];
    const ctx = makeCtx({}, { getHistory: vi.fn().mockReturnValue(entries) });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "99");
    expect(result).toContain("No session found");
  });

  it("with matching prefix resumes that session", async () => {
    const entry = makeEntry("aaaa1111-0000-0000-0000-000000000000");
    const ctx = makeCtx({}, { findByPrefix: vi.fn().mockReturnValue(entry) });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "aaaa");
    expect(ctx.sessions.resumeSession).toHaveBeenCalledWith("chat1", "aaaa1111-0000-0000-0000-000000000000");
    expect(result).toContain("aaaa1111");
  });

  it("with ambiguous prefix returns ambiguous message", async () => {
    const entries = [
      makeEntry("aaaa1111-0000-0000-0000-000000000000"),
      makeEntry("aaaa2222-0000-0000-0000-000000000000"),
    ];
    const ctx = makeCtx({}, {
      getHistory: vi.fn().mockReturnValue(entries),
      findByPrefix: vi.fn().mockReturnValue(null),
    });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "aaaa");
    expect(result).toContain("Ambiguous prefix");
  });

  it("with no matching prefix returns not found message", async () => {
    const ctx = makeCtx({}, {
      getHistory: vi.fn().mockReturnValue([]),
      findByPrefix: vi.fn().mockReturnValue(null),
    });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "zzz");
    expect(result).toContain("No session found");
  });

  it("returns error message when resumeSession fails", async () => {
    const entries = [makeEntry("aaaa1111-0000-0000-0000-000000000000")];
    const ctx = makeCtx({}, {
      getHistory: vi.fn().mockReturnValue(entries),
      resumeSession: vi.fn().mockRejectedValue(new Error("boom")),
    });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "1");
    expect(result).toBe("❌ Failed to resume session.");
  });

  it("--page flag shows paginated list", async () => {
    const entries = [makeEntry("aaaa1111-0000-0000-0000-000000000000")];
    const ctx = makeCtx({}, { getHistory: vi.fn().mockReturnValue(entries) });
    const result = await commandsExtension.commands!.resume.handler(ctx, "chat1", "--page 1");
    expect(result).toContain("Sessions");
  });
});

// ---------------------------------------------------------------------------
// /label
// ---------------------------------------------------------------------------

describe("/label", () => {
  it("with no args returns usage message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.label.handler(ctx, "chat1", "");
    expect(result).toBe("Usage: /label <name>");
  });

  it("with no active session returns error", async () => {
    const ctx = makeCtx({}, { getActiveSessionId: vi.fn().mockReturnValue(undefined) });
    const result = await commandsExtension.commands!.label.handler(ctx, "chat1", "my label");
    expect(result).toBe("No active session to label.");
  });

  it("with active session labels it and returns confirmation", async () => {
    const ctx = makeCtx({}, { getActiveSessionId: vi.fn().mockReturnValue("sess-abc") });
    const result = await commandsExtension.commands!.label.handler(ctx, "chat1", "my label");
    expect(ctx.sessions.labelSession).toHaveBeenCalledWith("chat1", "sess-abc", "my label");
    expect(result).toContain("my label");
  });
});

// ---------------------------------------------------------------------------
// /version
// ---------------------------------------------------------------------------

describe("/version", () => {
  it("returns version string (unknown when file missing)", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.version.handler(ctx, "chat1", "");
    expect(result).toContain("bridge");
    expect(result).toMatch(/version:/);
  });
});

// ---------------------------------------------------------------------------
// /sh
// ---------------------------------------------------------------------------

describe("/sh", () => {
  it("with no args returns usage message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.sh.handler(ctx, "chat1", "");
    expect(result).toBe("Usage: /sh <command>");
  });

  it("executes a command and returns output", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.sh.handler(ctx, "chat1", "echo hello");
    expect(result).toBe("hello");
  });

  it("returns exit code and output when command fails", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.sh.handler(ctx, "chat1", "exit 1");
    expect(result).toMatch(/Exit \d/);
  });
});

// ---------------------------------------------------------------------------
// /diagnose
// ---------------------------------------------------------------------------

describe("/diagnose", () => {
  it("returns diagnostic info including chat ID and agent status", async () => {
    const ctx = makeCtx(
      { isReady: vi.fn().mockReturnValue(true) },
      { getActiveSessionId: vi.fn().mockReturnValue("sess-abc123"), getHistory: vi.fn().mockReturnValue([]) },
    );
    const result = await commandsExtension.commands!.diagnose.handler(ctx, "chat1", "");
    expect(result).toContain("chat1");
    expect(result).toContain("true");
    expect(result).toContain("sess-abc");
  });

  it("shows 'none' when no active session", async () => {
    const ctx = makeCtx(
      {},
      { getActiveSessionId: vi.fn().mockReturnValue(undefined), getHistory: vi.fn().mockReturnValue([]) },
    );
    const result = await commandsExtension.commands!.diagnose.handler(ctx, "chat1", "");
    expect(result).toContain("none");
  });
});
