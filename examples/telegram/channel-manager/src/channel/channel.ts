import { Bot } from "grammy";
import type { Channel } from "./types.js";
import type { AcpAgent } from "../acp-client.js";
import type { SessionNotification, SessionUpdate } from "@agentclientprotocol/sdk";
import { createLogger } from "../logger.js";
import { StartupBuffer } from "../startup-buffer.js";
import { RateLimiter } from "./delivery/rate-limiter.js";
import { withRetry } from "./delivery/api-retry.js";
import { StreamController } from "./stream-controller.js";
import { SessionManager } from "./session-manager.js";
import { isAllowed } from "./acl.js";
import { parseConfig, type TelegramChannelConfig } from "./config.js";

const log = createLogger("telegram");
const DUMMY_TOKEN = "REDACTED_TELEGRAM_TOKEN";

interface BufferedMessage {
  chatId: number;
  text: string;
  messageId: number;
}

/** Sanitize a command name for Telegram (lowercase a-z, 0-9, underscore only). */
function sanitizeCommandName(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9_]/g, "_")
    .replace(/^_+|_+$/g, "");
}

/** Normalize a /command@botname into /command args */
function normalizeCommand(text: string): string {
  const spaceIdx = text.indexOf(" ");
  const cmd = spaceIdx === -1 ? text.slice(1) : text.slice(1, spaceIdx);
  const cleanCmd = cmd.split("@")[0];
  const args = spaceIdx === -1 ? "" : text.slice(spaceIdx + 1).trim();
  return args ? `/${cleanCmd} ${args}` : `/${cleanCmd}`;
}

/**
 * Telegram channel — connects to Telegram via grammY and routes messages
 * through ACP to the agent.
 */
export class TelegramChannel implements Channel {
  private bot: Bot;
  private agent: AcpAgent;
  private config: TelegramChannelConfig;
  private sessions: SessionManager;
  private rateLimiter = new RateLimiter(100);
  private startupBuffer = new StartupBuffer<BufferedMessage>();
  private botUsername: string | null = null;
  private chatQueues = new Map<number, Promise<void>>();

  constructor(rawConfig: Record<string, unknown>, agent: AcpAgent) {
    this.config = parseConfig(rawConfig);
    this.agent = agent;
    this.bot = new Bot(DUMMY_TOKEN);
    this.sessions = new SessionManager(agent, rawConfig.cwd as string);

    this.setupBot();
    this.setupCommandSync();
  }

  async start(): Promise<void> {
    log.info("starting bot (long polling)");
    this.bot.start({
      onStart: (info) => {
        this.botUsername = info.username;
        log.info({ username: info.username }, "bot connected");
        this.flushBuffer();
        this.registerBotCommands();
      },
    });
  }

  stop(): void {
    this.bot.stop();
  }

  // --- Bot setup ---

  private setupBot(): void {
    this.bot.catch((err) => {
      log.error({ error: err.message ?? err }, "bot error");
    });

    this.bot.on("message:text", async (ctx) => {
      const chatId = ctx.chat.id;
      const username = ctx.from?.username ? `@${ctx.from.username}` : null;
      const text = ctx.message.text;
      const messageId = ctx.message.message_id;
      const isGroup = ctx.chat.type === "group" || ctx.chat.type === "supergroup";

      // ACL check
      if (!isAllowed(
        { chatId, username, isGroup, text, botUsername: this.botUsername },
        this.config.accessControl,
      )) {
        return;
      }

      // Strip @botname from message text
      const normalized = text.startsWith("/")
        ? text
        : this.botUsername
          ? text.replace(new RegExp(`@${this.botUsername}\\b`, "g"), "").trim()
          : text;

      if (this.startupBuffer.push({ chatId, text: normalized, messageId })) {
        return;
      }

      this.processMessage(chatId, normalized, messageId);
    });
  }

  private setupCommandSync(): void {
    this.agent.onCommandsUpdate(() => {
      log.info("agent commands updated, re-registering bot menu");
      this.registerBotCommands();
    });
  }

  // --- Message handling ---

  private processMessage(chatId: number, text: string, messageId: number): void {
    const prev = this.chatQueues.get(chatId) ?? Promise.resolve();
    const next = prev.then(() => this.processMessageInner(chatId, text, messageId)).catch(() => {});
    this.chatQueues.set(chatId, next);
  }

  private async processMessageInner(chatId: number, text: string, messageId: number): Promise<void> {
    if (this.config.ackEmoji) {
      this.ackMessage(chatId, messageId);
    }

    this.sendTyping(chatId);

    const sessionId = await this.sessions.getOrCreate(chatId);
    const cleanText = text.startsWith("/") ? normalizeCommand(text) : text;

    const stream = new StreamController(
      {
        chatId,
        sendMessage: async (html, opts) => {
          await this.rateLimiter.acquire(chatId.toString());
          log.debug({ chatId, len: html.length }, "telegram:sendMessage");
          const msg = await withRetry(async () =>
            this.bot.api.sendMessage(chatId, html, {
              parse_mode: (opts?.parse_mode as "HTML") ?? "HTML",
              link_preview_options: { is_disabled: true },
            }),
          );
          return msg?.message_id ?? 0;
        },
        editMessage: async (msgId, html, opts) => {
          await this.rateLimiter.acquire(chatId.toString());
          log.debug({ chatId, msgId, len: html.length }, "telegram:editMessage");
          await withRetry(async () =>
            this.bot.api.editMessageText(chatId, msgId, html, {
              parse_mode: (opts?.parse_mode as "HTML") ?? "HTML",
              link_preview_options: { is_disabled: true },
            }),
          );
        },
        sendDraft: async (draftId, draftText) => {
          log.debug({ chatId, draftId, len: draftText.length }, "telegram:sendDraft");
          await withRetry(async () =>
            this.bot.api.sendMessageDraft(chatId, draftId, draftText, { parse_mode: "HTML" }),
          );
        },
      },
      { editIntervalMs: 2000, draftDebounceMs: 1000 },
    );

    try {
      await this.agent.prompt(sessionId, cleanText, {
        onSessionUpdate: (notification: SessionNotification) => {
          const { update } = notification;
          this.handleSessionUpdate(update, stream);
        },
      });
      await stream.finalize();
    } catch (err) {
      await stream.abort(err instanceof Error ? err : new Error(String(err)));
      log.error({ chatId, error: (err as Error).message }, "prompt failed");
    }
  }

  // --- ACP session update dispatch ---

  private handleSessionUpdate(update: SessionUpdate, stream: StreamController): void {
    switch (update.sessionUpdate) {
      case "agent_thought_chunk":
        if (update.content.type === "text") {
          log.debug({ len: update.content.text.length }, "agent_thought_chunk");
          stream.pushThinking(update.content.text);
        }
        break;
      case "tool_call":
        log.debug({ toolCallId: update.toolCallId, title: update.title, status: update.status }, "tool_call");
        stream.toolStart(update.toolCallId, update.title, update.status);
        break;
      case "tool_call_update":
        log.debug({ toolCallId: update.toolCallId, status: update.status }, "tool_call_update");
        stream.toolUpdate(update.toolCallId, update.status ?? "in_progress", update.content ?? undefined);
        break;
      case "agent_message_chunk":
        if (update.content.type === "text") {
          const text = update.content.text;
          if (/^\s*\S+\s+v\d+\.\d+\.\d+\s*\n---\s*\n?$/.test(text)) {
            log.debug({ text: text.trim() }, "filtered runtime banner");
            break;
          }
          log.debug({ len: text.length }, "agent_message_chunk");
          stream.pushText(text);
        }
        break;
      case "user_message_chunk":
      case "plan":
      case "available_commands_update":
      case "current_mode_update":
      case "config_option_update":
      case "session_info_update":
      case "usage_update":
        break;
      default: {
        const _exhaustive: never = update;
        log.warn({ sessionUpdate: (_exhaustive as SessionUpdate).sessionUpdate }, "unhandled session update type");
      }
    }
  }

  // --- Platform UX ---

  private ackMessage(chatId: number, messageId: number): void {
    this.bot.api.setMessageReaction(chatId, messageId, [
      { type: "emoji", emoji: this.config.ackEmoji! },
    ]).catch((err) => {
      log.debug({ chatId, error: (err as Error).message }, "ack reaction failed");
    });
  }

  private sendTyping(chatId: number): void {
    this.bot.api.sendChatAction(chatId, "typing").catch((err) => {
      log.debug({ chatId, error: (err as Error).message }, "typing indicator failed");
    });
  }

  private registerBotCommands(): void {
    const commands: Array<{ command: string; description: string }> = [];

    for (const agentCmd of this.agent.getAgentCommands()) {
      const sanitized = sanitizeCommandName(agentCmd.name);
      if (sanitized && sanitized.length <= 32) {
        commands.push({
          command: sanitized,
          description: agentCmd.description.slice(0, 256) || agentCmd.name,
        });
      }
    }

    if (commands.length === 0) return;

    this.bot.api
      .setMyCommands(commands)
      .then(() => {
        log.info({ count: commands.length }, "registered bot commands");
      })
      .catch((err) => {
        log.warn({ error: err }, "failed to register bot commands");
      });
  }

  // --- Startup buffer ---

  private flushBuffer(): void {
    for (const msg of this.startupBuffer.flush()) {
      this.processMessage(msg.chatId, msg.text, msg.messageId);
    }
  }
}

/**
 * Factory function for channel-manager plugin registry.
 */
export default function createTelegramChannel(
  config: Record<string, unknown>,
  agent: AcpAgent,
): Channel {
  return new TelegramChannel(config, agent);
}
