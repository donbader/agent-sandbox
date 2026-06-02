import { Bot } from "grammy";
import type { ReactionTypeEmoji } from "@grammyjs/types";
import type { Channel } from "./types.js";
import type { AcpAgent, AgentCommand } from "../acp-client.js";
import { createLogger } from "../logger.js";
import { RateLimiter } from "./delivery/rate-limiter.js";
import { withRetry } from "./delivery/api-retry.js";
import { formatMarkdown, splitMessage } from "./formatter/telegram.js";

const log = createLogger("telegram");
const DUMMY_TOKEN = "000000000:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

type ReactionEmoji = ReactionTypeEmoji["emoji"];

const VALID_REACTION_EMOJIS: Set<string> = new Set([
  "👍", "👎", "❤", "🔥", "🥰", "👏", "😁", "🤔", "🤯", "😱", "🤬", "😢",
  "🎉", "🤩", "🤮", "💩", "🙏", "👌", "🕊", "🤡", "🥱", "🥴", "😍", "🐳",
  "❤\u200D🔥", "🌚", "🌭", "💯", "🤣", "⚡", "🍌", "🏆", "💔", "🤨", "😐",
  "🍓", "🍾", "💋", "🖕", "😈", "😴", "😭", "🤓", "👻", "👨\u200D💻", "👀",
  "🎃", "🙈", "😇", "😨", "🤝", "✍", "🤗", "🫡", "🎅", "🎄", "☃", "💅",
  "🤪", "🗿", "🆒", "💘", "🙉", "🦄", "😘", "💊", "🙊", "😎", "👾",
  "🤷\u200D♂", "🤷", "🤷\u200D♀", "😡",
]);

function isValidReactionEmoji(emoji: string): emoji is ReactionEmoji {
  return VALID_REACTION_EMOJIS.has(emoji);
}

/** Sanitize a command name for Telegram (lowercase a-z, 0-9, underscore only). */
function sanitizeCommandName(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9_]/g, "_").replace(/^_+|_+$/g, "");
}

interface AccessControl {
  allowed_users?: string[];
  require_mention?: boolean;
  groups?: Record<string, { allowed_users?: string[]; require_mention?: boolean }>;
}

interface BufferedMessage {
  chatId: string;
  text: string;
  messageId: number;
  timestamp: number;
}

/**
 * Telegram channel that talks ACP directly.
 * Handles: platform UX, session mapping, custom commands, message forwarding.
 */
export default function createTelegramChannel(
  config: Record<string, unknown>,
  agent: AcpAgent
): Channel {
  const acl = (config?.access_control as AccessControl) ?? {};
  const ackRaw = config?.ack_emoji;
  let ackEmoji: ReactionEmoji | null;
  if (ackRaw === undefined) {
    ackEmoji = "👀";
  } else if (typeof ackRaw === "string" && isValidReactionEmoji(ackRaw)) {
    ackEmoji = ackRaw;
  } else if (!ackRaw) {
    ackEmoji = null;
  } else {
    log.warn({ ack_emoji: ackRaw }, "invalid ack_emoji, falling back to 👀");
    ackEmoji = "👀";
  }

  const bot = new Bot(DUMMY_TOKEN);
  const rateLimiter = new RateLimiter(100);
  let botUsername: string | null = null;

  // Session mapping: chatId → ACP sessionId
  const sessions = new Map<string, string>();

  // Perf tracking: last N response times (ms)
  const perfHistory: number[] = [];
  const PERF_MAX = 50;

  // Startup buffer: queue messages until agent is ready
  const buffer: BufferedMessage[] = [];
  let ready = false;

  // --- Session management ---

  async function getOrCreateSession(chatId: string): Promise<string> {
    const existing = sessions.get(chatId);
    if (existing) return existing;

    const conn = agent.getConnection();
    if (!conn) throw new Error("Agent not connected");

    const result = await conn.newSession({ cwd: "/workspace" });
    const sessionId = result.sessionId;
    sessions.set(chatId, sessionId);
    log.info({ chatId, sessionId: sessionId.slice(0, 8) }, "created session");
    return sessionId;
  }

  // --- Commands ---
  // None — all commands handled by ACP wrapper on agent side.
  // Channel is a pure platform adapter.

  // --- Message handling ---

  async function processMessage(chatId: string, text: string, messageId: number): Promise<void> {
    // Ack
    if (ackEmoji) {
      ackMessage(chatId, messageId);
    }

    // Typing indicator
    sendTyping(chatId);

    // Command routing — all forwarded to agent (wrapper handles bridge commands)
    if (text.startsWith("/")) {
      const spaceIdx = text.indexOf(" ");
      const cmd = spaceIdx === -1 ? text.slice(1) : text.slice(1, spaceIdx);
      // Strip @botname from command
      const cleanCmd = cmd.split("@")[0];
      const args = spaceIdx === -1 ? "" : text.slice(spaceIdx + 1).trim();
      // Reconstruct clean command text
      const cleanText = args ? `/${cleanCmd} ${args}` : `/${cleanCmd}`;

      try {
        const sessionId = await getOrCreateSession(chatId);
        const t0 = Date.now();
        const response = await agent.prompt(sessionId, cleanText);
        const elapsed = Date.now() - t0;
        perfHistory.push(elapsed);
        if (perfHistory.length > PERF_MAX) perfHistory.shift();
        sendMessage(chatId, response);
      } catch (err: unknown) {
        log.error({ error: err, chatId }, "agent prompt failed");
        sendMessage(chatId, "⚠️ Agent unavailable. Try again shortly.");
      }
      return;
    }

    // Forward to agent
    try {
      const sessionId = await getOrCreateSession(chatId);
      const t0 = Date.now();
      const response = await agent.prompt(sessionId, text);
      const elapsed = Date.now() - t0;
      perfHistory.push(elapsed);
      if (perfHistory.length > PERF_MAX) perfHistory.shift();
      sendMessage(chatId, response);
    } catch (err: unknown) {
      log.error({ error: err, chatId }, "agent prompt failed");
      sendMessage(chatId, "⚠️ Agent unavailable. Try again shortly.");
    }
  }

  // --- Platform UX ---

  function sendMessage(chatId: string, text: string): void {
    const html = formatMarkdown(text);
    const segments = splitMessage(html);

    for (const segment of segments) {
      rateLimiter.acquire().then(() =>
        withRetry(async () => {
          await bot.api.sendMessage(Number(chatId), segment, {
            parse_mode: "HTML",
            link_preview_options: { is_disabled: true },
          });
        })
      ).catch((err) => {
        log.error({ chatId, error: (err as Error).message }, "sendMessage failed");
      });
    }
  }

  function ackMessage(chatId: string, messageId: number): void {
    withRetry(async () => {
      await bot.api.setMessageReaction(Number(chatId), messageId, [
        { type: "emoji", emoji: ackEmoji! },
      ]);
    }).catch((err) => {
      log.debug({ chatId, error: (err as Error).message }, "ack reaction failed");
    });
  }

  function sendTyping(chatId: string): void {
    bot.api.sendChatAction(Number(chatId), "typing").catch(() => {});
  }

  function registerBotCommands(): void {
    // Only agent-declared commands (bridge has no custom commands)
    const commands: Array<{ command: string; description: string }> = [];

    for (const agentCmd of agent.getAgentCommands()) {
      const sanitized = sanitizeCommandName(agentCmd.name);
      if (sanitized && sanitized.length <= 32) {
        commands.push({
          command: sanitized,
          description: agentCmd.description.slice(0, 256) || agentCmd.name,
        });
      }
    }

    if (commands.length === 0) return; // Don't register empty list

    bot.api.setMyCommands(commands).then(() => {
      log.info({ count: commands.length }, "registered bot commands");
    }).catch((err) => {
      log.warn({ error: err }, "failed to register bot commands");
    });
  }

  // --- Startup buffer ---

  function flushBuffer(): void {
    const staleThreshold = Date.now() - 30_000;
    for (const msg of buffer) {
      if (msg.timestamp < staleThreshold) {
        log.debug({ chatId: msg.chatId }, "discarding stale buffered message");
        continue;
      }
      processMessage(msg.chatId, msg.text, msg.messageId);
    }
    buffer.length = 0;
  }

  // --- Bot setup ---

  bot.catch((err) => {
    log.error({ error: err.message ?? err }, "bot error");
  });

  bot.on("message:text", async (ctx) => {
    const chatId = ctx.chat.id.toString();
    const username = ctx.from?.username ? `@${ctx.from.username}` : null;
    const text = ctx.message.text;
    const messageId = ctx.message.message_id;
    const isGroup = ctx.chat.type === "group" || ctx.chat.type === "supergroup";

    // ACL checks
    const groupAcl = acl.groups?.[chatId];
    const allowedUsers = groupAcl?.allowed_users ?? acl.allowed_users;
    const requireMention = groupAcl?.require_mention ?? acl.require_mention ?? false;

    if (allowedUsers?.length && username) {
      if (!allowedUsers.includes(username)) {
        log.debug({ username, chatId }, "ignoring unauthorized user");
        return;
      }
    }

    // Mention check for groups
    if (isGroup && requireMention && botUsername) {
      if (!text.includes(`@${botUsername}`)) {
        return;
      }
    }

    // Strip @botname from message text
    const normalized = text.startsWith("/")
      ? text
      : (botUsername ? text.replace(new RegExp(`@${botUsername}\\b`, "g"), "").trim() : text);

    if (!ready) {
      buffer.push({ chatId, text: normalized, messageId, timestamp: Date.now() });
      return;
    }

    processMessage(chatId, normalized, messageId);
  });

  // Re-register bot commands when agent declares its commands
  agent.onCommandsUpdate(() => {
    log.info("agent commands updated, re-registering bot menu");
    registerBotCommands();
  });

  // --- Channel interface ---

  return {
    async start(): Promise<void> {
      log.info("starting bot (long polling)");
      bot.start({
        onStart: (info) => {
          botUsername = info.username;
          log.info({ username: info.username }, "bot connected");
          ready = true;
          flushBuffer();
          registerBotCommands();
        },
      });
    },

    stop(): void {
      bot.stop();
    },
  };
}
