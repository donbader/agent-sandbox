import { createServer, IncomingMessage, ServerResponse, Server } from "node:http";
import { WebSocketServer, WebSocket } from "ws";
import { createLogger } from "./logger.js";
import { AgentProcess } from "./agent-process.js";

const log = createLogger("acp-server");

interface AcpServerOptions {
  port: number;
}

/**
 * AcpServer exposes the agent-manager as an ACP Agent over HTTP/WebSocket.
 * 
 * Channel adapters (telegram, slack, etc.) connect here as ACP clients.
 * The server proxies ACP messages between upstream clients and the downstream
 * agent process (via stdio).
 * 
 * Supported transports (per ACP Streamable HTTP RFD):
 * - WebSocket: GET /acp with Upgrade: websocket
 * - Streamable HTTP: POST /acp + GET /acp (SSE streams)
 */
export class AcpServer {
  private agent: AgentProcess;
  private port: number;
  private httpServer: Server | null = null;
  private wss: WebSocketServer | null = null;

  constructor(agent: AgentProcess, options: AcpServerOptions) {
    this.agent = agent;
    this.port = options.port;
  }

  async start(): Promise<void> {
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
    this.wss?.close();
    return new Promise((resolve) => {
      if (this.httpServer) {
        this.httpServer.close(() => resolve());
      } else {
        resolve();
      }
    });
  }

  /**
   * Handle HTTP requests — Streamable HTTP transport.
   * POST /acp → client-to-agent JSON-RPC messages
   * GET /acp → SSE stream for agent-to-client messages
   * DELETE /acp → terminate connection
   */
  private handleHttp(req: IncomingMessage, res: ServerResponse): void {
    // Health check
    if (req.url === "/health" && req.method === "GET") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok", agent_running: this.agent.isRunning }));
      return;
    }

    if (req.url !== "/acp") {
      res.writeHead(404);
      res.end("Not found");
      return;
    }

    switch (req.method) {
      case "POST":
        this.handlePost(req, res);
        break;
      case "GET":
        this.handleGet(req, res);
        break;
      case "DELETE":
        this.handleDelete(req, res);
        break;
      default:
        res.writeHead(405);
        res.end("Method not allowed");
    }
  }

  private handlePost(req: IncomingMessage, res: ServerResponse): void {
    let body = "";
    req.on("data", (chunk) => { body += chunk; });
    req.on("end", () => {
      try {
        const message = JSON.parse(body);
        log.debug({ method: message.method, id: message.id }, "received ACP message");
        this.forwardToAgent(message, res);
      } catch {
        res.writeHead(400, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: "Invalid JSON" }));
      }
    });
  }

  private handleGet(_req: IncomingMessage, res: ServerResponse): void {
    // Open SSE stream for server→client messages
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      "Connection": "keep-alive",
    });
    res.write(":ok\n\n");

    // TODO: Register this stream for pushing agent notifications
    log.debug("SSE stream opened");
  }

  private handleDelete(_req: IncomingMessage, res: ServerResponse): void {
    res.writeHead(202);
    res.end();
    log.debug("connection terminated");
  }

  /**
   * Handle WebSocket connections — full-duplex ACP transport.
   */
  private handleWebSocket(ws: WebSocket): void {
    log.info("WebSocket client connected");

    ws.on("message", (data) => {
      try {
        const message = JSON.parse(data.toString());
        log.debug({ method: message.method, id: message.id }, "WS received");
        this.forwardToAgentWs(message, ws);
      } catch {
        ws.send(JSON.stringify({ jsonrpc: "2.0", error: { code: -32700, message: "Parse error" } }));
      }
    });

    ws.on("close", () => {
      log.info("WebSocket client disconnected");
    });
  }

  private forwardToAgent(message: Record<string, unknown>, res: ServerResponse): void {
    const stdin = this.agent.stdin;
    if (!stdin) {
      res.writeHead(503, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ jsonrpc: "2.0", id: message.id, error: { code: -32000, message: "Agent not running" } }));
      return;
    }

    stdin.write(JSON.stringify(message) + "\n");

    // Per ACP spec: initialize returns 200 with JSON body, everything else returns 202
    if (message.method === "initialize") {
      // TODO: Wait for actual agent response and return it synchronously
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ jsonrpc: "2.0", id: message.id, result: { protocolVersion: "1", agentCapabilities: {} } }));
    } else {
      res.writeHead(202);
      res.end();
    }
  }

  private forwardToAgentWs(message: Record<string, unknown>, ws: WebSocket): void {
    const stdin = this.agent.stdin;
    if (!stdin) {
      ws.send(JSON.stringify({ jsonrpc: "2.0", id: message.id, error: { code: -32000, message: "Agent not running" } }));
      return;
    }

    stdin.write(JSON.stringify(message) + "\n");
    // TODO: Route agent stdout responses back to this ws connection
  }
}
