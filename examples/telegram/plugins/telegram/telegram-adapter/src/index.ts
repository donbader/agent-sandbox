/**
 * Telegram Adapter — ACP client that bridges Telegram ↔ agent-manager-acp.
 *
 * Connects to agent-manager via ACP over WebSocket, receives Telegram messages
 * via grammY, and forwards them as ACP prompts.
 */
import { Bot, Context } from "grammy";
import WebSocket from "ws";
import pino from "pino";

const log = pino({ name: "telegram-adapter" });

const AGENT_MANAGER_URL = process.env.AGENT_MANAGER_URL ?? "ws://agent:3100/acp";
const BOT_TOKEN = process.env.TELEGRAM_BOT_TOKEN;
const ALLOWED_USERS = (process.env.ALLOWED_USERS ?? "").split(",").filter(Boolean);
const RECONNECT_DELAY_MS = 3000;
const MAX_TELEGRAM_MSG_LENGTH = 4096;
const RETRY_ATTEMPTS = 3;
const RETRY_BASE_DELAY_MS = 1000;

if (!BOT_TOKEN) {
  log.fatal("TELEGRAM_BOT_TOKEN is required");
  process.exit(1);
}

interface JsonRpcMessage {
  jsonrpc: "2.0";
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: unknown;
  error?: { code: number; message: string };
}

interface SessionUpdate {
  sessionId: string;
  update: {
    sessionUpdate: string;
    content?: { type: string; text?: string };
  };
}

class TelegramAdapter {
  private bot: Bot;
  private ws: WebSocket | null = null;
  private nextId = 1;
  private pendingRequests = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  private sessionMap = new Map<number, string>(); // chatId → sessionId
  private activeChatId: number | null = null;
  private messageBuffer = "";
  private flushTimer: NodeJS.Timeout | null = null;
  private connected = false;

  constructor(token: string) {
    this.bot = new Bot(token);
  }

  async start(): Promise<void> {
    await this.connectAcp();
    await this.acpInitialize();

    this.bot.catch((err) => {
      log.error({ err: err.error }, "bot error");
    });

    this.bot.on("message:text", async (ctx) => {
      if (!this.isAllowed(ctx)) return;
      const text = ctx.message.text;
      const chatId = ctx.chat.id;
      log.info({ chatId, text: text.slice(0, 50) }, "received message");
      this.activeChatId = chatId;

      try { await ctx.react("👀"); } catch { /* ignore */ }

      try {
        await this.withRetry(async () => {
          let sessionId = this.sessionMap.get(chatId);
          if (!sessionId) {
            try {
              sessionId = await this.acpNewSession();
            } catch (err) {
              this.sessionMap.delete(chatId);
              throw err;
            }
            this.sessionMap.set(chatId, sessionId);
          }
          await this.acpPrompt(sessionId, text);
        });
      } catch (err) {
        log.error({ err, chatId }, "all retries exhausted");
        try {
          await ctx.reply("Sorry, the agent is temporarily unavailable. Please try again.");
        } catch (replyErr) {
          log.error({ err: replyErr, chatId }, "failed to send error reply");
        }
      }
    });

    await this.bot.start();
    log.info("telegram adapter started");
  }

  private isAllowed(ctx: Context): boolean {
    if (ALLOWED_USERS.length === 0) return true;
    const username = ctx.from?.username;
    if (!username) return false;
    return ALLOWED_USERS.includes(`@${username}`) || ALLOWED_USERS.includes(username);
  }

  private async withRetry<T>(fn: () => Promise<T>, retries = RETRY_ATTEMPTS): Promise<T> {
    let lastErr: unknown;
    for (let attempt = 0; attempt < retries; attempt++) {
      try {
        return await fn();
      } catch (err) {
        lastErr = err;
        if (attempt < retries - 1) {
          const delay = RETRY_BASE_DELAY_MS * Math.pow(2, attempt);
          log.warn({ attempt: attempt + 1, retries, delay }, "retrying after failure");
          await new Promise((r) => setTimeout(r, delay));
        }
      }
    }
    throw lastErr;
  }

  private async connectAcp(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.ws = new WebSocket(AGENT_MANAGER_URL);
      this.ws.on("open", () => { log.info({ url: AGENT_MANAGER_URL }, "connected"); this.connected = true; resolve(); });
      this.ws.on("error", (err) => { log.error({ err }, "WS error"); if (!this.connected) reject(err); });
      this.ws.on("message", (data) => { try { this.handleAcpMessage(JSON.parse(data.toString())); } catch {} });
      this.ws.on("close", () => { this.connected = false; setTimeout(() => this.reconnect(), RECONNECT_DELAY_MS); });
    });
  }

  private async reconnect(): Promise<void> {
    try { await this.connectAcp(); await this.acpInitialize(); log.info("reconnected"); }
    catch { setTimeout(() => this.reconnect(), RECONNECT_DELAY_MS); }
  }

  private handleAcpMessage(msg: JsonRpcMessage): void {
    if (msg.id && this.pendingRequests.has(msg.id)) {
      const p = this.pendingRequests.get(msg.id)!;
      this.pendingRequests.delete(msg.id);
      msg.error ? p.reject(new Error(msg.error.message)) : p.resolve(msg.result);
      return;
    }
    if (msg.method === "session/update") {
      this.handleSessionUpdate(msg.params as unknown as SessionUpdate);
    }
  }

  private handleSessionUpdate(params: SessionUpdate): void {
    const { update } = params;
    if (update.sessionUpdate === "agent_message_chunk" && update.content?.text) {
      this.messageBuffer += update.content.text;
      this.scheduleFlush();
    }
  }

  private scheduleFlush(): void {
    if (this.flushTimer) return;
    this.flushTimer = setTimeout(() => this.flushMessage(), 500);
  }

  private async flushMessage(): Promise<void> {
    this.flushTimer = null;
    if (!this.messageBuffer || !this.activeChatId) return;
    const text = this.messageBuffer;
    this.messageBuffer = "";
    const chunks = text.length <= MAX_TELEGRAM_MSG_LENGTH ? [text] : [];
    if (chunks.length === 0) {
      let r = text;
      while (r.length > 0) { chunks.push(r.slice(0, MAX_TELEGRAM_MSG_LENGTH)); r = r.slice(MAX_TELEGRAM_MSG_LENGTH); }
    }
    for (const chunk of chunks) {
      try { await this.bot.api.sendMessage(this.activeChatId, chunk, { parse_mode: "Markdown" }); }
      catch { try { await this.bot.api.sendMessage(this.activeChatId!, chunk); } catch (e) { log.error({ err: e }, "send failed"); } }
    }
  }

  private async acpSend(method: string, params: Record<string, unknown> = {}): Promise<unknown> {
    const id = this.nextId++;
    const msg: JsonRpcMessage = { jsonrpc: "2.0", id, method, params };
    return new Promise((resolve, reject) => { this.pendingRequests.set(id, { resolve, reject }); this.ws!.send(JSON.stringify(msg)); });
  }

  private async acpInitialize(): Promise<void> {
    const result = await this.acpSend("initialize", { protocolVersion: 1, clientCapabilities: {} });
    log.info({ result }, "ACP initialized");
  }

  private async acpNewSession(): Promise<string> {
    const result = await this.acpSend("session/new", {}) as { sessionId: string };
    return result.sessionId;
  }

  private async acpPrompt(sessionId: string, text: string): Promise<void> {
    await this.acpSend("session/prompt", { sessionId, prompt: [{ type: "text", text }] });
    await this.flushMessage();
  }

  stop(): void { this.bot.stop(); this.ws?.close(); }
}

const adapter = new TelegramAdapter(BOT_TOKEN);
adapter.start().catch((err) => { log.fatal({ error: err }, "fatal"); process.exit(1); });
process.on("SIGTERM", () => { adapter.stop(); process.exit(0); });
