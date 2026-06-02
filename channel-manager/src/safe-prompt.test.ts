import { describe, it, expect, vi } from "vitest";
import { safePrompt } from "./safe-prompt.js";
import type { AcpAgent } from "./acp-client.js";

function mockAgent(response?: string, error?: Error): AcpAgent {
  return {
    prompt: vi.fn().mockImplementation(async () => {
      if (error) throw error;
      return response ?? "agent response";
    }),
  } as unknown as AcpAgent;
}

describe("safePrompt", () => {
  it("returns ok with response on success", async () => {
    const agent = mockAgent("hello world");
    const result = await safePrompt(agent, "sess-1", "hi");
    expect(result).toEqual({ ok: true, response: "hello world" });
  });

  it("returns error message on failure", async () => {
    const agent = mockAgent(undefined, new Error("connection lost"));
    const result = await safePrompt(agent, "sess-1", "hi");
    expect(result).toEqual({ ok: false, error: "⚠️ Agent unavailable. Try again shortly." });
  });

  it("passes sessionId and text to agent.prompt", async () => {
    const agent = mockAgent("ok");
    await safePrompt(agent, "my-session", "fix the bug");
    expect(agent.prompt).toHaveBeenCalledWith("my-session", "fix the bug");
  });
});
