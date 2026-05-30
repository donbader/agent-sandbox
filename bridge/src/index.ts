import { readFileSync } from "node:fs";
import { AgentProcess } from "./agent-process.js";
import { TelegramChannel } from "./channel/telegram.js";
import type { Channel } from "./channel/types.js";

interface BridgeConfig {
  channel: string;
  agent_cmd: string[];
  allowed_chat_ids?: string[];
}

function loadConfig(): BridgeConfig {
  const configPath = process.env.BRIDGE_CONFIG || "/opt/bridge/config.json";
  const data = readFileSync(configPath, "utf-8");
  return JSON.parse(data) as BridgeConfig;
}

async function main(): Promise<void> {
  const config = loadConfig();
  console.log(`bridge: starting channel=${config.channel} cmd=${config.agent_cmd.join(" ")}`);

  // Start agent process
  const agent = new AgentProcess(config.agent_cmd);
  agent.start();

  // Create channel
  let channel: Channel;
  if (config.channel === "telegram") {
    channel = new TelegramChannel(config.allowed_chat_ids);
  } else {
    console.error(`bridge: unknown channel type: ${config.channel}`);
    process.exit(1);
  }

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
    console.log("bridge: shutting down");
    channel.stop();
    agent.stop();
    process.exit(0);
  });
}

main().catch((err) => {
  console.error("bridge: fatal:", err);
  process.exit(1);
});
