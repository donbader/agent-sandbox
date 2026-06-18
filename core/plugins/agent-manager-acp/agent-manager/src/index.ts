import { readFileSync } from "node:fs";
import { createLogger } from "./logger.js";
import { AgentProcess } from "./agent-process.js";
import { StdioRelay } from "./stdio-relay.js";

const log = createLogger("agent-manager");

const RESPAWN_DELAY_MS = 3000;

interface ManagerConfig {
  acp_command: string[];
  cwd: string;
}

/** Perform the ACP initialize + auth handshake. Returns the init result. */
async function handshake(agent: AgentProcess): Promise<unknown> {
  const initResp = await agent.sendAndWait({
    jsonrpc: "2.0", id: -1, method: "initialize",
    params: { protocolVersion: 1, clientCapabilities: {} },
  });
  if (initResp.error) {
    throw new Error(`agent initialize failed: ${initResp.error.message}`);
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
      throw new Error(`agent auth/authenticate failed: ${authResp.error.message}`);
    }
  } else {
    log.info("agent ACP authenticated");
  }

  return initResp.result;
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

  let shuttingDown = false;

  // Downstream: spawn the actual agent via ACP over stdio
  const agent = new AgentProcess(config.acp_command, config.cwd);

  await agent.start();
  const initResult = await handshake(agent);

  // Stdio relay: the only interface. Parent (telegram-adapter) communicates via stdin/stdout.
  const relay = new StdioRelay(agent, config.cwd, { exitOnClose: true });
  relay.setInitResult(initResult);
  relay.start();

  // Auto-respawn: when the inner agent (pi-acp) crashes, respawn it after a delay.
  // The relay already notifies the parent of the exit via __system__ session update.
  agent.on("exit", (code: number | null, signal: string | null) => {
    if (shuttingDown) return;
    log.info({ code, signal, delayMs: RESPAWN_DELAY_MS }, "scheduling agent respawn");
    setTimeout(() => respawn(), RESPAWN_DELAY_MS);
  });

  async function respawn(): Promise<void> {
    if (shuttingDown) return;
    try {
      await agent.start();
      const newInitResult = await handshake(agent);
      relay.setInitResult(newInitResult);
      log.info("agent respawned successfully");
    } catch (err) {
      log.error({ err }, "agent respawn failed, retrying...");
      setTimeout(() => respawn(), RESPAWN_DELAY_MS);
    }
  }

  process.on("SIGTERM", async () => {
    log.info("shutting down");
    shuttingDown = true;
    await agent.stop();
    process.exit(0);
  });
}

main().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});
