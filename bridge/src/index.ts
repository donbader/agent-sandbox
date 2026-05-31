import { readFileSync } from "node:fs";
import { AcpAgent } from "./acp-client.js";
import { channels } from "./channel/channels.gen.js";
import type { Channel } from "./channel/types.js";
import { createLogger } from "./logger.js";

const log = createLogger("bridge");

interface BridgeConfig {
  channel: string;
  acp_command: string[];
  cwd?: string;
  [key: string]: unknown; // plugin-specific config passed to channel constructor
}

function loadConfig(): BridgeConfig {
  const configPath = process.env.BRIDGE_CONFIG || "/opt/bridge/config.json";
  const data = readFileSync(configPath, "utf-8");
  return JSON.parse(data) as BridgeConfig;
}

async function main(): Promise<void> {
  const config = loadConfig();

  if (!config.acp_command || config.acp_command.length === 0) {
    log.fatal("acp_command is required in bridge config");
    process.exit(1);
  }

  // Create channel from generated registry
  const ChannelClass = channels[config.channel];
  if (!ChannelClass) {
    log.error(
      { channel: config.channel, available: Object.keys(channels) },
      "unknown channel type"
    );
    process.exit(1);
  }
  const channel: Channel = new ChannelClass(config);

  log.info(
    { channel: config.channel, cmd: config.acp_command.join(" ") },
    "starting channel"
  );
  const agent = new AcpAgent({
    cmd: config.acp_command,
    cwd: config.cwd ?? process.cwd(),
  });

  // Start agent in background — don't block channel startup
  agent.start().catch((err: unknown) => {
    log.error({ error: err }, "ACP agent failed to start (will retry on next message)");
  });

  // Wire channel → agent
  channel.onMessage((chatId, text) => {
    agent
      .prompt(text)
      .then((response) => {
        channel.sendMessage(chatId, response);
      })
      .catch((err: unknown) => {
        log.error({ error: err, chatId }, "agent prompt failed");
      });
  });

  // Start channel
  await channel.start();

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
