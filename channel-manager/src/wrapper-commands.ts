/**
 * Bridge commands — handled locally by the ACP wrapper.
 * These are commands that the underlying agent doesn't support,
 * added by the enriched ACP server.
 */
import { execSync } from "node:child_process";
import { cpus, totalmem, freemem } from "node:os";

export interface WrapperCommandContext {
  agentCmd: string[];
  perfHistory: number[];
}

/**
 * Attempt to handle a wrapper command.
 * Returns the response string if handled, or null if not a wrapper command.
 */
export function handleWrapperCommand(text: string, ctx: WrapperCommandContext): string | null {
  const trimmed = text.trim();

  if (trimmed === "/sh") return "Usage: /sh <command>";
  if (trimmed.startsWith("/sh ")) return handleSh(trimmed.slice(4).trim());
  if (trimmed === "/diagnose") return handleDiagnose(ctx);

  return null;
}

function handleSh(cmd: string): string {
  if (!cmd) return "Usage: /sh <command>";
  try {
    const output = execSync(cmd, {
      timeout: 30_000,
      maxBuffer: 1024 * 1024,
      encoding: "utf-8",
      cwd: process.env.HOME ?? "/workspace",
    });
    return output.trim().slice(0, 4000) || "(no output)";
  } catch (err: unknown) {
    const e = err as { status?: number; stdout?: string; stderr?: string };
    const output = (e.stdout || "") + (e.stderr || "");
    return `Exit ${e.status ?? "?"}:\n${output.trim().slice(0, 4000)}`;
  }
}

function handleDiagnose(ctx: WrapperCommandContext): string {
  const lines = ["🔍 Agent Diagnostics:"];
  lines.push(`  PID: ${process.pid}`);
  lines.push(`  Uptime: ${Math.round(process.uptime())}s`);
  lines.push(`  Memory: ${Math.round(process.memoryUsage().rss / 1024 / 1024)}MB RSS`);
  lines.push(`  System: ${cpus().length} CPUs, ${Math.round(freemem() / 1024 / 1024)}MB free / ${Math.round(totalmem() / 1024 / 1024)}MB total`);
  lines.push(`  CWD: ${process.cwd()}`);
  lines.push(`  Agent cmd: ${ctx.agentCmd.join(" ")}`);
  if (ctx.perfHistory.length > 0) {
    const sorted = [...ctx.perfHistory].sort((a, b) => a - b);
    const avg = Math.round(sorted.reduce((a, b) => a + b, 0) / sorted.length);
    const p95 = sorted[Math.floor(sorted.length * 0.95)];
    const last = ctx.perfHistory[ctx.perfHistory.length - 1];
    lines.push(`  Perf (${sorted.length} prompts): avg ${avg}ms / p95 ${p95}ms / last ${last}ms`);
  }
  return lines.join("\n");
}
