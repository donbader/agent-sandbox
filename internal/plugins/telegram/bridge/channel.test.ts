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
    start: vi.fn(({ onStart }: any) => {
      startCallback = onStart;
    }),
    stop: vi.fn(),
    api: {
      sendMessage: vi.fn().mockResolvedValue({}),
    },
  })),
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
    message: { text: opts.text },
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
});
