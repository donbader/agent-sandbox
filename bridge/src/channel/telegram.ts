import { Bot } from "grammy";
import type { Channel } from "./types.js";

// The bridge uses a dummy token. The gateway MITM rewrites it to the real token.
const DUMMY_TOKEN = "000000000:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

/**
 * TelegramChannel implements Channel using grammy.
 * It connects to api.telegram.org through the gateway MITM proxy,
 * which replaces the dummy token with the real bot token.
 */
export class TelegramChannel implements Channel {
  private bot: Bot;
  private handler: ((chatId: string, text: string) => void) | null = null;
  private allowedChatIds: Set<string> | null;

  constructor(allowedChatIds?: string[]) {
    this.allowedChatIds = allowedChatIds?.length
      ? new Set(allowedChatIds)
      : null;

    this.bot = new Bot(DUMMY_TOKEN);

    this.bot.on("message:text", (ctx) => {
      const chatId = ctx.chat.id.toString();

      // Filter by allowed chat IDs if configured
      if (this.allowedChatIds && !this.allowedChatIds.has(chatId)) {
        console.log(`telegram: ignoring message from unauthorized chat ${chatId}`);
        return;
      }

      const text = ctx.message.text;
      console.log(`telegram: received message from chat ${chatId}`);

      if (this.handler) {
        this.handler(chatId, text);
      }
    });
  }

  async start(): Promise<void> {
    console.log("telegram: starting bot (long polling)");
    // grammy's bot.start() blocks, so we don't await it
    this.bot.start({
      onStart: () => console.log("telegram: bot started"),
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
      console.error(`telegram: failed to send message to ${chatId}:`, err);
    });
  }
}
