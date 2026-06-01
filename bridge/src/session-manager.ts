import type * as acp from "@agentclientprotocol/sdk";
import { createLogger } from "./logger.js";
import type { SessionStore, ChatId } from "./session-store.js";

export interface SessionManagerConfig {
  /** Getter for the current connection (survives auto-restart). */
  getConnection: () => acp.ClientSideConnection;
  cwd: string;
  store: SessionStore;
}

export class SessionManager {
  private sessions = new Map<string, string>();
  private log = createLogger("session-manager");
  private config: SessionManagerConfig;

  constructor(config: SessionManagerConfig) {
    this.config = config;
  }

  async getSession(chatId: ChatId): Promise<string> {
    // Check in-memory cache
    const cached = this.sessions.get(chatId);
    if (cached) return cached;

    // Try to resume persisted session
    const persisted = this.config.store.getSessionId(chatId);
    if (persisted) {
      const conn = this.config.getConnection();
      const loadSession = (conn as unknown as Record<string, unknown>)["loadSession"];
      if (typeof loadSession === "function") {
        try {
          await (loadSession as (p: { sessionId: string }) => Promise<unknown>).call(conn, { sessionId: persisted });
          this.sessions.set(chatId, persisted);
          this.config.store.touchSession(chatId, persisted);
          this.log.info({ chatId, sessionId: persisted }, "resumed session");
          return persisted;
        } catch {
          this.log.warn({ chatId, sessionId: persisted }, "loadSession failed, creating new");
        }
      } else {
        this.log.debug({ chatId }, "agent does not support loadSession, creating new");
      }
    }

    return this.createSession(chatId);
  }

  async createSession(chatId: ChatId): Promise<string> {
    const conn = this.config.getConnection();
    const { sessionId } = await conn.newSession({
      cwd: this.config.cwd,
      mcpServers: [],
    });
    this.sessions.set(chatId, sessionId);
    this.config.store.setSessionId(chatId, sessionId);
    this.config.store.addToHistory(chatId, sessionId);
    this.log.info({ chatId, sessionId }, "created new session");
    return sessionId;
  }

  async resetSession(chatId: ChatId): Promise<string> {
    this.sessions.delete(chatId);
    this.config.store.deleteSessionId(chatId);
    return this.createSession(chatId);
  }

  hasSession(chatId: ChatId): boolean {
    return this.sessions.has(chatId);
  }

  getSessionId(chatId: ChatId): string | undefined {
    return this.sessions.get(chatId);
  }
}
