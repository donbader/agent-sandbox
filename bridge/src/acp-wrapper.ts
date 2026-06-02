#!/usr/bin/env node
/**
 * base-acp-wrapper — SDK-based ACP middleware that intercepts bridge commands.
 *
 * Usage: node acp-wrapper.js -- <agent-acp-command...>
 *   e.g.: node acp-wrapper.js -- npx codex-acp
 *
 * Acts as ACP server to bridge (stdin/stdout via AgentSideConnection).
 * Acts as ACP client to real agent (subprocess via ClientSideConnection).
 * Intercepts /sh and /diagnose prompts, handles locally.
 */
import { spawn } from "node:child_process";
import { execSync } from "node:child_process";
import { Writable, Readable } from "node:stream";
import { cpus, totalmem, freemem } from "node:os";
import * as acp from "@agentclientprotocol/sdk";

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

  if (trimmed === "/sh") return "Usage: /sh <command>";

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

  return null;
}

/** Extract text from ACP prompt content array. */
function extractPromptText(prompt: unknown): string | null {
  if (!Array.isArray(prompt) || prompt.length === 0) return null;
  const first = prompt[0] as { type?: string; text?: string };
  if (first.type === "text" && typeof first.text === "string") {
    return first.text;
  }
  return null;
}

// --- Spawn real agent subprocess ---
const [cmd, ...cmdArgs] = agentCmd;
const agentProc = spawn(cmd, cmdArgs, {
  stdio: ["pipe", "pipe", "inherit"],
});

agentProc.on("exit", (code) => {
  process.stderr.write(`[acp-wrapper] agent exited with code ${code}\n`);
  process.exit(code ?? 1);
});

// --- Connect to real agent as ACP client ---
const agentInput = Writable.toWeb(agentProc.stdin!);
const agentOutput = Readable.toWeb(agentProc.stdout!) as ReadableStream<Uint8Array>;
const agentStream = acp.ndJsonStream(agentInput, agentOutput);

// BridgeClient proxies session updates from agent back to bridge
let bridgeConn: acp.AgentSideConnection | null = null;

class ProxyClient implements acp.Client {
  async requestPermission(
    params: acp.RequestPermissionRequest
  ): Promise<acp.RequestPermissionResponse> {
    // Proxy permission requests back to bridge (auto-approve here since bridge also auto-approves)
    const allowOption = params.options.find(
      (o) => o.kind === "allow_once" || o.kind === "allow_always"
    );
    const chosen = allowOption ?? params.options[0];
    if (!chosen) throw new Error("requestPermission: no options");
    return { outcome: { outcome: "selected", optionId: chosen.optionId } };
  }

  async sessionUpdate(params: acp.SessionNotification): Promise<void> {
    // Forward all session updates from agent to bridge
    if (bridgeConn) {
      await bridgeConn.sessionUpdate(params);
    }
  }
}

const agentConn = new acp.ClientSideConnection(() => new ProxyClient(), agentStream);

// --- Accept bridge connection as ACP server ---
const bridgeInput = Writable.toWeb(process.stdout);
const bridgeOutput = Readable.toWeb(process.stdin) as ReadableStream<Uint8Array>;
const bridgeStream = acp.ndJsonStream(bridgeInput, bridgeOutput);

class WrapperAgent implements acp.Agent {
  async initialize(params: acp.InitializeRequest): Promise<acp.InitializeResponse> {
    return agentConn.initialize(params);
  }

  async newSession(params: acp.NewSessionRequest): Promise<acp.NewSessionResponse> {
    return agentConn.newSession(params);
  }

  async loadSession(params: acp.LoadSessionRequest): Promise<acp.LoadSessionResponse> {
    return agentConn.loadSession(params);
  }

  async authenticate(params: acp.AuthenticateRequest): Promise<acp.AuthenticateResponse | void> {
    return (agentConn as any).authenticate?.(params);
  }

  async prompt(params: acp.PromptRequest): Promise<acp.PromptResponse> {
    const text = extractPromptText(params.prompt);

    // Intercept bridge commands
    if (text) {
      const result = handleBridgeCommand(text);
      if (result !== null) {
        // Send response as agent_message_chunk, then return
        await bridgeConn!.sessionUpdate({
          sessionId: params.sessionId,
          update: {
            sessionUpdate: "agent_message_chunk",
            content: { type: "text", text: result },
          },
        });
        return { stopReason: "end_turn" } as acp.PromptResponse;
      }
    }

    // Forward to real agent with perf tracking
    const t0 = Date.now();
    const response = await agentConn.prompt(params);
    const elapsed = Date.now() - t0;
    perfHistory.push(elapsed);
    if (perfHistory.length > PERF_MAX) perfHistory.shift();

    return response;
  }

  async cancel(params: acp.CancelNotification): Promise<void> {
    return (agentConn as any).cancel?.(params);
  }
}

bridgeConn = new acp.AgentSideConnection(
  () => new WrapperAgent(),
  bridgeStream
);

process.stderr.write(`[acp-wrapper] proxying to: ${agentCmd.join(" ")}\n`);
