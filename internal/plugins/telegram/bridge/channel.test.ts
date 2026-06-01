import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock grammy before importing channel
let messageHandler: ((ctx: any) => void) | null = null;
let startCallback: ((info: any) => void) | null = null;

vi.mock("grammy", () => ({
  Bot: vi.fn().mockImplementation(() => ({
    on: vi.fn((event: string, handler: any) => {
      if (event === "message:text") {
        messageHandler = handler;
      }
    }),
    catch: vi.fn(),
    start: vi.fn(({ onStart }: any) => {
      startCallback = onStart;
    }),
    stop: vi.fn(),
    api: {
      sendMessage: vi.fn().mockResolvedValue({}),
      setMessageReaction: vi.fn().mockResolvedValue({}),
      sendChatAction: vi.fn().mockResolvedValue({}),
      setMyCommands: vi.fn().mockResolvedValue(true),
    },
  })),
}));

vi.mock("../logger.js", () => ({
  createLogger: () => ({
    info: vi.fn(),
    debug: vi.fn(),
    error: vi.fn(),
    warn: vi.fn(),
  }),
}));

vi.mock("../delivery/rate-limiter.js", () => ({
  RateLimiter: vi.fn().mockImplementation(() => ({
    acquire: vi.fn().mockResolvedValue(undefined),
  })),
}));

vi.mock("../delivery/api-retry.js", () => ({
  withRetry: vi.fn().mockImplementation((fn: () => Promise<any>) => fn()),
}));

vi.mock("../formatter/telegram.js", () => ({
  formatMarkdown: vi.fn().mockImplementation((text: string) => text),
  splitMessage: vi.fn().mockImplementation((text: string) => [text]),
}));

// Import after mock setup
const { default: TelegramChannel } = await import("./channel.js");

function makeCtx(opts: {
  chatId: string;
  username?: string;
  text: string;
  chatType?: "private" | "group" | "supergroup";
}) {
  return {
    chat: {
      id: Number(opts.chatId),
      type: opts.chatType ?? "private",
    },
    from: opts.username ? { username: opts.username } : {},
    message: { text: opts.text, message_id: 1 },
  };
}

describe("TelegramChannel", () => {
  let channel: InstanceType<typeof TelegramChannel>;
  let handler: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    messageHandler = null;
    startCallback = null;
    handler = vi.fn();
  });

  describe("allowed_users", () => {
    it("allows messages from authorized users", () => {
      channel = new TelegramChannel({ access_control: { allowed_users: ["@alice"] } });
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello" }));
      expect(handler).toHaveBeenCalledWith("123", "hello");
    });

    it("blocks messages from unauthorized users", () => {
      channel = new TelegramChannel({ access_control: { allowed_users: ["@alice"] } });
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", username: "bob", text: "hello" }));
      expect(handler).not.toHaveBeenCalled();
    });

    it("blocks messages from users without username when allowed_users is set", () => {
      channel = new TelegramChannel({ access_control: { allowed_users: ["@alice"] } });
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", text: "hello" }));
      expect(handler).not.toHaveBeenCalled();
    });

    it("allows all messages when allowed_users is empty", () => {
      channel = new TelegramChannel({ access_control: {} });
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", username: "anyone", text: "hello" }));
      expect(handler).toHaveBeenCalledWith("123", "hello");
    });

    it("allows all messages when no config provided", () => {
      channel = new TelegramChannel({});
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", username: "anyone", text: "hello" }));
      expect(handler).toHaveBeenCalledWith("123", "hello");
    });
  });

  describe("require_mention", () => {
    it("passes messages in private chats regardless of require_mention", () => {
      channel = new TelegramChannel({ access_control: { require_mention: true } });
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello", chatType: "private" }));
      expect(handler).toHaveBeenCalledWith("123", "hello");
    });

    it("blocks messages in groups without @mention when require_mention is true", async () => {
      channel = new TelegramChannel({ access_control: { require_mention: true } });
      channel.onMessage(handler);
      await channel.start();
      startCallback?.({ username: "mybot" });

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello", chatType: "group" }));
      expect(handler).not.toHaveBeenCalled();
    });

    it("passes messages in groups with @mention when require_mention is true", async () => {
      channel = new TelegramChannel({ access_control: { require_mention: true } });
      channel.onMessage(handler);
      await channel.start();
      startCallback?.({ username: "mybot" });

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hey @mybot do something", chatType: "group" }));
      expect(handler).toHaveBeenCalledWith("123", "hey @mybot do something");
    });

    it("passes all group messages when require_mention is false", () => {
      channel = new TelegramChannel({ access_control: { require_mention: false } });
      channel.onMessage(handler);

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello", chatType: "group" }));
      expect(handler).toHaveBeenCalledWith("123", "hello");
    });
  });

  describe("per-group ACL overrides", () => {
    it("uses group-specific allowed_users over top-level", () => {
      channel = new TelegramChannel({
        access_control: {
          allowed_users: ["@alice"],
          groups: { "456": { allowed_users: ["@bob"] } },
        },
      });
      channel.onMessage(handler);

      // alice is top-level allowed but not in group 456
      messageHandler!(makeCtx({ chatId: "456", username: "alice", text: "hi", chatType: "group" }));
      expect(handler).not.toHaveBeenCalled();

      // bob is allowed in group 456
      messageHandler!(makeCtx({ chatId: "456", username: "bob", text: "hi", chatType: "group" }));
      expect(handler).toHaveBeenCalledWith("456", "hi");
    });

    it("uses group-specific require_mention over top-level", async () => {
      channel = new TelegramChannel({
        access_control: {
          require_mention: false,
          groups: { "789": { require_mention: true } },
        },
      });
      channel.onMessage(handler);
      await channel.start();
      startCallback?.({ username: "mybot" });

      // Group 789 requires mention even though top-level doesn't
      messageHandler!(makeCtx({ chatId: "789", username: "alice", text: "hello", chatType: "supergroup" }));
      expect(handler).not.toHaveBeenCalled();

      messageHandler!(makeCtx({ chatId: "789", username: "alice", text: "hey @mybot", chatType: "supergroup" }));
      expect(handler).toHaveBeenCalledWith("789", "hey @mybot");
    });

    it("falls back to top-level for groups without override", () => {
      channel = new TelegramChannel({
        access_control: {
          allowed_users: ["@alice"],
          groups: { "456": { allowed_users: ["@bob"] } },
        },
      });
      channel.onMessage(handler);

      // Group 999 has no override, uses top-level allowed_users
      messageHandler!(makeCtx({ chatId: "999", username: "alice", text: "hi", chatType: "group" }));
      expect(handler).toHaveBeenCalledWith("999", "hi");
    });
  });

  describe("handler assignment", () => {
    it("does not call handler when none is registered", () => {
      channel = new TelegramChannel({});
      // Don't call onMessage — handler stays null

      // Should not throw
      expect(() => {
        messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello" }));
      }).not.toThrow();
    });
  });

  describe("registerCommands", () => {
    it("calls setMyCommands with sanitized command names", async () => {
      channel = new TelegramChannel({});
      const mockApi = (channel as any).bot.api;

      await channel.registerCommands([
        { name: "new", description: "Start a new session" },
        { name: "stop", description: "Stop current operation" },
      ]);

      expect(mockApi.setMyCommands).toHaveBeenCalledWith([
        { command: "new", description: "Start a new session" },
        { command: "stop", description: "Stop current operation" },
      ]);
    });

    it("sanitizes command names (lowercase, replace invalid chars)", async () => {
      channel = new TelegramChannel({});
      const mockApi = (channel as any).bot.api;

      await channel.registerCommands([
        { name: "My-Command", description: "test" },
        { name: "UPPER", description: "test" },
      ]);

      expect(mockApi.setMyCommands).toHaveBeenCalledWith([
        { command: "my_command", description: "test" },
        { command: "upper", description: "test" },
      ]);
    });

    it("filters out commands with empty names after sanitization", async () => {
      channel = new TelegramChannel({});
      const mockApi = (channel as any).bot.api;

      await channel.registerCommands([
        { name: "---", description: "all invalid" },
        { name: "valid", description: "ok" },
      ]);

      expect(mockApi.setMyCommands).toHaveBeenCalledWith([
        { command: "valid", description: "ok" },
      ]);
    });

    it("truncates descriptions to 256 chars", async () => {
      channel = new TelegramChannel({});
      const mockApi = (channel as any).bot.api;

      const longDesc = "a".repeat(300);
      await channel.registerCommands([{ name: "test", description: longDesc }]);

      const calls = mockApi.setMyCommands.mock.calls[0][0];
      expect(calls[0].description).toHaveLength(256);
    });

    it("does not throw when setMyCommands fails", async () => {
      channel = new TelegramChannel({});
      const mockApi = (channel as any).bot.api;
      mockApi.setMyCommands.mockRejectedValueOnce(new Error("API error"));

      await expect(
        channel.registerCommands([{ name: "test", description: "test" }])
      ).resolves.toBeUndefined();
    });
  });
});
