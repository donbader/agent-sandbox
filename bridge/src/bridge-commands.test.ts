import { describe, it, expect, vi } from "vitest";
import { handleBridgeCommand, type BridgeCommandContext } from "./bridge-commands.js";

const defaultCtx: BridgeCommandContext = {
  agentCmd: ["npx", "codex-acp"],
  perfHistory: [],
};

describe("handleBridgeCommand", () => {
  describe("/sh", () => {
    it("returns usage when no args", () => {
      expect(handleBridgeCommand("/sh", defaultCtx)).toBe("Usage: /sh <command>");
    });

    it("executes command and returns output", () => {
      const result = handleBridgeCommand("/sh echo hello", defaultCtx);
      expect(result).toBe("hello");
    });

    it("returns (no output) for silent commands", () => {
      const result = handleBridgeCommand("/sh true", defaultCtx);
      expect(result).toBe("(no output)");
    });

    it("returns exit code and stderr on failure", () => {
      const result = handleBridgeCommand("/sh false", defaultCtx);
      expect(result).toContain("Exit 1");
    });

    it("truncates long output at 4000 chars", () => {
      // Generate output longer than 4000 chars
      const result = handleBridgeCommand("/sh seq 1 5000", defaultCtx);
      expect(result!.length).toBeLessThanOrEqual(4000);
    });
  });

  describe("/diagnose", () => {
    it("returns diagnostics info", () => {
      const result = handleBridgeCommand("/diagnose", defaultCtx);
      expect(result).toContain("🔍 Agent Diagnostics:");
      expect(result).toContain("PID:");
      expect(result).toContain("Uptime:");
      expect(result).toContain("Agent cmd: npx codex-acp");
    });

    it("includes perf stats when available", () => {
      const ctx: BridgeCommandContext = {
        agentCmd: ["npx", "codex-acp"],
        perfHistory: [100, 200, 300],
      };
      const result = handleBridgeCommand("/diagnose", ctx);
      expect(result).toContain("Perf (3 prompts)");
      expect(result).toContain("avg 200ms");
    });

    it("omits perf stats when no history", () => {
      const result = handleBridgeCommand("/diagnose", defaultCtx);
      expect(result).not.toContain("Perf");
    });
  });

  describe("non-commands", () => {
    it("returns null for regular text", () => {
      expect(handleBridgeCommand("hello world", defaultCtx)).toBeNull();
    });

    it("returns null for unknown commands", () => {
      expect(handleBridgeCommand("/model gpt-4o", defaultCtx)).toBeNull();
    });

    it("returns null for /new", () => {
      expect(handleBridgeCommand("/new", defaultCtx)).toBeNull();
    });
  });
});
