import type { BridgeExtension, ExtensionContext, ChatId, AgentEvent } from "../extension.js";
import { appendFileSync, statSync, truncateSync } from "node:fs";

const LOG_PATH = "/var/log/bridge/events.jsonl";
const MAX_BYTES = 10 * 1024 * 1024; // 10MB

function emit(data: Record<string, unknown>): void {
  const line = JSON.stringify({ ts: Date.now(), ...data }) + "\n";
  try {
    try {
      const stat = statSync(LOG_PATH);
      if (stat.size > MAX_BYTES) truncateSync(LOG_PATH, 0);
    } catch { /* file doesn't exist yet */ }
    appendFileSync(LOG_PATH, line);
  } catch { /* ignore write errors */ }
}

const eventLoggerPlugin: BridgeExtension = {
  name: "event-logger",
  onTurnStart(_ctx: ExtensionContext, chatId: ChatId): void {
    emit({ event: "turn_start", chatId });
  },
  onTurnEnd(_ctx: ExtensionContext, chatId: ChatId): void {
    emit({ event: "turn_end", chatId });
  },
  onEvent(_ctx: ExtensionContext, chatId: ChatId, event: AgentEvent): void {
    emit({ event: event.type, chatId, ...event });
  },
};

export default eventLoggerPlugin;
