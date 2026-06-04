/**
 * Session manager — manages ACP session lifecycle for chat-to-session mapping.
 * Uses LRU eviction to prevent unbounded memory growth in long-running bots.
 */
import type { AcpAgent } from "../acp-client.js";
import { createLogger } from "../logger.js";

const log = createLogger("telegram:sessions");

const DEFAULT_MAX_SESSIONS = 1000;

export class SessionManager {
  private sessions = new Map<number, string>();
  private agent: AcpAgent;
  private readonly cwd: string;
  private readonly maxSessions: number;

  constructor(agent: AcpAgent, cwd: string, maxSessions = DEFAULT_MAX_SESSIONS) {
    this.agent = agent;
    this.cwd = cwd;
    this.maxSessions = maxSessions;
  }

  /** Get existing session or create a new one for the given chat. */
  async getOrCreate(chatId: number): Promise<string> {
    const existing = this.sessions.get(chatId);
    if (existing) {
      // Refresh LRU position
      this.sessions.delete(chatId);
      this.sessions.set(chatId, existing);
      return existing;
    }

    const conn = this.agent.getConnection();
    if (!conn) throw new Error("Agent not connected");

    const result = await conn.newSession({ cwd: this.cwd, mcpServers: [] });
    const sessionId = result.sessionId;
    this.sessions.set(chatId, sessionId);
    this.evict();
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

  /** Number of active sessions (for testing/diagnostics). */
  get size(): number {
    return this.sessions.size;
  }

  /** Evict oldest sessions when map exceeds maxSessions. */
  private evict(): void {
    while (this.sessions.size > this.maxSessions) {
      const oldest = this.sessions.keys().next().value;
      if (oldest !== undefined) {
        log.debug({ chatId: oldest }, "evicting stale session");
        this.sessions.delete(oldest);
      }
    }
  }
}
