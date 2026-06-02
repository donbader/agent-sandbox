#!/usr/bin/env node
/**
 * base-acp-wrapper — Line-filter ACP middleware.
 *
 * Usage: node acp-wrapper.js -- <agent-acp-command...>
 *
 * Transparent ndjson pipe between channel manager and agent.
 * Intercepts session/prompt for wrapper commands (/sh, /diagnose).
 * All other ACP messages pass through untouched.
 */
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import type {
  PromptRequest,
  PromptResponse,
  SessionNotification,
} from "@agentclientprotocol/sdk";
import { handleWrapperCommand } from "./wrapper-commands.js";

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
const promptTimers = new Map<unknown, number>();

/** Write a JSON-RPC message to channel manager (stdout). */
function writeToBridge(msg: object): void {
  process.stdout.write(JSON.stringify(msg) + "\n");
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

// --- Spawn real agent ---
const [cmd, ...cmdArgs] = agentCmd;
const agent = spawn(cmd, cmdArgs, {
  stdio: ["pipe", "pipe", "inherit"],
});

agent.on("exit", (code) => {
  process.stderr.write(`[acp-wrapper] agent exited with code ${code}\n`);
  process.exit(code ?? 1);
});

// --- Bridge → Agent (with interception) ---
const managerInput = createInterface({ input: process.stdin });

managerInput.on("line", (line) => {
  let msg: { jsonrpc?: string; method?: string; id?: unknown; params?: any };
  try {
    msg = JSON.parse(line);
  } catch {
    agent.stdin!.write(line + "\n");
    return;
  }

  // Intercept session/prompt
  if (msg.method === "session/prompt" && msg.id != null) {
    const params = msg.params as PromptRequest;
    const text = extractPromptText(params.prompt);

    if (text) {
      const result = handleWrapperCommand(text, { agentCmd, perfHistory });
      if (result !== null) {
        // Respond locally — send chunk notification + response
        const notification: { jsonrpc: string; method: string; params: SessionNotification } = {
          jsonrpc: "2.0",
          method: "session/update",
          params: {
            sessionId: params.sessionId,
            update: {
              sessionUpdate: "agent_message_chunk",
              content: { type: "text", text: result },
            },
          },
        };
        const response: { jsonrpc: string; id: unknown; result: PromptResponse } = {
          jsonrpc: "2.0",
          id: msg.id,
          result: { stopReason: "end_turn" },
        };
        writeToBridge(notification);
        writeToBridge(response);
        return;
      }
    }
    // Track timing for forwarded prompts
    promptTimers.set(msg.id, Date.now());
  }

  // Pass through to agent
  agent.stdin!.write(line + "\n");
});

managerInput.on("close", () => {
  agent.stdin!.end();
});

// --- Agent → Bridge (with perf tracking) ---
const agentOutput = createInterface({ input: agent.stdout! });

agentOutput.on("line", (line) => {
  // Track prompt completion for perf
  try {
    const msg = JSON.parse(line) as { id?: unknown; result?: { stopReason?: string } };
    if (msg.id != null && msg.result?.stopReason && promptTimers.has(msg.id)) {
      const elapsed = Date.now() - promptTimers.get(msg.id)!;
      promptTimers.delete(msg.id);
      perfHistory.push(elapsed);
      if (perfHistory.length > PERF_MAX) perfHistory.shift();
    }
  } catch {
    // Not JSON — pass through
  }

  process.stdout.write(line + "\n");
});

agentOutput.on("close", () => {
  process.exit(0);
});

process.stderr.write(`[acp-wrapper] proxying to: ${agentCmd.join(" ")}\n`);
