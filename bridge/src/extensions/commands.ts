import type { BridgeExtension, ExtensionContext, ChatId } from "../extension.js";
import { createLogger } from "../logger.js";

const log = createLogger("commands");
const PAGE_SIZE = 10;

const commandsExtension: BridgeExtension = {
  name: "commands",
  commands: {
    new: {
      description: "Start a new conversation session",
      async handler(ctx: ExtensionContext, chatId: ChatId, _args: string) {
        try {
          await ctx.sessions.resetSession(chatId);
          return "✨ New session started.";
        } catch (err) {
          log.error({ error: err }, "/new failed");
          return "❌ Failed to reset session.";
        }
      },
    },

    stop: {
      description: "Stop the current operation",
      async handler(ctx: ExtensionContext, _chatId: ChatId, _args: string) {
        ctx.agent.abort();
        return "⏹ Stopped.";
      },
    },

    resume: {
      description: "Resume a previous session (usage: /resume [N|id])",
      async handler(ctx: ExtensionContext, chatId: ChatId, args: string) {
        const arg = args.trim();

        // No args — show session list
        if (!arg) {
          return showSessions(ctx, chatId, 1);
        }

        // Pagination: /resume --page N
        const pageMatch = arg.match(/^--page\s+(\d+)$/);
        if (pageMatch) {
          return showSessions(ctx, chatId, Math.max(1, Number(pageMatch[1])));
        }

        const history = ctx.sessions.getHistory(chatId);

        // Try numeric index first (e.g. "/resume 2")
        const num = Number(arg);
        if (Number.isInteger(num) && num >= 1 && num <= history.length) {
          const entry = history[num - 1];
          try {
            await ctx.sessions.resumeSession(chatId, entry.sessionId);
            return `Resumed session \`${entry.sessionId.slice(0, 8)}\`. Send a message to continue.`;
          } catch (err) {
            log.error({ error: err }, "/resume failed");
            return "❌ Failed to resume session.";
          }
        }

        // Otherwise treat as session ID prefix
        const entry = ctx.sessions.findByPrefix(chatId, arg);

        if (!entry) {
          const matches = history.filter(e => e.sessionId.startsWith(arg));
          if (matches.length > 1) {
            const ids = matches.map(e => `\`${e.sessionId.slice(0, 8)}\``).join(", ");
            return `Ambiguous prefix "${arg}" — matches: ${ids}\nUse the full 8-char ID or list number.`;
          }
          return `No session found matching "${arg}".\nUse /resume to see available sessions.`;
        }

        try {
          await ctx.sessions.resumeSession(chatId, entry.sessionId);
          return `Resumed session \`${entry.sessionId.slice(0, 8)}\`. Send a message to continue.`;
        } catch (err) {
          log.error({ error: err }, "/resume failed");
          return "❌ Failed to resume session.";
        }
      },
    },

    label: {
      description: "Label current session (usage: /label <name>)",
      async handler(ctx: ExtensionContext, chatId: ChatId, args: string) {
        if (!args.trim()) return "Usage: /label <name>";

        const sessionId = ctx.sessions.getActiveSessionId(chatId);
        if (!sessionId) return "No active session to label.";

        ctx.sessions.labelSession(chatId, sessionId, args.trim());
        return `✓ Session labeled: ${args.trim()}`;
      },
    },

    version: {
      description: "Show bridge version info",
      async handler(_ctx: ExtensionContext, _chatId: ChatId, _args: string) {
        const fs = await import("node:fs");
        let version = "unknown";
        try {
          version = fs.readFileSync("/opt/bridge-version", "utf-8").trim();
        } catch { /* ignore */ }
        return `🏗 bridge\n  version: ${version}`;
      },
    },

    sh: {
      description: "Execute a shell command (usage: /sh <command>)",
      async handler(_ctx: ExtensionContext, _chatId: ChatId, args: string) {
        if (!args.trim()) return "Usage: /sh <command>";

        const { execSync } = await import("node:child_process");
        try {
          const output = execSync(args.trim(), {
            timeout: 30_000,
            maxBuffer: 1024 * 1024,
            encoding: "utf-8",
          });
          const trimmed = output.trim().slice(0, 4000);
          return trimmed || "(no output)";
        } catch (err: unknown) {
          const e = err as { status?: number; stdout?: string; stderr?: string };
          const output = (e.stdout || "") + (e.stderr || "");
          return `Exit ${e.status ?? "?"}:\n${output.trim().slice(0, 4000)}`;
        }
      },
    },

    diagnose: {
      description: "Show diagnostic info for debugging",
      async handler(ctx: ExtensionContext, chatId: ChatId, _args: string) {
        const lines: string[] = ["🔍 Diagnostics:"];
        lines.push(`  Chat ID: ${chatId}`);
        lines.push(`  Agent ready: ${ctx.agent.isReady()}`);

        const sessionId = ctx.sessions.getActiveSessionId(chatId);
        lines.push(`  Session: ${sessionId ? sessionId.slice(0, 8) + "…" : "none"}`);

        const history = ctx.sessions.getHistory(chatId);
        lines.push(`  History: ${history.length} sessions`);

        return lines.join("\n");
      },
    },
  },
};

// --- Helpers ---

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function showSessions(ctx: ExtensionContext, chatId: ChatId, page: number): string {
  const history = ctx.sessions.getHistory(chatId);
  const currentId = ctx.sessions.getActiveSessionId(chatId);

  if (history.length === 0) {
    return "No session history yet. Use /new to create sessions.";
  }

  const totalPages = Math.ceil(history.length / PAGE_SIZE);
  const safePage = Math.min(page, totalPages);

  const endIdx = history.length - (safePage - 1) * PAGE_SIZE;
  const startIdx = Math.max(0, endIdx - PAGE_SIZE);
  const shown = history.slice(startIdx, endIdx);

  const lines: string[] = ["📋 Sessions:\n"];

  if (totalPages > 1) {
    lines.push(`_Page ${safePage}/${totalPages}_\n`);
  }

  for (let i = 0; i < shown.length; i++) {
    const entry = shown[i];
    const globalIdx = startIdx + i + 1;
    const isCurrent = entry.sessionId === currentId;
    const pointer = isCurrent ? "👉" : "⚪";
    const name = entry.label || `\`${entry.sessionId.slice(0, 8)}\``;
    const time = relativeTime(entry.touchedAt || entry.createdAt);
    lines.push(`${pointer} ${globalIdx}. ${name} (${time})`);
  }

  const hints: string[] = ["\n`/resume <N>` to switch"];
  if (safePage < totalPages) {
    hints.push(`\`/resume --page ${safePage + 1}\` for older`);
  }
  if (safePage > 1) {
    hints.push(`\`/resume --page ${safePage - 1}\` for newer`);
  }
  lines.push(hints.join("\n"));
  return lines.join("\n");
}

export default commandsExtension;
