import { readFileSync } from "node:fs";
import { AcpAgent } from "./acp-client.js";
import { createLogger } from "./logger.js";

const log = createLogger("channel-manager");

interface BridgeConfig {
  channel: string;
  acp_command: string[];
  cwd: string;
  [key: string]: unknown;
}

async function main(): Promise<void> {
  const configPath = process.env.CHANNEL_MANAGER_CONFIG ?? "/opt/channel-manager/config.json";
  const raw = readFileSync(configPath, "utf-8");
  const config: BridgeConfig = JSON.parse(raw);

  if (!config.acp_command || config.acp_command.length === 0) {
    log.fatal("acp_command is required in channel-manager config");
    process.exit(1);
  }

  log.info(
    { channel: config.channel, cmd: config.acp_command.join(" ") },
    "starting channel manager"
  );

  // Create ACP agent
  const agent = new AcpAgent({
    cmd: config.acp_command,
    cwd: config.cwd ?? "/workspace",
  });

  // Dynamically load channel (generated registry)
  const { loadChannel } = await import("./channel/channels.gen.js");
  const channel = loadChannel(config.channel, config, agent);

  // Start agent first, then channel
  await agent.start();
  await channel.start();

  log.info("channel manager ready");

  // Handle shutdown
  process.on("SIGTERM", () => {
    log.info("shutting down");
    channel.stop();
    agent.stop();
    process.exit(0);
  });
}

main().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});
