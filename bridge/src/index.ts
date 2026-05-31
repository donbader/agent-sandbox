import { readFileSync } from "node:fs";
import { AcpAgent } from "./acp-client.js";
import { AgentProcess } from "./legacy-agent-process.js";
import { channels } from "./channel/channels.gen.js";
import type { Channel } from "./channel/types.js";
import { createLogger } from "./logger.js";

const log = createLogger("bridge");

interface BridgeConfig {
  channel: string;
  agent_cmd: string[];
  acp_command?: string[];
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

  let stopAgent: () => void;

  if (config.acp_command) {
    // ACP mode — use standard Agent Client Protocol
    log.info(
      { channel: config.channel, cmd: config.acp_command.join(" ") },
      "starting channel (ACP mode)"
    );
    const agent = new AcpAgent({
      cmd: config.acp_command,
      cwd: config.cwd ?? process.cwd(),
    });
    await agent.start();

    // Wire channel → agent (fire-and-forget; errors are logged)
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

    stopAgent = () => agent.stop();
  } else {
    // Legacy mode — custom JSON lines over stdio
    log.info(
      { channel: config.channel, cmd: config.agent_cmd.join(" ") },
      "starting channel (legacy mode)"
    );
    const agent = new AgentProcess(config.agent_cmd);
    agent.start();

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

    stopAgent = () => agent.stop();
  }

  // Start channel
  await channel.start();

  // Handle shutdown
  process.on("SIGTERM", () => {
    log.info("shutting down");
    channel.stop();
    stopAgent();
    process.exit(0);
  });
}

main().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});
