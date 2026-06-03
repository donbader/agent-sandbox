import { readFileSync } from "node:fs";
import { AcpAgent } from "./acp-client.js";
import { handleWrapperCommand, WRAPPER_COMMANDS } from "./wrapper-commands.js";
import { createLogger, createPluginLogger } from "./logger.js";
import { registerPlugin, handleCommand, handleMessage } from "./command/registry.js";

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

  if (!config.cwd) {
    log.fatal("cwd is required in channel-manager config");
    process.exit(1);
  }

  log.info(
    { channel: config.channel, cmd: config.acp_command.join(" ") },
    "starting channel manager"
  );

  // Create ACP agent
  const agent = new AcpAgent({
    cmd: config.acp_command,
    cwd: config.cwd,
  });

  // Load command plugins (if any are generated)
  const pluginCommandNames: Array<{ name: string; description: string }> = [];
  try {
    const { commandPlugins } = await import("./command/commands.gen.js");
    for (const plugin of commandPlugins) {
      const pluginLogger = createPluginLogger(`plugin:${plugin.name}`);
      plugin.init?.(config, pluginLogger);
      registerPlugin(plugin);
      for (const cmdName of Object.keys(plugin.commands)) {
        pluginCommandNames.push({ name: cmdName, description: `${plugin.name} plugin command` });
      }
    }
    log.info({ plugins: commandPlugins.map((p: { name: string }) => p.name) }, "loaded command plugins");
  } catch {
    // No command plugins generated — that's fine
  }

  // Register static commands (wrapper + plugin) so channels can expose them in menus
  agent.registerStaticCommands([
    ...WRAPPER_COMMANDS.map((c) => ({ name: c.name, description: c.description })),
    ...pluginCommandNames,
  ]);

  // Set up prompt interceptor: wrapper commands → command plugins → agent
  agent.setPromptInterceptor(async (text: string, sessionId: string) => {
    // 1. Sync wrapper commands (/sh, /diagnose)
    const wrapperResult = handleWrapperCommand(text, {
      agentCmd: config.acp_command,
      perfHistory: agent.perfHistory,
      cwd: config.cwd,
    });
    if (wrapperResult !== null) return wrapperResult;

    // 2. Async command plugins (/oauth, etc.)
    const commandResult = await handleCommand(text, sessionId, () => {});
    if (commandResult !== null) return commandResult;

    // 3. onMessage interceptors (paste-back)
    let messageResponse = "";
    const handled = await handleMessage(text, sessionId, (msg: string) => { messageResponse = msg; });
    if (handled) return messageResponse || null;

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
