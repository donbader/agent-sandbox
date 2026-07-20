import { execSync } from "node:child_process";
import { createLogger } from "./logger.js";
import { AgentProcess, JsonRpcMessage } from "./agent-process.js";

const log = createLogger("message-handler");

/**
 * MessageHandler contains all ACP message interception and enrichment logic.
 *
 * It is transport-agnostic — any relay (stdio, websocket, etc.) can use it
 * to process inbound messages from the client and outbound messages from the agent.
 */
export class MessageHandler {
  private agent: AgentProcess;
  private cwd: string;
  private initResult: JsonRpcMessage["result"] | null = null;

  constructor(agent: AgentProcess, cwd: string) {
    this.agent = agent;
    this.cwd = cwd;
  }

  setInitResult(result: JsonRpcMessage["result"]): void {
    this.initResult = result;
  }

  /**
   * Process a message from the client (inbound).
   *
   * Returns a response to send back if the message was intercepted,
   * or null if it should be forwarded to the agent.
   * May also call `emit` for intermediate notifications (e.g. /sh output).
   */
  handleClientMessage(msg: JsonRpcMessage, emit: (msg: JsonRpcMessage) => void): JsonRpcMessage | null {
    const intercepted = this.interceptMessage(msg, emit);
    if (intercepted) return intercepted;

    // Log responses and cancel notifications for debugging
    if (!msg.method && msg.id !== undefined) {
      log.debug({ id: msg.id, hasResult: !!msg.result, hasError: !!msg.error }, "forwarding response to agent");
    } else if (msg.method === "session/cancel") {
      log.debug({ method: msg.method, params: msg.params }, "forwarding cancel notification to agent");
    }

    if (!this.agent.send(msg)) {
      if (msg.id) {
        return {
          jsonrpc: "2.0",
          id: msg.id,
          error: { code: -32000, message: "Agent process is not running" },
        };
      }
    }
    return null;
  }

  /**
   * Process a message from the agent (outbound).
   *
   * Enriches the message in place (e.g. appending manager commands)
   * and returns it. Always returns the message — never swallows it.
   */
  handleAgentMessage(msg: JsonRpcMessage): JsonRpcMessage {
    if (msg.method === "session/update") {
      const params = msg.params as any;
      if (params?.update?.sessionUpdate === "available_commands_update" && Array.isArray(params.update.availableCommands)) {
        params.update.availableCommands = [
          ...params.update.availableCommands,
          ...this.getManagerCommands(),
        ];
      }
    }
    return msg;
  }

  private interceptMessage(msg: JsonRpcMessage, emit: (msg: JsonRpcMessage) => void): JsonRpcMessage | null {
    if (msg.method === "initialize") {
      return { jsonrpc: "2.0", id: msg.id, result: this.initResult };
    }
    if (msg.method === "auth/authenticate") {
      return { jsonrpc: "2.0", id: msg.id, result: {} };
    }
    if (msg.method === "session/new" || msg.method === "session/load") {
      if (!msg.params) msg.params = {};
      const params = msg.params as Record<string, unknown>;
      if (!params.cwd) params.cwd = this.cwd;
      if (!params.mcpServers) params.mcpServers = [];
      return null; // forward to agent (with enriched params)
    }
    if (msg.method !== "session/prompt") return null;

    const params = msg.params as { prompt?: Array<{ type: string; text: string }>; sessionId?: string } | undefined;
    const text = params?.prompt?.[0]?.text?.trim();
    if (!text) return null;

    if (text === "/restart") {
      log.info("intercepted /restart command");
      this.agent.restart();
      return { jsonrpc: "2.0", id: msg.id, result: { stopReason: "end_turn", sessionId: params?.sessionId } };
    }

    if (text.startsWith("/sh")) {
      return this.handleShCommand(msg.id, params?.sessionId, text, emit);
    }

    return null; // forward to agent
  }

  private handleShCommand(
    id: JsonRpcMessage["id"],
    sessionId: string | undefined,
    text: string,
    emit: (msg: JsonRpcMessage) => void,
  ): JsonRpcMessage {
    const cmd = text.replace(/^\/sh\s*/, "");
    if (!cmd) {
      return { jsonrpc: "2.0", id, error: { code: -32602, message: "Usage: /sh <command>" } };
    }

    log.info({ cmd }, "intercepted /sh command");
    let output: string;
    try {
      output = execSync(cmd, { cwd: this.cwd, timeout: 30000, encoding: "utf-8", maxBuffer: 1024 * 1024 });
    } catch (err: any) {
      const stderr = err.stderr?.toString()?.slice(0, 2000) ?? "";
      const stdout = err.stdout?.toString()?.slice(0, 2000) ?? "";
      output = (stdout || stderr || err.message);
    }

    const displayOutput = output.trim() || "(no output)";
    emit({
      jsonrpc: "2.0",
      method: "session/update",
      params: {
        sessionId: sessionId ?? "__system__",
        update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "```\n" + displayOutput.slice(0, 4000) + "\n```" } },
      },
    });
    return { jsonrpc: "2.0", id, result: { stopReason: "end_turn", sessionId } };
  }

  private getManagerCommands(): Array<{ name: string; description: string; input?: { type: string; hint?: string } }> {
    return [
      { name: "restart", description: "Restart the agent process", input: { type: "unstructured", hint: "" } },
      { name: "sh", description: "Run a shell command in the container", input: { type: "unstructured", hint: "command to run" } },
    ];
  }
}
