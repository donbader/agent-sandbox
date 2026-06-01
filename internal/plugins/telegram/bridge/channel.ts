import { Bot } from "grammy";
import type { ReactionTypeEmoji } from "@grammyjs/types";
import type { Channel, CommandDef } from "./types.js";
import { createLogger } from "../logger.js";
import { RateLimiter } from "./delivery/rate-limiter.js";
import { withRetry } from "./delivery/api-retry.js";
import { formatMarkdown, splitMessage } from "./formatter/telegram.js";

const log = createLogger("telegram");
const DUMMY_TOKEN = "000000000:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

type ReactionEmoji = ReactionTypeEmoji["emoji"];

/** Valid Telegram reaction emojis (from Bot API). */
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

export default class TelegramChannel implements Channel {
  private bot: Bot;
  private handler: ((chatId: string, text: string) => void) | null = null;
  private acl: AccessControl;
  private ackEmoji: ReactionEmoji | null;
  private botUsername: string | null = null;
  private rateLimiter = new RateLimiter(100);

  constructor(config: Record<string, unknown>) {
    this.acl = (config?.access_control as AccessControl) ?? {};
    const ackRaw = config?.ack_emoji;
    if (ackRaw === undefined) {
      this.ackEmoji = "👀";
    } else if (typeof ackRaw === "string" && isValidReactionEmoji(ackRaw)) {
      this.ackEmoji = ackRaw;
    } else if (!ackRaw) {
      this.ackEmoji = null; // explicitly disabled
    } else {
      log.warn({ ack_emoji: ackRaw }, "invalid ack_emoji, falling back to 👀");
      this.ackEmoji = "👀";
    }
    this.bot = new Bot(DUMMY_TOKEN);

    this.bot.catch((err) => {
      log.error({ error: err.message ?? err }, "bot error");
    });

    this.bot.on("message:text", async (ctx) => {
      const chatId = ctx.chat.id.toString();
      const username = ctx.from?.username ? `@${ctx.from.username}` : null;
      const text = ctx.message.text;
      const isGroup = ctx.chat.type === "group" || ctx.chat.type === "supergroup";

      // ACL checks (same as before)
      const groupAcl = this.acl.groups?.[chatId];
      const allowedUsers = groupAcl?.allowed_users ?? this.acl.allowed_users;
      const requireMention = groupAcl?.require_mention ?? this.acl.require_mention ?? false;

      if (allowedUsers?.length && username) {
        if (!allowedUsers.includes(username)) {
          log.debug({ username, chatId }, "ignoring unauthorized user");
          return;
        }
      } else if (allowedUsers?.length && !username) {
        log.debug({ chatId }, "ignoring user without username");
        return;
      }

      if (isGroup && requireMention) {
        const mentioned = this.botUsername
          ? text.includes(`@${this.botUsername}`)
          : false;
        if (!mentioned) return;
      }

      log.info({ username: username ?? "unknown", chatId }, "received message");

      // Ack emoji — react to show we received the message
      if (this.ackEmoji) {
        this.ackMessage(chatId, ctx.message.message_id);
      }

      // Typing indicator
      this.sendTyping(chatId);

      if (this.handler) {
        // Strip @botname from commands (Telegram group chat convention)
        const normalized = text.startsWith("/")
          ? text.replace(/^(\/\w+)@\S+/, "$1")
          : text;
        this.handler(chatId, normalized);
      }
    });
  }

  async start(): Promise<void> {
    log.info("starting bot (long polling)");
    this.bot.start({
      onStart: (info) => {
        this.botUsername = info.username;
        log.info({ username: info.username }, "bot connected");
      },
    });
  }

  stop(): void {
    this.bot.stop();
  }

  async registerCommands(commands: CommandDef[]): Promise<void> {
    const botCommands = commands
      .map(({ name, description }) => ({
        command: sanitizeCommandName(name),
        description: description.slice(0, 256),
      }))
      .filter(({ command }) => command.length > 0 && command.length <= 32);

    try {
      await this.bot.api.setMyCommands(botCommands);
      log.info({ count: botCommands.length }, "registered bot commands");
    } catch (err) {
      log.warn({ error: err }, "failed to register bot commands");
    }
  }

  onMessage(handler: (chatId: string, text: string) => void): void {
    this.handler = handler;
  }

  sendMessage(chatId: string, text: string): void {
    void this.sendFormatted(chatId, text);
  }

  // --- Private helpers ---

  private async sendFormatted(chatId: string, text: string): Promise<void> {
    const html = formatMarkdown(text);
    const segments = splitMessage(html);

    for (const segment of segments) {
      await withRetry(async () => {
        await this.rateLimiter.acquire(chatId);
        await this.bot.api.sendMessage(Number(chatId), segment, {
          parse_mode: "HTML",
        });
      });
    }
  }

  private ackMessage(chatId: string, messageId: number): void {
    withRetry(async () => {
      await this.bot.api.setMessageReaction(Number(chatId), messageId, [
        { type: "emoji", emoji: this.ackEmoji! },
      ]);
    }).catch((err) => {
      // Non-critical — don't fail if reaction fails (old API, permissions, etc.)
      log.debug({ chatId, error: (err as Error).message }, "ack reaction failed");
    });
  }

  private sendTyping(chatId: string): void {
    void this.bot.api.sendChatAction(Number(chatId), "typing").catch(() => {
      // Non-critical
    });
  }
}
