import { describe, it, expect } from "vitest";
import perfPlugin from "./perf-tracker.js";
import type { ExtensionContext } from "../extension.js";

const mockCtx: ExtensionContext = {
  sendMessage: () => {},
  config: {},
};

describe("perf-tracker", () => {
  it("/perf with no data returns 'No performance data yet.'", async () => {
    const result = await perfPlugin.commands!.perf.handler(mockCtx, "chat-no-data-perf", "");
    expect(result).toBe("No performance data yet.");
  });

  it("tracks turn duration and shows it in /perf output", async () => {
    const chatId = "chat-perf-tracking";
    perfPlugin.onTurnStart!(mockCtx, chatId);
    await new Promise((resolve) => setTimeout(resolve, 10));
    perfPlugin.onTurnEnd!(mockCtx, chatId);

    const result = await perfPlugin.commands!.perf.handler(mockCtx, chatId, "");
    expect(result).toContain("Performance");
    expect(result).toMatch(/\d+(ms|s)/);
  });

  it("/perf shows average when multiple turns are tracked", async () => {
    const chatId = "chat-perf-avg";

    perfPlugin.onTurnStart!(mockCtx, chatId);
    await new Promise((resolve) => setTimeout(resolve, 5));
    perfPlugin.onTurnEnd!(mockCtx, chatId);

    perfPlugin.onTurnStart!(mockCtx, chatId);
    await new Promise((resolve) => setTimeout(resolve, 5));
    perfPlugin.onTurnEnd!(mockCtx, chatId);

    const result = await perfPlugin.commands!.perf.handler(mockCtx, chatId, "");
    expect(result).toContain("Avg:");
  });

  it("onTurnEnd without onTurnStart does not throw", () => {
    expect(() => perfPlugin.onTurnEnd!(mockCtx, "chat-no-start")).not.toThrow();
  });
});
