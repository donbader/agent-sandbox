import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock grammy before importing channel
let messageHandler: ((ctx: any) => void) | null = null;
let startCallback: ((info: any) => void) | null = null;
let mockBotApi: any;

vi.mock("grammy", () => {
  mockBotApi = {
    sendMessage: vi.fn().mockResolvedValue({}),
    setMessageReaction: vi.fn().mockResolvedValue({}),
    sendChatAction: vi.fn().mockResolvedValue({}),
    setMyCommands: vi.fn().mockResolvedValue(true),
  };
  return {
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
      api: mockBotApi,
    })),
  };
});

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

// Mock AcpAgent
function createMockAgent() {
  const listeners: Array<(cmds: any[]) => void> = [];
  const connection = {
    newSession: vi.fn().mockResolvedValue({ sessionId: "test-session-123" }),
  };
  return {
    isReady: vi.fn().mockReturnValue(true),
    getConnection: vi.fn().mockReturnValue(connection),
    prompt: vi.fn().mockResolvedValue("Agent response"),
    abort: vi.fn(),
    stop: vi.fn(),
    start: vi.fn().mockResolvedValue(undefined),
    reset: vi.fn().mockResolvedValue(undefined),
    getAgentCommands: vi.fn().mockReturnValue([]),
    onCommandsUpdate: vi.fn((cb: any) => listeners.push(cb)),
    _triggerCommandsUpdate(cmds: any[]) {
      for (const cb of listeners) cb(cmds);
    },
    _connection: connection,
  };
}

// Import after mock setup
const { default: createTelegramChannel } = await import("./channel.js");

function makeCtx(opts: {
  chatId: string;
  username?: string;
  text: string;
  chatType?: "private" | "group" | "supergroup";
  messageId?: number;
}) {
  return {
    chat: { id: Number(opts.chatId), type: opts.chatType ?? "private" },
    from: opts.username ? { username: opts.username } : undefined,
    message: { text: opts.text, message_id: opts.messageId ?? 1 },
  };
}

describe("TelegramChannel (thin channel manager)", () => {
  let agent: ReturnType<typeof createMockAgent>;

  beforeEach(async () => {
    vi.clearAllMocks();
    agent = createMockAgent();
    const ch = createTelegramChannel({}, agent as any);
    await ch.start();
    // Simulate bot connected
    startCallback?.({ username: "testbot" });
  });

  describe("message forwarding", () => {
    it("forwards regular messages to agent", async () => {
      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith("test-session-123", "hello");
    });

    it("creates a session on first message from a chat", async () => {
      messageHandler!(makeCtx({ chatId: "456", username: "bob", text: "hi" }));
      await vi.waitFor(() => expect(agent._connection.newSession).toHaveBeenCalled());
    });

    it("reuses session for same chat", async () => {
      messageHandler!(makeCtx({ chatId: "789", username: "carol", text: "first" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(1));

      messageHandler!(makeCtx({ chatId: "789", username: "carol", text: "second" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(2));

      // newSession should only be called once
      expect(agent._connection.newSession).toHaveBeenCalledTimes(1);
    });
  });

  describe("custom commands", () => {
    it("all /commands are forwarded to agent", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "/model gpt-4o" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith("test-session-123", "/model gpt-4o");
    });

    it("/sh is forwarded to agent (handled by ACP wrapper)", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "/sh ls" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith("test-session-123", "/sh ls");
    });

    it("/diagnose is forwarded to agent (handled by ACP wrapper)", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "/diagnose" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith("test-session-123", "/diagnose");
    });

    it("/command@botname strips mention before forwarding", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "/model@testbot gpt-4o" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith("test-session-123", "/model gpt-4o");
    });
  });

  describe("access control", () => {
    it("ignores unauthorized users", async () => {
      // Create a new channel with ACL
      const ch = createTelegramChannel(
        { access_control: { allowed_users: ["@alice"] } },
        agent as any
      );
      await ch.start();
      startCallback?.({ username: "testbot" });

      messageHandler!(makeCtx({ chatId: "100", username: "bob", text: "hi" }));
      expect(agent.prompt).not.toHaveBeenCalled();
    });
  });

  describe("startup buffer", () => {
    it("buffers messages before bot is ready", async () => {
      const freshAgent = createMockAgent();
      const ch = createTelegramChannel({}, freshAgent as any);
      // Call start but don't trigger startCallback — bot not connected yet
      await ch.start();

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "buffered" }));
      expect(freshAgent.prompt).not.toHaveBeenCalled();
    });
  });

  describe("bot menu registration", () => {
    it("does not register commands if agent has none", () => {
      // Agent has no commands declared yet
      expect(mockBotApi.setMyCommands).not.toHaveBeenCalled();
    });

    it("registers when agent commands update", () => {
      agent.getAgentCommands.mockReturnValue([
        { name: "model", description: "Switch model" },
        { name: "new", description: "New conversation" },
      ]);
      agent._triggerCommandsUpdate([
        { name: "model", description: "Switch model" },
        { name: "new", description: "New conversation" },
      ]);
      expect(mockBotApi.setMyCommands).toHaveBeenCalled();
      const calls = mockBotApi.setMyCommands.mock.calls[0][0];
      expect(calls.some((c: any) => c.command === "model")).toBe(true);
      expect(calls.some((c: any) => c.command === "new")).toBe(true);
    });
  });
});
