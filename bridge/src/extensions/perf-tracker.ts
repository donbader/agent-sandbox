import type { BridgeExtension, ExtensionContext, ChatId } from "../extension.js";

interface TurnTrace {
  chatId: string;
  startTime: number;
  endTime: number;
  duration: number;
}

const RING_SIZE = 50;
const ring: TurnTrace[] = [];
const activeTraces = new Map<string, number>();

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

const perfPlugin: BridgeExtension = {
  name: "perf-tracker",
  commands: {
    perf: {
      description: "Show performance stats",
      async handler(_ctx: ExtensionContext, chatId: ChatId, args: string) {
        const n = parseInt(args) || 5;
        const traces = ring.filter(t => t.chatId === chatId).slice(-n);
        if (traces.length === 0) return "No performance data yet.";

        const lines = ["📊 Performance (recent turns):", ""];
        for (const t of traces) {
          lines.push(`• ${formatDuration(t.duration)}`);
        }
        if (traces.length > 1) {
          const avg = Math.round(traces.reduce((s, t) => s + t.duration, 0) / traces.length);
          lines.push("", `Avg: ${formatDuration(avg)}`);
        }
        return lines.join("\n");
      },
    },
  },
  onTurnStart(_ctx: ExtensionContext, chatId: ChatId): void {
    activeTraces.set(chatId, Date.now());
  },
  onTurnEnd(_ctx: ExtensionContext, chatId: ChatId): void {
    const start = activeTraces.get(chatId);
    if (start) {
      const endTime = Date.now();
      const trace: TurnTrace = { chatId, startTime: start, endTime, duration: endTime - start };
      ring.push(trace);
      if (ring.length > RING_SIZE) ring.shift();
      activeTraces.delete(chatId);
    }
  },
};

export default perfPlugin;
