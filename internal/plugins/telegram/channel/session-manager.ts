/**
 * Session manager — manages ACP session lifecycle for chat-to-session mapping.
 * Handles creation, lookup, and potential future TTL/eviction.
 */
import type { AcpAgent } from "../acp-client.js";
import { createLogger } from "../logger.js";

const log = createLogger("telegram:sessions");

export class SessionManager {
  private sessions = new Map<number, string>();
  private agent: AcpAgent;

  constructor(agent: AcpAgent) {
    this.agent = agent;
  }

  /** Get existing session or create a new one for the given chat. */
  async getOrCreate(chatId: number): Promise<string> {
    const existing = this.sessions.get(chatId);
    if (existing) return existing;

    const conn = this.agent.getConnection();
    if (!conn) throw new Error("Agent not connected");

    const result = await conn.newSession({ cwd: "/workspace", mcpServers: [] });
    const sessionId = result.sessionId;
    this.sessions.set(chatId, sessionId);
    log.info({ chatId, sessionId: sessionId.slice(0, 8) }, "created session");
    return sessionId;
  }

  /** Remove a session (e.g., on reset). */
  delete(chatId: number): void {
    this.sessions.delete(chatId);
  }

  /** Check if a session exists for a chat. */
  has(chatId: number): boolean {
    return this.sessions.has(chatId);
  }
}
