import { createInterface } from "node:readline";
import { createLogger } from "./logger.js";
import { AgentProcess, JsonRpcMessage } from "./agent-process.js";

const log = createLogger("stdio-relay");

/**
 * StdioRelay exposes the agent over the parent process's stdin/stdout.
 *
 * Reads ndjson from process.stdin, forwards to the agent, and writes
 * agent responses/notifications to process.stdout. Supports the same
 * interception logic as AcpServer (cached init, auth bypass, /restart).
 *
 * When exitOnClose=false, stdin closing won't terminate the process
 * (useful when WebSocket server is also running).
 */
export class StdioRelay {
  private agent: AgentProcess;
  private cwd: string;
  private initResult: JsonRpcMessage["result"] | null = null;
  private exitOnClose: boolean;

  constructor(agent: AgentProcess, cwd: string, opts?: { exitOnClose?: boolean }) {
    this.agent = agent;
    this.cwd = cwd;
    this.exitOnClose = opts?.exitOnClose ?? true;
  }

  setInitResult(result: JsonRpcMessage["result"]): void {
    this.initResult = result;
  }

  start(): void {
    // Forward agent notifications/responses to parent via stdout
    this.agent.on("message", (msg: JsonRpcMessage) => {
      // If it's a response to a pending request, send to parent
      // If it's a notification (no id), also send to parent
      this.send(msg);
    });

    this.agent.on("exit", (code: number | null, signal: string | null) => {
      log.warn({ code, signal }, "agent exited — notifying parent");
      this.send({
        jsonrpc: "2.0",
        method: "session/update",
        params: {
          sessionId: "__system__",
          update: { sessionUpdate: "error", error: { code: -32000, message: `Agent process exited (code=${code}, signal=${signal})` } },
        },
      });
    });

    this.agent.on("stderr", (text: string) => {
      if (/\b(401|403|unauthorized|forbidden|authentication failed|invalid.*key|expired.*token)\b/i.test(text)) {
        this.send({
          jsonrpc: "2.0",
          method: "session/update",
          params: { sessionId: "__system__", update: { sessionUpdate: "error", error: { code: -32000, message: `Agent authentication error: ${text.slice(0, 200)}` } } },
        });
      } else if (/\b(429|rate.?limit|too many requests)\b/i.test(text)) {
        this.send({
          jsonrpc: "2.0",
          method: "session/update",
          params: { sessionId: "__system__", update: { sessionUpdate: "error", error: { code: -32000, message: `Agent rate limited: ${text.slice(0, 200)}` } } },
        });
      } else if (/\b(500|502|503|504|internal server error|service unavailable|bad gateway)\b/i.test(text)) {
        this.send({
          jsonrpc: "2.0",
          method: "session/update",
          params: { sessionId: "__system__", update: { sessionUpdate: "error", error: { code: -32000, message: `Agent upstream error: ${text.slice(0, 200)}` } } },
        });
      }
    });

    // Read from parent's stdin
    const rl = createInterface({ input: process.stdin });
    rl.on("line", (line) => {
      if (!line.trim()) return;
      try {
        const msg: JsonRpcMessage = JSON.parse(line);
        this.handleParentMessage(msg);
      } catch {
        log.warn({ line: line.slice(0, 100) }, "non-JSON line from parent stdin");
      }
    });

    rl.on("close", () => {
      if (this.exitOnClose) {
        log.info("parent stdin closed, shutting down");
        process.exit(0);
      } else {
        log.debug("stdin closed (WebSocket server still running)");
      }
    });

    log.info("stdio relay started");
  }

  private handleParentMessage(msg: JsonRpcMessage): void {
    const intercepted = this.interceptMessage(msg);
    if (intercepted) {
      this.send(intercepted);
      return;
    }
    if (!this.agent.send(msg)) {
      // Agent is not running — return an error response if the message has an id
      if (msg.id) {
        this.send({
          jsonrpc: "2.0",
          id: msg.id,
          error: { code: -32000, message: "Agent process is not running" },
        });
      }
    }
  }

  private interceptMessage(msg: JsonRpcMessage): JsonRpcMessage | null {
    if (msg.method === "initialize") {
      return { jsonrpc: "2.0", id: msg.id, result: this.initResult };
    }
    if (msg.method === "auth/authenticate") {
      return { jsonrpc: "2.0", id: msg.id, result: {} };
    }
    if (msg.method === "session/new") {
      if (!msg.params) msg.params = {};
      const params = msg.params as Record<string, unknown>;
      if (!params.cwd) params.cwd = this.cwd;
      if (!params.mcpServers) params.mcpServers = [];
      return null;
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
    return null;
  }

  private send(msg: JsonRpcMessage): void {
    process.stdout.write(JSON.stringify(msg) + "\n");
  }
}
