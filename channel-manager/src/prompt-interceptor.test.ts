import { describe, it, expect, vi } from "vitest";
import { handleWrapperCommand } from "./wrapper-commands.js";

/**
 * Tests for the prompt interceptor pattern used in index.ts.
 * We test the logic without importing AcpAgent (which requires @agentclientprotocol/sdk).
 */

describe("prompt interceptor logic", () => {
  const wrapperCtx = { agentCmd: ["codex", "exec"], perfHistory: [] as number[], cwd: "/workspace" };

  // Simulates the interceptor logic from index.ts
  async function simulateInterceptor(
    text: string,
    sessionId: string,
    commandPlugins: Array<{
      name: string;
      commands: Record<string, (ctx: any) => Promise<void>>;
      onMessage?: (text: string, chatId: string, reply: (msg: string) => void) => Promise<boolean>;
    }>,
  ): Promise<string | null> {
    // 1. Sync wrapper commands
    const wrapperResult = handleWrapperCommand(text, wrapperCtx);
    if (wrapperResult !== null) return wrapperResult;

    // 2. Command plugins
    const trimmed = text.trim();
    if (trimmed.startsWith("/")) {
      const [cmd, ...args] = trimmed.slice(1).split(/\s+/);
      for (const plugin of commandPlugins) {
        if (cmd in plugin.commands) {
          let response = "";
          await plugin.commands[cmd]({
            args: args.join(" "),
            chatId: sessionId,
            reply: (msg: string) => { response = msg; },
          });
          return response || null;
        }
      }
    }

    // 3. onMessage interceptors
    for (const plugin of commandPlugins) {
      if (plugin.onMessage) {
        let response = "";
        const handled = await plugin.onMessage(text, sessionId, (msg: string) => { response = msg; });
        if (handled) return response || null;
      }
    }

    return null;
  }

  describe("wrapper commands", () => {
    it("/sh echo hello is intercepted", async () => {
      const result = await simulateInterceptor("/sh echo hello", "s1", []);
      expect(result).toBe("hello");
    });

    it("/diagnose is intercepted", async () => {
      const result = await simulateInterceptor("/diagnose", "s1", []);
      expect(result).toContain("Agent Diagnostics");
    });

    it("unknown /commands pass through", async () => {
      const result = await simulateInterceptor("/unknown", "s1", []);
      expect(result).toBeNull();
    });

    it("normal text passes through", async () => {
      const result = await simulateInterceptor("hello world", "s1", []);
      expect(result).toBeNull();
    });
  });

  describe("command plugins", () => {
    it("/oauth is handled by plugin", async () => {
      const mockPlugin = {
        name: "mcp-oauth",
        commands: {
          oauth: vi.fn(async (ctx: any) => {
            ctx.reply(`OAuth flow for ${ctx.args}`);
          }),
        },
      };

      const result = await simulateInterceptor("/oauth notion", "s1", [mockPlugin]);
      expect(result).toBe("OAuth flow for notion");
      expect(mockPlugin.commands.oauth).toHaveBeenCalledWith(
        expect.objectContaining({ args: "notion", chatId: "s1" }),
      );
    });

    it("/oauth with no args works", async () => {
      const mockPlugin = {
        name: "mcp-oauth",
        commands: {
          oauth: vi.fn(async (ctx: any) => {
            ctx.reply("Available providers: notion");
          }),
        },
      };

      const result = await simulateInterceptor("/oauth", "s1", [mockPlugin]);
      expect(result).toBe("Available providers: notion");
    });

    it("unregistered commands pass to agent", async () => {
      const mockPlugin = {
        name: "mcp-oauth",
        commands: { oauth: vi.fn() },
      };

      const result = await simulateInterceptor("/model gpt-4", "s1", [mockPlugin]);
      expect(result).toBeNull();
    });
  });

  describe("onMessage interceptor (paste-back)", () => {
    it("callback URLs are intercepted", async () => {
      const mockPlugin = {
        name: "mcp-oauth",
        commands: {},
        onMessage: vi.fn(async (text: string, _chatId: string, reply: (msg: string) => void) => {
          if (text.includes("code=") && text.includes("state=")) {
            reply("Token saved!");
            return true;
          }
          return false;
        }),
      };

      const result = await simulateInterceptor(
        "http://localhost:3000/callback?code=abc&state=xyz",
        "s1",
        [mockPlugin],
      );
      expect(result).toBe("Token saved!");
    });

    it("normal messages are not intercepted", async () => {
      const mockPlugin = {
        name: "mcp-oauth",
        commands: {},
        onMessage: vi.fn(async () => false),
      };

      const result = await simulateInterceptor("just chatting", "s1", [mockPlugin]);
      expect(result).toBeNull();
    });
  });

  describe("priority order", () => {
    it("wrapper commands take priority over plugins", async () => {
      const mockPlugin = {
        name: "test",
        commands: {
          sh: vi.fn(async (ctx: any) => ctx.reply("plugin sh")),
        },
      };

      const result = await simulateInterceptor("/sh echo priority", "s1", [mockPlugin]);
      expect(result).toBe("priority");
      expect(mockPlugin.commands.sh).not.toHaveBeenCalled();
    });
  });
});
