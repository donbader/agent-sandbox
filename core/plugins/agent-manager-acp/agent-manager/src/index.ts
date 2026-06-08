import { readFileSync } from "node:fs";
import { createLogger } from "./logger.js";
import { AgentProcess } from "./agent-process.js";
import { AcpServer } from "./acp-server.js";

const log = createLogger("agent-manager");

interface ManagerConfig {
  acp_command: string[];
  cwd: string;
}

async function main(): Promise<void> {
  const port = parseInt(process.env.AGENT_MANAGER_PORT ?? "3100", 10);
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

  // Upstream: expose ACP over HTTP/WebSocket for channel adapters
  const server = new AcpServer(agent, { port });
  server.setInitResult(initResp.result);
  await server.start();

  log.info({ port }, "agent manager ready — ACP endpoint available");

  // Graceful shutdown
  process.on("SIGTERM", async () => {
    log.info("shutting down");
    await server.stop();
    await agent.stop();
    process.exit(0);
  });
}

main().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});
