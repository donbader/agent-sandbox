import { readFileSync } from "node:fs";
import { createLogger } from "./logger.js";
import { AgentProcess } from "./agent-process.js";
import { AcpServer } from "./acp-server.js";
import { StdioRelay } from "./stdio-relay.js";

const log = createLogger("agent-manager");

interface ManagerConfig {
  acp_command: string[];
  cwd: string;
  ws_port?: number;
}

async function main(): Promise<void> {
  const configPath = process.env.AGENT_MANAGER_CONFIG ?? "/opt/agent-manager/config.json";
  const raw = readFileSync(configPath, "utf-8");
  const config: ManagerConfig = JSON.parse(raw);

  if (!config.acp_command || config.acp_command.length === 0) {
    log.fatal("acp_command is required in agent-manager config");
    process.exit(1);
  }

  if (!config.cwd) {
    log.fatal("cwd is required in agent-manager config");
    process.exit(1);
  }

  log.info({ cmd: config.acp_command.join(" "), cwd: config.cwd }, "starting agent manager");

  // Downstream: spawn the actual agent via ACP over stdio
  const agent = new AgentProcess(config.acp_command, config.cwd);

  await agent.start();

  // Perform ACP handshake: initialize + authenticate the agent before accepting clients.
  const initResp = await agent.sendAndWait({
    jsonrpc: "2.0", id: -1, method: "initialize",
    params: { protocolVersion: 1, clientCapabilities: {} },
  });
  if (initResp.error) {
    log.fatal({ error: initResp.error }, "agent initialize failed");
    process.exit(1);
  }
  log.info("agent ACP initialized");

  // Authenticate with a placeholder — the gateway rewrites real credentials on outbound calls.
  // Some agents don't implement auth/authenticate (code -32601) — skip gracefully.
  const authResp = await agent.sendAndWait({
    jsonrpc: "2.0", id: -2, method: "auth/authenticate",
    params: { id: "api-key", secret: "gateway-managed" },
  });
  if (authResp.error) {
    if (authResp.error.code === -32601) {
      log.info("agent does not implement auth/authenticate — skipping");
    } else {
      log.fatal({ error: authResp.error }, "agent auth/authenticate failed");
      process.exit(1);
    }
  } else {
    log.info("agent ACP authenticated");
  }

  // WebSocket server: controlled by plugin.options.port in config.
  // Set to 0 to disable WS and run stdio-only.
  const wsPort = config.ws_port ?? 3100;

  // Stdio relay: always available for local ACP clients.
  // Don't exit on stdin close when WebSocket server is also running.
  const relay = new StdioRelay(agent, config.cwd, { exitOnClose: wsPort <= 0 });
  relay.setInitResult(initResp.result);
  relay.start();

  if (wsPort > 0) {
    const server = new AcpServer(agent, { port: wsPort, cwd: config.cwd });
    server.setInitResult(initResp.result);
    await server.start();
    log.info({ port: wsPort }, "WebSocket ACP endpoint available");

    process.on("SIGTERM", async () => {
      log.info("shutting down");
      await server.stop();
      await agent.stop();
      process.exit(0);
    });
  } else {
    log.info("WebSocket server disabled (port=0)");
    process.on("SIGTERM", async () => {
      log.info("shutting down");
      await agent.stop();
      process.exit(0);
    });
  }
}

main().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});
