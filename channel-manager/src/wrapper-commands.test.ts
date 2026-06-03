import { describe, it, expect, vi } from "vitest";
import { handleWrapperCommand, type WrapperCommandContext } from "./wrapper-commands.js";

const defaultCtx: WrapperCommandContext = {
  agentCmd: ["npx", "codex-acp"],
  perfHistory: [],
  cwd: "/workspace",
};

describe("handleWrapperCommand", () => {
  describe("/sh", () => {
    it("returns usage when no args", () => {
      expect(handleWrapperCommand("/sh", defaultCtx)).toBe("Usage: /sh <command>");
    });

    it("executes command and returns output", () => {
      const result = handleWrapperCommand("/sh echo hello", defaultCtx);
      expect(result).toBe("hello");
    });

    it("returns (no output) for silent commands", () => {
      const result = handleWrapperCommand("/sh true", defaultCtx);
      expect(result).toBe("(no output)");
    });

    it("returns exit code and stderr on failure", () => {
      const result = handleWrapperCommand("/sh false", defaultCtx);
      expect(result).toContain("Exit 1");
    });

    it("truncates long output at 4000 chars", () => {
      // Generate output longer than 4000 chars
      const result = handleWrapperCommand("/sh seq 1 5000", defaultCtx);
      expect(result!.length).toBeLessThanOrEqual(4000);
    });
  });

  describe("/diagnose", () => {
    it("returns diagnostics info", () => {
      const result = handleWrapperCommand("/diagnose", defaultCtx);
      expect(result).toContain("🔍 Agent Diagnostics:");
      expect(result).toContain("PID:");
      expect(result).toContain("Uptime:");
      expect(result).toContain("Agent cmd: npx codex-acp");
    });

    it("includes perf stats when available", () => {
      const ctx: WrapperCommandContext = {
        agentCmd: ["npx", "codex-acp"],
        perfHistory: [100, 200, 300],
        cwd: "/workspace",
      };
      const result = handleWrapperCommand("/diagnose", ctx);
      expect(result).toContain("Perf (3 prompts)");
      expect(result).toContain("avg 200ms");
    });

    it("omits perf stats when no history", () => {
      const result = handleWrapperCommand("/diagnose", defaultCtx);
      expect(result).not.toContain("Perf");
    });
  });

  describe("non-commands", () => {
    it("returns null for regular text", () => {
      expect(handleWrapperCommand("hello world", defaultCtx)).toBeNull();
    });

    it("returns null for unknown commands", () => {
      expect(handleWrapperCommand("/model gpt-4o", defaultCtx)).toBeNull();
    });

    it("returns null for /new", () => {
      expect(handleWrapperCommand("/new", defaultCtx)).toBeNull();
    });
  });
});
