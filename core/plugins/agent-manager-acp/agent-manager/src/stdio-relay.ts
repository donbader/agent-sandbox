import { createInterface } from "node:readline";
import { createLogger } from "./logger.js";
import { AgentProcess, JsonRpcMessage } from "./agent-process.js";
import { MessageHandler } from "./message-handler.js";

const log = createLogger("stdio-relay");

/**
 * StdioRelay is a transport layer that bridges parent process stdin/stdout
 * to the agent via the MessageHandler.
 *
 * It handles only transport concerns (reading/writing ndjson over stdio).
 * All message interception, enrichment, and command handling lives in MessageHandler.
 */
export class StdioRelay {
  private handler: MessageHandler;
  private agent: AgentProcess;
  private exitOnClose: boolean;

  constructor(agent: AgentProcess, cwd: string, opts?: { exitOnClose?: boolean }) {
    this.agent = agent;
    this.exitOnClose = opts?.exitOnClose ?? true;
    this.handler = new MessageHandler(agent, cwd);
  }

  setInitResult(result: JsonRpcMessage["result"]): void {
    this.handler.setInitResult(result);
  }

  start(): void {
    // Agent → parent: enrich and forward
    this.agent.on("message", (msg: JsonRpcMessage) => {
      const enriched = this.handler.handleAgentMessage(msg);
      this.send(enriched);
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

    // Parent → agent: intercept or forward
    const rl = createInterface({ input: process.stdin });
    rl.on("line", (line) => {
      if (!line.trim()) return;
      try {
        const msg: JsonRpcMessage = JSON.parse(line);
        const response = this.handler.handleClientMessage(msg, (m) => this.send(m));
        if (response) this.send(response);
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

  private send(msg: JsonRpcMessage): void {
    process.stdout.write(JSON.stringify(msg) + "\n");
  }
}
