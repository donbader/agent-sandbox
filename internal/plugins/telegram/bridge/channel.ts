import { Bot } from "grammy";
import type { Channel } from "./types.js";
import { createLogger } from "../logger.js";
import { RateLimiter } from "./delivery/rate-limiter.js";
import { withRetry } from "./delivery/api-retry.js";
import { formatMarkdown, splitMessage } from "./formatter/telegram.js";

const log = createLogger("telegram");
const DUMMY_TOKEN = "000000000:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

interface AccessControl {
  allowed_users?: string[];
  require_mention?: boolean;
  groups?: Record<string, { allowed_users?: string[]; require_mention?: boolean }>;
}

export default class TelegramChannel implements Channel {
  private bot: Bot;
  private handler: ((chatId: string, text: string) => void) | null = null;
  private acl: AccessControl;
  private botUsername: string | null = null;
  private rateLimiter = new RateLimiter(100);

  constructor(config: Record<string, unknown>) {
    this.acl = (config?.access_control as AccessControl) ?? {};
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

      // Ack emoji — react with 👀 to show we received the message
      this.ackMessage(chatId, ctx.message.message_id);

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
        { type: "emoji", emoji: "👀" },
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
