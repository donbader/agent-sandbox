import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock grammy before importing channel
let messageHandler: ((ctx: any) => void) | null = null;
let startCallback: ((info: any) => void) | null = null;
let mockBotApi: any;

vi.mock("grammy", () => {
  mockBotApi = {
    sendMessage: vi.fn().mockResolvedValue({ message_id: 42 }),
    editMessageText: vi.fn().mockResolvedValue({}),
    setMessageReaction: vi.fn().mockResolvedValue({}),
    sendChatAction: vi.fn().mockResolvedValue({}),
    setMyCommands: vi.fn().mockResolvedValue(true),
    callApi: vi.fn().mockResolvedValue({}),
  };
  return {
    Bot: vi.fn().mockImplementation(function () {
      return {
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
      };
    }),
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
  RateLimiter: vi.fn().mockImplementation(function () {
    return {
      acquire: vi.fn().mockResolvedValue(undefined),
    };
  }),
}));

vi.mock("../delivery/api-retry.js", () => ({
  withRetry: vi.fn().mockImplementation((fn: () => Promise<any>) => fn()),
}));

vi.mock("../formatter/telegram.js", () => ({
  formatMarkdown: vi.fn().mockImplementation((text: string) => text),
  closeOpenTags: vi.fn().mockImplementation((text: string) => text),
  splitMessage: vi.fn().mockImplementation((text: string) => [text]),
  MAX_MESSAGE_LENGTH: 4096,
}));

vi.mock("../startup-buffer.js", () => ({
  StartupBuffer: vi.fn().mockImplementation(function () {
    return {
      push: vi.fn().mockReturnValue(false),
      flush: vi.fn().mockReturnValue([]),
    };
  }),
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

describe("TelegramChannel behavior", () => {
  let agent: ReturnType<typeof createMockAgent>;

  beforeEach(async () => {
    vi.clearAllMocks();
    agent = createMockAgent();
    const ch = createTelegramChannel({}, agent as any);
    await ch.start();
    startCallback?.({ username: "testbot" });
  });

  // -------------------------------------------------------------------------
  // Message Delivery — user messages reach agent, agent responses reach user
  // -------------------------------------------------------------------------

  describe("message delivery", () => {
    it("delivers a plain text message to the agent", async () => {
      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith(
        "test-session-123",
        "hello",
        expect.objectContaining({ onSessionUpdate: expect.any(Function) }),
      );
    });

    it("delivers /command with args to agent", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "/model gpt-4o" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith(
        "test-session-123",
        "/model gpt-4o",
        expect.any(Object),
      );
    });

    it("strips @botname from /command@bot before forwarding", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "/model@testbot gpt-4o" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(agent.prompt).toHaveBeenCalledWith(
        "test-session-123",
        "/model gpt-4o",
        expect.any(Object),
      );
    });

    it("sends ack emoji reaction on message receipt", async () => {
      const ch = createTelegramChannel({ ack_emoji: "👀" }, agent as any);
      await ch.start();
      startCallback?.({ username: "testbot" });

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "hi", messageId: 7 }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

      expect(mockBotApi.setMessageReaction).toHaveBeenCalledWith(100, 7, [
        { type: "emoji", emoji: "👀" },
      ]);
    });

    it("sends typing indicator while processing", async () => {
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "hi" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
      expect(mockBotApi.sendChatAction).toHaveBeenCalledWith(100, "typing");
    });
  });

  // -------------------------------------------------------------------------
  // Concurrency — same-chat serialized, cross-chat parallel
  // -------------------------------------------------------------------------

  describe("concurrency", () => {
    it("serializes prompts from the same chat", async () => {
      let resolveFirst: () => void;
      const firstPrompt = new Promise<void>((r) => { resolveFirst = r; });

      const promptOrder: string[] = [];
      agent.prompt.mockImplementation(async (_sid: string, text: string) => {
        promptOrder.push(text);
        if (text === "first") await firstPrompt;
        return "done";
      });

      messageHandler!(makeCtx({ chatId: "999", username: "alice", text: "first" }));
      messageHandler!(makeCtx({ chatId: "999", username: "alice", text: "second" }));

      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(1));
      expect(promptOrder).toEqual(["first"]);

      resolveFirst!();
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(2));
      expect(promptOrder).toEqual(["first", "second"]);
    });

    it("allows concurrent prompts from different chats", async () => {
      let resolveFirst: () => void;
      const firstPrompt = new Promise<void>((r) => { resolveFirst = r; });

      const promptOrder: string[] = [];
      agent.prompt.mockImplementation(async (_sid: string, text: string) => {
        promptOrder.push(text);
        if (text === "chat-a") await firstPrompt;
        return "done";
      });

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "chat-a" }));
      messageHandler!(makeCtx({ chatId: "200", username: "bob", text: "chat-b" }));

      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(2));
      expect(promptOrder).toContain("chat-a");
      expect(promptOrder).toContain("chat-b");

      resolveFirst!();
    });
  });

  // -------------------------------------------------------------------------
  // Error Recovery — bot stays alive after failures
  // -------------------------------------------------------------------------

  describe("error recovery", () => {
    it("continues serving after agent prompt rejects", async () => {
      agent.prompt.mockRejectedValueOnce(new Error("agent crashed"));

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "crash me" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(1));

      // Bot still works — send another message
      agent.prompt.mockResolvedValue("recovered");
      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "still alive?" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(2));
    });

    it("continues serving after ack reaction fails", async () => {
      mockBotApi.setMessageReaction.mockRejectedValueOnce(new Error("reaction failed"));

      const ch = createTelegramChannel({ ack_emoji: "👀" }, agent as any);
      await ch.start();
      startCallback?.({ username: "testbot" });

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "hi" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

      // Prompt still reached agent despite ack failure
      expect(agent.prompt).toHaveBeenCalledWith(
        "test-session-123",
        "hi",
        expect.any(Object),
      );
    });

    it("continues serving after typing indicator fails", async () => {
      mockBotApi.sendChatAction.mockRejectedValueOnce(new Error("typing failed"));

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "hi" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

      expect(agent.prompt).toHaveBeenCalledWith(
        "test-session-123",
        "hi",
        expect.any(Object),
      );
    });

    it("queued message still runs after previous message errors", async () => {
      const promptOrder: string[] = [];
      agent.prompt
        .mockImplementationOnce(async (_sid: string, text: string) => {
          promptOrder.push(text);
          throw new Error("first failed");
        })
        .mockImplementationOnce(async (_sid: string, text: string) => {
          promptOrder.push(text);
          return "ok";
        });

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "will-fail" }));
      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "should-still-run" }));

      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(2));
      expect(promptOrder).toEqual(["will-fail", "should-still-run"]);
    });
  });

  // -------------------------------------------------------------------------
  // Streaming — progressive response delivery
  // -------------------------------------------------------------------------

  describe("streaming", () => {
    it("passes onSessionUpdate to agent for stream events", async () => {
      agent.prompt.mockImplementation(async (_sid: string, _text: string, opts?: any) => {
        opts?.onSessionUpdate?.({
          update: {
            sessionUpdate: "agent_message_chunk",
            content: { type: "text", text: "Streamed!" },
          },
        });
        return "Streamed!";
      });

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "hello" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

      expect(agent.prompt).toHaveBeenCalledWith(
        "test-session-123",
        "hello",
        expect.objectContaining({ onSessionUpdate: expect.any(Function) }),
      );
    });

    it("sends thinking draft via callApi for thinking chunks", async () => {
      agent.prompt.mockImplementation(async (_sid: string, _text: string, opts?: any) => {
        opts?.onSessionUpdate?.({
          update: {
            sessionUpdate: "agent_thought_chunk",
            content: { type: "text", text: "Let me analyze..." },
          },
        });
        opts?.onSessionUpdate?.({
          update: {
            sessionUpdate: "agent_message_chunk",
            content: { type: "text", text: "Done." },
          },
        });
        return "done";
      });

      messageHandler!(makeCtx({ chatId: "123", username: "alice", text: "think" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());

      expect(mockBotApi.callApi).toHaveBeenCalledWith("sendMessageDraft", {
        chat_id: 123,
        draft_id: 1,
        text: "🧠 Let me analyze...",
        parse_mode: "HTML",
      });
    });
  });

  // -------------------------------------------------------------------------
  // Access Control — unauthorized users are rejected
  // -------------------------------------------------------------------------

  describe("access control", () => {
    it("rejects unauthorized users", async () => {
      const ch = createTelegramChannel(
        { access_control: { allowed_users: ["@alice"] } },
        agent as any,
      );
      await ch.start();
      startCallback?.({ username: "testbot" });

      messageHandler!(makeCtx({ chatId: "100", username: "bob", text: "hi" }));
      expect(agent.prompt).not.toHaveBeenCalled();
    });

    it("allows authorized users", async () => {
      const ch = createTelegramChannel(
        { access_control: { allowed_users: ["@alice"] } },
        agent as any,
      );
      await ch.start();
      startCallback?.({ username: "testbot" });

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "hi" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalled());
    });
  });

  // -------------------------------------------------------------------------
  // Session Lifecycle — reuse, creation, startup buffering
  // -------------------------------------------------------------------------

  describe("session lifecycle", () => {
    it("creates a session on first message from a chat", async () => {
      messageHandler!(makeCtx({ chatId: "456", username: "bob", text: "hi" }));
      await vi.waitFor(() => expect(agent._connection.newSession).toHaveBeenCalled());
    });

    it("reuses session for subsequent messages from same chat", async () => {
      messageHandler!(makeCtx({ chatId: "789", username: "carol", text: "first" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(1));

      messageHandler!(makeCtx({ chatId: "789", username: "carol", text: "second" }));
      await vi.waitFor(() => expect(agent.prompt).toHaveBeenCalledTimes(2));

      expect(agent._connection.newSession).toHaveBeenCalledTimes(1);
    });

    it("buffers messages before bot is connected", async () => {
      const freshAgent = createMockAgent();
      const ch = createTelegramChannel({}, freshAgent as any);
      await ch.start();
      // Don't trigger startCallback — bot not connected yet

      messageHandler!(makeCtx({ chatId: "100", username: "alice", text: "buffered" }));
      expect(freshAgent.prompt).not.toHaveBeenCalled();
    });
  });

  // -------------------------------------------------------------------------
  // Bot Commands — menu registration from agent-declared commands
  // -------------------------------------------------------------------------

  describe("bot commands", () => {
    it("does not register commands if agent has none", () => {
      expect(mockBotApi.setMyCommands).not.toHaveBeenCalled();
    });

    it("registers bot menu when agent commands update", () => {
      agent.getAgentCommands.mockReturnValue([
        { name: "model", description: "Switch model" },
        { name: "new", description: "New conversation" },
      ]);
      agent._triggerCommandsUpdate([
        { name: "model", description: "Switch model" },
        { name: "new", description: "New conversation" },
      ]);
      expect(mockBotApi.setMyCommands).toHaveBeenCalled();
      const commands = mockBotApi.setMyCommands.mock.calls[0][0];
      expect(commands).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ command: "model" }),
          expect.objectContaining({ command: "new" }),
        ]),
      );
    });
  });
});
