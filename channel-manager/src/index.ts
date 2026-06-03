import { readFileSync } from "node:fs";
import { AcpAgent } from "./acp-client.js";
import { handleWrapperCommand } from "./wrapper-commands.js";
import { createLogger, createPluginLogger } from "./logger.js";
import type { CommandPlugin } from "./command/types.js";

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

  // Load command plugins (if any are generated)
  const commandPlugins: CommandPlugin[] = [];
  try {
    const { commandPlugins: plugins } = await import("./command/commands.gen.js");
    for (const plugin of plugins) {
      const pluginLogger = createPluginLogger(`plugin:${plugin.name}`);
      plugin.init?.(config, pluginLogger);
      commandPlugins.push(plugin);
    }
    log.info({ plugins: commandPlugins.map((p) => p.name) }, "loaded command plugins");
  } catch {
    // No command plugins generated — that's fine
  }

  // Set up prompt interceptor: wrapper commands → command plugins → agent
  agent.setPromptInterceptor(async (text: string, _sessionId: string) => {
    // 1. Sync wrapper commands (/sh, /diagnose)
    const wrapperResult = handleWrapperCommand(text, {
      agentCmd: config.acp_command,
      perfHistory: [],
    });
    if (wrapperResult !== null) return wrapperResult;

    // 2. Async command plugins (/oauth, etc.)
    const trimmed = text.trim();
    if (trimmed.startsWith("/")) {
      const [cmd, ...args] = trimmed.slice(1).split(/\s+/);
      for (const plugin of commandPlugins) {
        if (cmd in plugin.commands) {
          let response = "";
          await plugin.commands[cmd]({
            args: args.join(" "),
            chatId: _sessionId,
            reply: (msg: string) => { response = msg; },
          });
          return response || null;
        }
      }
    }

    // 3. onMessage interceptors (paste-back)
    for (const plugin of commandPlugins) {
      if (plugin.onMessage) {
        let response = "";
        const handled = await plugin.onMessage(text, _sessionId, (msg: string) => { response = msg; });
        if (handled) return response || null;
      }
    }

    return null; // Not intercepted — forward to agent
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
