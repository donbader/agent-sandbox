import { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } from "node:fs";
import { join, dirname } from "node:path";
import { createLogger } from "./logger.js";

export type ChatId = string;

export interface SessionHistoryEntry {
  sessionId: string;
  createdAt: string;
  touchedAt: string;
  label?: string;
}

export interface SessionStoreOptions {
  dir?: string;
  maxHistory?: number;
}

const PERSIST_DEBOUNCE_MS = 1000;

export class SessionStore {
  private map: Record<string, string> = {};
  private history: Record<string, SessionHistoryEntry[]> = {};
  private historyDirty = false;
  private historyTimer: ReturnType<typeof setTimeout> | null = null;
  private readonly dir: string;
  private readonly maxHistory: number;
  private readonly mapPath: string;
  private readonly historyPath: string;
  private readonly log = createLogger("session-store");

  constructor(options?: SessionStoreOptions) {
    this.dir = options?.dir ?? "/var/lib/bridge/sessions";
    this.maxHistory = options?.maxHistory ?? 20;
    this.mapPath = join(this.dir, "session-map.json");
    this.historyPath = join(this.dir, "session-history.json");
    this.load();
  }

  getSessionId(chatId: ChatId): string | undefined { return this.map[chatId]; }

  setSessionId(chatId: ChatId, sessionId: string): void {
    this.map[chatId] = sessionId;
    this.persistMap();
  }

  deleteSessionId(chatId: ChatId): void {
    delete this.map[chatId];
    this.persistMap();
  }

  getAllActive(): Record<string, string> { return { ...this.map }; }

  addToHistory(chatId: ChatId, sessionId: string, label?: string): void {
    if (!this.history[chatId]) this.history[chatId] = [];
    if (this.history[chatId].some(e => e.sessionId === sessionId)) return;
    const now = new Date().toISOString();
    this.history[chatId].push({ sessionId, createdAt: now, touchedAt: now, label });
    if (this.history[chatId].length > this.maxHistory) {
      this.history[chatId].sort((a, b) => a.touchedAt.localeCompare(b.touchedAt));
      this.history[chatId] = this.history[chatId].slice(-this.maxHistory);
    }
    this.scheduleHistoryPersist();
  }

  getHistory(chatId: ChatId): SessionHistoryEntry[] {
    return this.history[chatId] ?? [];
  }

  touchSession(chatId: ChatId, sessionId: string): void {
    const entry = this.history[chatId]?.find(e => e.sessionId === sessionId);
    if (entry) {
      entry.touchedAt = new Date().toISOString();
      this.scheduleHistoryPersist();
    }
  }

  setLabel(chatId: ChatId, sessionId: string, label: string): void {
    const entry = this.history[chatId]?.find(e => e.sessionId === sessionId);
    if (entry) {
      entry.label = label;
      this.scheduleHistoryPersist();
    }
  }

  flushSync(): void {
    if (this.historyTimer) {
      clearTimeout(this.historyTimer);
      this.historyTimer = null;
    }
    if (this.historyDirty) this.writeHistory();
  }

  private load(): void {
    this.map = this.readJson(this.mapPath) ?? {};
    this.history = this.readJson(this.historyPath) ?? {};
  }

  private readJson<T>(filePath: string): T | null {
    try {
      if (existsSync(filePath)) {
        return JSON.parse(readFileSync(filePath, "utf-8")) as T;
      }
    } catch { /* corrupted — start fresh */ }
    return null;
  }

  private persistMap(): void {
    this.atomicWrite(this.mapPath, this.map);
  }

  private scheduleHistoryPersist(): void {
    this.historyDirty = true;
    if (this.historyTimer === null) {
      this.historyTimer = setTimeout(() => {
        this.historyTimer = null;
        this.writeHistory();
      }, PERSIST_DEBOUNCE_MS);
    }
  }

  private writeHistory(): void {
    if (!this.historyDirty) return;
    this.atomicWrite(this.historyPath, this.history);
    this.historyDirty = false;
  }

  private atomicWrite(filePath: string, data: unknown): void {
    const dir = dirname(filePath);
    if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
    const tmp = filePath + ".tmp";
    writeFileSync(tmp, JSON.stringify(data, null, 2), "utf-8");
    renameSync(tmp, filePath);
  }
}
