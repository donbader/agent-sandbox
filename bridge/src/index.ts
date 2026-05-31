import { readFileSync } from "node:fs";
import { AgentProcess } from "./agent-process.js";
import { channels } from "./channel/channels.gen.js";
import type { Channel } from "./channel/types.js";
import { createLogger } from "./logger.js";

const log = createLogger("bridge");

interface BridgeConfig {
  channel: string;
  agent_cmd: string[];
  [key: string]: unknown; // plugin-specific config passed to channel constructor
}

function loadConfig(): BridgeConfig {
  const configPath = process.env.BRIDGE_CONFIG || "/opt/bridge/config.json";
  const data = readFileSync(configPath, "utf-8");
  return JSON.parse(data) as BridgeConfig;
}

async function main(): Promise<void> {
  const config = loadConfig();
  log.info({ channel: config.channel, cmd: config.agent_cmd.join(" ") }, "starting channel");

  // Start agent process
  const agent = new AgentProcess(config.agent_cmd);
  agent.start();

  // Create channel from generated registry
  const ChannelClass = channels[config.channel];
  if (!ChannelClass) {
    log.error({ channel: config.channel, available: Object.keys(channels) }, "unknown channel type");
    process.exit(1);
  }
  const channel: Channel = new ChannelClass(config);

  // Wire channel → agent
  channel.onMessage((chatId, text) => {
    agent.send({ type: "message", chat_id: chatId, text });
  });

  // Wire agent → channel
  agent.onMessage((msg) => {
    if (msg.type === "response" && msg.chat_id && msg.text) {
      channel.sendMessage(msg.chat_id, msg.text);
    }
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
