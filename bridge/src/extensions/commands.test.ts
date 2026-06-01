import { describe, it, expect, vi } from "vitest";
import commandsExtension from "./commands.js";
import type { ExtensionContext, AgentControl } from "../extension.js";

function makeCtx(agentOverrides: Partial<AgentControl> = {}): ExtensionContext {
  return {
    sendMessage: vi.fn(),
    config: {},
    agent: {
      isReady: vi.fn().mockReturnValue(true),
      reset: vi.fn().mockResolvedValue(undefined),
      abort: vi.fn(),
      ...agentOverrides,
    },
  };
}

describe("commands extension", () => {
  it("/new calls agent.reset() and returns success message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.new.handler(ctx, "chat1", "");
    expect(ctx.agent.reset).toHaveBeenCalled();
    expect(result).toBe("✨ New session started.");
  });

  it("/new returns error message when reset fails", async () => {
    const ctx = makeCtx({ reset: vi.fn().mockRejectedValue(new Error("boom")) });
    const result = await commandsExtension.commands!.new.handler(ctx, "chat1", "");
    expect(result).toBe("❌ Failed to reset session.");
  });

  it("/stop calls agent.abort() and returns message", async () => {
    const ctx = makeCtx();
    const result = await commandsExtension.commands!.stop.handler(ctx, "chat1", "");
    expect(ctx.agent.abort).toHaveBeenCalled();
    expect(result).toBe("⏹ Stopped.");
  });

  it("/status returns connected message when agent is ready", async () => {
    const ctx = makeCtx({ isReady: vi.fn().mockReturnValue(true) });
    const result = await commandsExtension.commands!.status.handler(ctx, "chat1", "");
    expect(result).toBe("✅ Agent: connected");
  });

  it("/status returns starting message when agent is not ready", async () => {
    const ctx = makeCtx({ isReady: vi.fn().mockReturnValue(false) });
    const result = await commandsExtension.commands!.status.handler(ctx, "chat1", "");
    expect(result).toBe("⏳ Agent: starting...");
  });
});
