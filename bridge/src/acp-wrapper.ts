#!/usr/bin/env node
/**
 * base-acp-wrapper — ACP middleware that intercepts bridge commands
 * before forwarding to the actual agent.
 *
 * Usage: node acp-wrapper.js -- <agent-acp-command...>
 *   e.g.: node acp-wrapper.js -- npx codex-acp
 *
 * Speaks ACP JSON-RPC on stdin/stdout (to bridge).
 * Spawns the real agent command as subprocess and proxies ACP traffic.
 * Intercepts certain prompts (/sh, /diagnose) and handles them locally.
 */
import { spawn } from "node:child_process";
import { execSync } from "node:child_process";
import { createInterface } from "node:readline";
import { cpus, totalmem, freemem } from "node:os";

// --- Parse args ---
const args = process.argv.slice(2);
const dashDash = args.indexOf("--");
if (dashDash === -1 || dashDash === args.length - 1) {
  process.stderr.write("Usage: acp-wrapper.js -- <agent-command...>\n");
  process.exit(1);
}
const agentCmd = args.slice(dashDash + 1);

// --- Perf tracking ---
const perfHistory: number[] = [];
const PERF_MAX = 50;

// --- Bridge commands handled locally ---
function handleBridgeCommand(text: string): string | null {
  const trimmed = text.trim();

  // /sh <command>
  if (trimmed.startsWith("/sh ")) {
    const cmd = trimmed.slice(4).trim();
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

  // /diagnose
  if (trimmed === "/diagnose") {
    const lines = ["🔍 Agent Diagnostics:"];
    lines.push(`  PID: ${process.pid}`);
    lines.push(`  Uptime: ${Math.round(process.uptime())}s`);
    lines.push(`  Memory: ${Math.round(process.memoryUsage().rss / 1024 / 1024)}MB RSS`);
    lines.push(`  System: ${cpus().length} CPUs, ${Math.round(freemem() / 1024 / 1024)}MB free / ${Math.round(totalmem() / 1024 / 1024)}MB total`);
    lines.push(`  CWD: ${process.cwd()}`);
    lines.push(`  Agent cmd: ${agentCmd.join(" ")}`);
    if (perfHistory.length > 0) {
      const sorted = [...perfHistory].sort((a, b) => a - b);
      const avg = Math.round(sorted.reduce((a, b) => a + b, 0) / sorted.length);
      const p95 = sorted[Math.floor(sorted.length * 0.95)];
      const last = perfHistory[perfHistory.length - 1];
      lines.push(`  Perf (${sorted.length} prompts): avg ${avg}ms / p95 ${p95}ms / last ${last}ms`);
    }
    return lines.join("\n");
  }

  return null; // Not a bridge command
}

// --- Extract text from ACP prompt content ---
function extractPromptText(prompt: unknown[]): string | null {
  if (!Array.isArray(prompt) || prompt.length === 0) return null;
  const first = prompt[0] as { type?: string; text?: string };
  if (first.type === "text" && typeof first.text === "string") {
    return first.text;
  }
  return null;
}

// --- JSON-RPC helpers ---
let nextId = 1_000_000; // Use high IDs to avoid collisions with bridge

function makeResponse(id: unknown, result: unknown): string {
  return JSON.stringify({ jsonrpc: "2.0", id, result }) + "\n";
}

function makeSessionUpdate(sessionId: string, text: string): string {
  return JSON.stringify({
    jsonrpc: "2.0",
    method: "session/update",
    params: {
      sessionId,
      update: {
        sessionUpdate: "agent_message_chunk",
        content: { type: "text", text },
      },
    },
  }) + "\n";
}

// --- Spawn agent subprocess ---
const [cmd, ...cmdArgs] = agentCmd;
const agent = spawn(cmd, cmdArgs, {
  stdio: ["pipe", "pipe", "inherit"],
});

agent.on("exit", (code) => {
  process.stderr.write(`[acp-wrapper] agent exited with code ${code}\n`);
  process.exit(code ?? 1);
});

// --- Proxy: bridge stdin → agent stdin (with interception) ---
const bridgeInput = createInterface({ input: process.stdin });

// Track in-flight prompts for perf timing
const promptTimers = new Map<unknown, number>();

bridgeInput.on("line", (line) => {
  let msg: any;
  try {
    msg = JSON.parse(line);
  } catch {
    // Not JSON, pass through
    agent.stdin!.write(line + "\n");
    return;
  }

  // Intercept session/prompt requests
  if (msg.method === "session/prompt" && msg.id != null) {
    const text = extractPromptText(msg.params?.prompt);
    if (text) {
      const result = handleBridgeCommand(text);
      if (result !== null) {
        // Handle locally — send chunk + response back to bridge
        const sessionId = msg.params?.sessionId ?? "";
        process.stdout.write(makeSessionUpdate(sessionId, result));
        process.stdout.write(makeResponse(msg.id, { stopReason: "end_turn" }));
        return; // Don't forward to agent
      }
    }
    // Track timing for forwarded prompts
    promptTimers.set(msg.id, Date.now());
  }

  // Forward to agent
  agent.stdin!.write(line + "\n");
});

bridgeInput.on("close", () => {
  agent.stdin!.end();
});

// --- Proxy: agent stdout → bridge stdout (with perf tracking) ---
const agentOutput = createInterface({ input: agent.stdout! });

agentOutput.on("line", (line) => {
  // Track prompt completion for perf
  let msg: any;
  try {
    msg = JSON.parse(line);
  } catch {
    process.stdout.write(line + "\n");
    return;
  }

  // If this is a response to a tracked prompt, record timing
  if (msg.id != null && msg.result?.stopReason && promptTimers.has(msg.id)) {
    const elapsed = Date.now() - promptTimers.get(msg.id)!;
    promptTimers.delete(msg.id);
    perfHistory.push(elapsed);
    if (perfHistory.length > PERF_MAX) perfHistory.shift();
  }

  process.stdout.write(line + "\n");
});

agentOutput.on("close", () => {
  process.exit(0);
});
