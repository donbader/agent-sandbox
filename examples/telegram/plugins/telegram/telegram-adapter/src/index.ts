/**
 * Telegram Adapter — ACP client that bridges Telegram ↔ agent-manager-acp.
 *
 * Connects to agent-manager via ACP over WebSocket, receives Telegram messages
 * via grammY, and forwards them as ACP prompts.
 */
import { Bot } from "grammy";
import WebSocket from "ws";
import pino from "pino";

const log = pino({ name: "telegram-adapter" });

const AGENT_MANAGER_URL = process.env.AGENT_MANAGER_URL ?? "ws://agent:3100/acp";
const BOT_TOKEN = process.env.TELEGRAM_BOT_TOKEN;

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

class TelegramAdapter {
  private bot: Bot;
  private ws: WebSocket | null = null;
  private nextId = 1;
  private pendingRequests = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  private sessionId: string | null = null;

  constructor(token: string) {
    this.bot = new Bot(token);
  }

  async start(): Promise<void> {
    // Connect to agent-manager via ACP WebSocket
    await this.connectAcp();

    // Initialize ACP connection
    await this.acpInitialize();

    // Set up Telegram bot handlers
    this.bot.on("message:text", async (ctx) => {
      const text = ctx.message.text;
      const chatId = ctx.chat.id;

      log.info({ chatId, text: text.slice(0, 50) }, "received telegram message");

      // Ensure we have a session
      if (!this.sessionId) {
        this.sessionId = await this.acpNewSession();
      }

      // Send prompt via ACP
      await this.acpPrompt(this.sessionId, text);
    });

    // Listen for ACP notifications (agent responses)
    // These come back over the WebSocket and we forward to Telegram
    this.setupNotificationHandler();

    await this.bot.start();
    log.info("telegram adapter started");
  }

  private async connectAcp(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.ws = new WebSocket(AGENT_MANAGER_URL);
      this.ws.on("open", () => {
        log.info({ url: AGENT_MANAGER_URL }, "connected to agent-manager");
        resolve();
      });
      this.ws.on("error", (err) => {
        log.error({ err }, "WebSocket error");
        reject(err);
      });
      this.ws.on("message", (data) => {
        this.handleAcpMessage(JSON.parse(data.toString()));
      });
      this.ws.on("close", () => {
        log.warn("WebSocket closed — agent-manager disconnected");
        // TODO: reconnect logic
      });
    });
  }

  private handleAcpMessage(msg: JsonRpcMessage): void {
    // Response to a request we sent
    if (msg.id && this.pendingRequests.has(msg.id)) {
      const pending = this.pendingRequests.get(msg.id)!;
      this.pendingRequests.delete(msg.id);
      if (msg.error) {
        pending.reject(new Error(msg.error.message));
      } else {
        pending.resolve(msg.result);
      }
      return;
    }

    // Notification from agent (session/update)
    if (msg.method === "session/update") {
      this.handleSessionUpdate(msg.params as Record<string, unknown>);
    }
  }

  private handleSessionUpdate(_params: Record<string, unknown>): void {
    // TODO: Parse session update, extract agent message chunks, send to Telegram
    log.debug({ params: _params }, "session update notification");
  }

  private setupNotificationHandler(): void {
    // Notifications are handled in handleAcpMessage above
  }

  private async acpSend(method: string, params: Record<string, unknown> = {}): Promise<unknown> {
    const id = this.nextId++;
    const msg: JsonRpcMessage = { jsonrpc: "2.0", id, method, params };

    return new Promise((resolve, reject) => {
      this.pendingRequests.set(id, { resolve, reject });
      this.ws!.send(JSON.stringify(msg));
    });
  }

  private async acpInitialize(): Promise<void> {
    const result = await this.acpSend("initialize", {
      protocolVersion: "1",
      clientCapabilities: {},
    });
    log.info({ result }, "ACP initialized");
  }

  private async acpNewSession(): Promise<string> {
    const result = await this.acpSend("session/new", {
      cwd: "/home/agent/workspace",
    }) as { sessionId: string };
    log.info({ sessionId: result.sessionId }, "session created");
    return result.sessionId;
  }

  private async acpPrompt(sessionId: string, text: string): Promise<void> {
    await this.acpSend("session/prompt", {
      sessionId,
      prompt: [{ type: "text", text }],
    });
  }

  stop(): void {
    this.bot.stop();
    this.ws?.close();
  }
}

const adapter = new TelegramAdapter(BOT_TOKEN);

adapter.start().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});

process.on("SIGTERM", () => {
  adapter.stop();
  process.exit(0);
});
