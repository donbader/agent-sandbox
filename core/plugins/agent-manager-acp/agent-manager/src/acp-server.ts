import { createServer, IncomingMessage, ServerResponse, Server } from "node:http";
import { WebSocketServer, WebSocket } from "ws";
import { createLogger } from "./logger.js";
import { AgentProcess, JsonRpcMessage } from "./agent-process.js";

const log = createLogger("acp-server");

interface AcpServerOptions {
  port: number;
}

interface PendingRequest {
  resolve: (msg: JsonRpcMessage) => void;
  timeout: NodeJS.Timeout;
}

/**
 * AcpServer exposes the agent-manager as an ACP Agent over HTTP/WebSocket.
 *
 * Architecture:
 *   Client ──[WS/HTTP]──► AcpServer ──[stdio JSON-RPC]──► Agent Process
 *   Client ◄──[WS/SSE]── AcpServer ◄──[stdout lines]──── Agent Process
 */
export class AcpServer {
  private agent: AgentProcess;
  private port: number;
  private httpServer: Server | null = null;
  private wss: WebSocketServer | null = null;
  private wsClients = new Set<WebSocket>();
  private sseClients = new Set<ServerResponse>();
  private pendingRequests = new Map<number | string, PendingRequest>();

  constructor(agent: AgentProcess, options: AcpServerOptions) {
    this.agent = agent;
    this.port = options.port;
  }

  async start(): Promise<void> {
    this.agent.on("message", (msg: JsonRpcMessage) => this.handleAgentMessage(msg));
    this.httpServer = createServer(this.handleHttp.bind(this));
    this.wss = new WebSocketServer({ server: this.httpServer });
    this.wss.on("connection", this.handleWebSocket.bind(this));
    return new Promise((resolve) => {
      this.httpServer!.listen(this.port, () => {
        log.info({ port: this.port }, "ACP server listening");
        resolve();
      });
    });
  }

  async stop(): Promise<void> {
    for (const res of this.sseClients) { res.end(); }
    this.sseClients.clear();
    for (const ws of this.wsClients) { ws.close(); }
    this.wsClients.clear();
    this.wss?.close();
    return new Promise((resolve) => {
      this.httpServer ? this.httpServer.close(() => resolve()) : resolve();
    });
  }

  private handleAgentMessage(msg: JsonRpcMessage): void {
    if (msg.id !== undefined && this.pendingRequests.has(msg.id)) {
      const pending = this.pendingRequests.get(msg.id)!;
      this.pendingRequests.delete(msg.id);
      clearTimeout(pending.timeout);
      pending.resolve(msg);
      return;
    }
    this.broadcastToClients(msg);
  }

  private broadcastToClients(msg: JsonRpcMessage): void {
    const data = JSON.stringify(msg);
    for (const ws of this.wsClients) {
      if (ws.readyState === WebSocket.OPEN) ws.send(data);
    }
    for (const res of this.sseClients) {
      res.write(`data: ${data}\n\n`);
    }
  }

  private forwardAndWait(msg: JsonRpcMessage, timeoutMs = 30000): Promise<JsonRpcMessage> {
    return new Promise((resolve) => {
      if (!this.agent.send(msg)) {
        resolve({ jsonrpc: "2.0", id: msg.id, error: { code: -32000, message: "Agent not running" } });
        return;
      }
      if (msg.id === undefined) { resolve({ jsonrpc: "2.0" } as JsonRpcMessage); return; }
      const timeout = setTimeout(() => {
        this.pendingRequests.delete(msg.id!);
        resolve({ jsonrpc: "2.0", id: msg.id, error: { code: -32000, message: "Agent response timeout" } });
      }, timeoutMs);
      this.pendingRequests.set(msg.id, { resolve, timeout });
    });
  }

  private interceptCommand(msg: JsonRpcMessage): JsonRpcMessage | null {
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

  // ─── HTTP Transport ────────────────────────────────────────────────────────

  private handleHttp(req: IncomingMessage, res: ServerResponse): void {
    if (req.url === "/health" && req.method === "GET") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok", agent_running: this.agent.isRunning }));
      return;
    }
    if (req.url !== "/acp") { res.writeHead(404); res.end("Not found"); return; }
    switch (req.method) {
      case "POST": this.handlePost(req, res); break;
      case "GET": this.handleGet(req, res); break;
      case "DELETE": this.handleDelete(res); break;
      default: res.writeHead(405); res.end("Method not allowed");
    }
  }

  private handlePost(req: IncomingMessage, res: ServerResponse): void {
    let body = "";
    req.on("data", (chunk) => { body += chunk; });
    req.on("end", async () => {
      try {
        const message: JsonRpcMessage = JSON.parse(body);
        log.debug({ method: message.method, id: message.id }, "HTTP POST");
        const intercepted = this.interceptCommand(message);
        if (intercepted) { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify(intercepted)); return; }
        if (message.method === "initialize") {
          const response = await this.forwardAndWait(message, 10000);
          res.writeHead(200, { "Content-Type": "application/json" });
          res.end(JSON.stringify(response));
          return;
        }
        if (!this.agent.send(message)) {
          res.writeHead(503, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ jsonrpc: "2.0", id: message.id, error: { code: -32000, message: "Agent not running" } }));
          return;
        }
        if (message.id !== undefined) {
          const timeout = setTimeout(() => { this.pendingRequests.delete(message.id!); }, 300000);
          this.pendingRequests.set(message.id, { resolve: (r) => this.broadcastToClients(r), timeout });
        }
        res.writeHead(202); res.end();
      } catch { res.writeHead(400); res.end(JSON.stringify({ error: "Invalid JSON" })); }
    });
  }

  private handleGet(_req: IncomingMessage, res: ServerResponse): void {
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", "Connection": "keep-alive" });
    res.write(":ok\n\n");
    this.sseClients.add(res);
    log.debug({ total: this.sseClients.size }, "SSE stream opened");
    res.on("close", () => { this.sseClients.delete(res); });
  }

  private handleDelete(res: ServerResponse): void { res.writeHead(202); res.end(); }

  // ─── WebSocket Transport ──────────────────────────────────────────────────

  private handleWebSocket(ws: WebSocket): void {
    this.wsClients.add(ws);
    log.info({ total: this.wsClients.size }, "WebSocket client connected");
    ws.on("message", async (data) => {
      try {
        const message: JsonRpcMessage = JSON.parse(data.toString());
        log.debug({ method: message.method, id: message.id }, "WS received");
        const intercepted = this.interceptCommand(message);
        if (intercepted) { ws.send(JSON.stringify(intercepted)); return; }
        if (message.id !== undefined) {
          const response = await this.forwardAndWait(message);
          ws.send(JSON.stringify(response));
        } else {
          this.agent.send(message);
        }
      } catch { ws.send(JSON.stringify({ jsonrpc: "2.0", error: { code: -32700, message: "Parse error" } })); }
    });
    ws.on("close", () => { this.wsClients.delete(ws); log.info({ total: this.wsClients.size }, "WS disconnected"); });
  }
}
