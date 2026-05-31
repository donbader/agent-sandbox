import { Bot } from "grammy";
import type { Channel } from "./types.js";
import { createLogger } from "../logger.js";

const log = createLogger("telegram");

// The bridge uses a dummy token. The gateway MITM rewrites it to the real token.
const DUMMY_TOKEN = "000000000:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

/** Access control configuration passed via BridgeConfig */
interface AccessControl {
  allowed_users?: string[];
  require_mention?: boolean;
  groups?: Record<string, { allowed_users?: string[]; require_mention?: boolean }>;
}

/**
 * TelegramChannel implements Channel using grammy.
 * It connects to api.telegram.org through the gateway MITM proxy,
 * which replaces the dummy token with the real bot token.
 *
 * Protocol: export default a class implementing Channel.
 * Constructor receives the plugin's BridgeConfig.
 */
export default class TelegramChannel implements Channel {
  private bot: Bot;
  private handler: ((chatId: string, text: string) => void) | null = null;
  private acl: AccessControl;
  private botUsername: string | null = null;

  constructor(config: Record<string, unknown>) {
    this.acl = (config?.access_control as AccessControl) ?? {};
    this.bot = new Bot(DUMMY_TOKEN);

    // Catch all bot errors (DNS failures, network errors, API errors)
    this.bot.catch((err) => {
      log.error({ error: err.message ?? err }, "bot error");
    });

    this.bot.on("message:text", (ctx) => {
      const chatId = ctx.chat.id.toString();
      const username = ctx.from?.username ? `@${ctx.from.username}` : null;
      const text = ctx.message.text;
      const isGroup = ctx.chat.type === "group" || ctx.chat.type === "supergroup";

      // Resolve effective ACL (per-group overrides > top-level)
      const groupAcl = this.acl.groups?.[chatId];
      const allowedUsers = groupAcl?.allowed_users ?? this.acl.allowed_users;
      const requireMention = groupAcl?.require_mention ?? this.acl.require_mention ?? false;

      // Check allowed users
      if (allowedUsers?.length && username) {
        if (!allowedUsers.includes(username)) {
          log.debug({ username, chatId }, "ignoring unauthorized user");
          return;
        }
      } else if (allowedUsers?.length && !username) {
        log.debug({ chatId }, "ignoring user without username");
        return;
      }

      // Check require_mention in group chats
      if (isGroup && requireMention) {
        const mentioned = this.botUsername
          ? text.includes(`@${this.botUsername}`)
          : false;
        if (!mentioned) {
          return; // silently ignore non-mentioned messages in groups
        }
      }

      log.info({ username: username ?? "unknown", chatId }, "received message");

      if (this.handler) {
        this.handler(chatId, text);
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
    this.bot.api.sendMessage(Number(chatId), text).catch((err) => {
      log.error({ chatId, error: err.message ?? err }, "failed to send message");
    });
  }
}
