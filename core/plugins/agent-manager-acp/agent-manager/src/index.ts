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

  // Upstream: expose ACP over HTTP/WebSocket for channel adapters
  const server = new AcpServer(agent, { port: 3100 });

  await agent.start();
  await server.start();

  log.info({ port: 3100 }, "agent manager ready — ACP endpoint available");

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
