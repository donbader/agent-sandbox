import { readFileSync } from "node:fs";
import { AcpAgent } from "./acp-client.js";
import { SessionStore } from "./session-store.js";
import { SessionManager } from "./session-manager.js";
import { channels } from "./channel/channels.gen.js";
import type { Channel } from "./channel/types.js";
import { createLogger } from "./logger.js";
import { ExtensionRegistry } from "./extension.js";
import type { ExtensionContext } from "./extension.js";
import perfPlugin from "./extensions/perf-tracker.js";
import eventLoggerPlugin from "./extensions/event-logger.js";
import commandsExtension from "./extensions/commands.js";
import { StartupBuffer } from "./startup-buffer.js";

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

  // Set up plugin registry
  const registry = new ExtensionRegistry();
  registry.register(perfPlugin);
  registry.register(eventLoggerPlugin);
  registry.register(commandsExtension);

  log.info(
    { channel: config.channel, cmd: config.acp_command.join(" ") },
    "starting channel"
  );

  const agent = new AcpAgent({
    cmd: config.acp_command,
    cwd: config.cwd ?? process.cwd(),
  });

  const store = new SessionStore();
  const sessionManager = new SessionManager({
    getConnection: () => {
      const conn = agent.getConnection();
      if (!conn) throw new Error("agent not connected");
      return conn;
    },
    cwd: config.cwd ?? process.cwd(),
    store,
  });

  const ctx: ExtensionContext = {
    sendMessage: (chatId, text) => channel.sendMessage(chatId, text),
    config: config as Record<string, unknown>,
    agent: {
      isReady: () => agent.isReady(),
      reset: async (chatId: string) => {
        await sessionManager.resetSession(chatId);
      },
      abort: () => agent.abort(),
    },
    sessions: {
      getHistory: (chatId) => store.getHistory(chatId),
      getActiveSessionId: (chatId) => sessionManager.getSessionId(chatId),
      resumeSession: (chatId, sessionId) => sessionManager.resumeSession(chatId, sessionId),
      resetSession: (chatId) => sessionManager.resetSession(chatId),
      labelSession: (chatId, sessionId, label) => store.setLabel(chatId, sessionId, label),
      findByPrefix: (chatId, prefix) => store.findByPrefix(chatId, prefix),
    },
  };

  const startupBuffer = new StartupBuffer();

  // Start agent in background — signal buffer when ready
  agent.start()
    .then(() => {
      startupBuffer.ready();
    })
    .catch((err: unknown) => {
      log.error({ error: err }, "ACP agent failed to start (will retry on next message)");
      // Mark buffer ready anyway so messages aren't stuck forever.
      // They'll get errors from sessionManager but at least users get feedback.
      startupBuffer.ready();
    });

  // Wire startup buffer → agent (with plugin command routing)
  startupBuffer.onMessage((chatId, text) => {
    if (text.startsWith("/")) {
      const spaceIdx = text.indexOf(" ");
      const cmd = spaceIdx === -1 ? text.slice(1) : text.slice(1, spaceIdx);
      const args = spaceIdx === -1 ? "" : text.slice(spaceIdx + 1).trim();

      // /help always generates a dynamic list of all registered commands
      if (cmd === "help") {
        const names = registry.getCommandNames();
        const lines = ["Available commands:", ""];
        for (const name of names) {
          const handler = registry.getCommand(name);
          lines.push(`/${name}${handler?.description ? ` — ${handler.description}` : ""}`);
        }
        // Show agent-declared commands
        const agentCmds = agent.getAgentCommands();
        const coreNames = new Set(names);
        const agentOnly = agentCmds.filter((c) => !coreNames.has(c.name));
        if (agentOnly.length > 0) {
          lines.push("", "Agent commands:", "");
          for (const c of agentOnly) {
            lines.push(`/${c.name}${c.description ? ` — ${c.description}` : ""}`);
          }
        }
        channel.sendMessage(chatId, lines.join("\n"));
        return;
      }

      const handler = registry.getCommand(cmd);
      if (handler) {
        handler
          .handler(ctx, chatId, args)
          .then((response) => {
            if (response) channel.sendMessage(chatId, response);
          })
          .catch((err: unknown) => {
            log.error({ error: err, chatId, cmd }, "command handler failed");
            channel.sendMessage(chatId, "Command failed.");
          });
      } else {
        // Not a core command — forward to agent as prompt (agent handles its own commands)
        sessionManager.getSession(chatId)
          .then((sessionId) => {
            registry.notifyTurnStart(ctx, chatId);
            return agent.prompt(sessionId, text).then((response) => {
              registry.notifyTurnEnd(ctx, chatId);
              channel.sendMessage(chatId, response);
            });
          })
          .catch((err: unknown) => {
            registry.notifyTurnEnd(ctx, chatId);
            log.error({ error: err, chatId }, "agent prompt failed");
            channel.sendMessage(chatId, "⚠️ Agent unavailable. Try again shortly.");
          });
      }
      return;
    }

    sessionManager.getSession(chatId)
      .then((sessionId) => {
        registry.notifyTurnStart(ctx, chatId);
        return agent.prompt(sessionId, text).then((response) => {
          registry.notifyTurnEnd(ctx, chatId);
          channel.sendMessage(chatId, response);
        });
      })
      .catch((err: unknown) => {
        registry.notifyTurnEnd(ctx, chatId);
        log.error({ error: err, chatId }, "agent prompt failed");
        channel.sendMessage(chatId, "⚠️ Agent unavailable. Try again shortly.");
      });
  });

  // Wire channel → startup buffer
  channel.onMessage((chatId, text) => {
    startupBuffer.push(chatId, text);
  });

  // Start channel
  await channel.start();

  // Boot plugins after channel is ready
  await registry.boot(ctx);

  // Register commands with channel platform (e.g., Telegram bot menu)
  const registerAllCommands = (): void => {
    if (!channel.registerCommands) return;
    const commands = registry.getCommandNames().map((name) => {
      const cmd = registry.getCommand(name);
      return { name, description: cmd?.description ?? "" };
    });
    // Add agent-declared commands (skip those that overlap with core)
    const coreNames = new Set(commands.map((c) => c.name));
    for (const agentCmd of agent.getAgentCommands()) {
      if (!coreNames.has(agentCmd.name)) {
        commands.push({ name: agentCmd.name, description: agentCmd.description });
      }
    }
    // Always include /help
    if (!commands.some((c) => c.name === "help")) {
      commands.push({ name: "help", description: "List all available commands" });
    }
    channel.registerCommands(commands).catch((err: unknown) => {
      log.warn({ error: err }, "failed to register commands with channel");
    });
  };

  registerAllCommands();

  // Re-register when agent declares its commands
  agent.onCommandsUpdate(() => {
    log.info("agent commands updated, re-registering bot menu");
    registerAllCommands();
  });

  // Handle shutdown
  process.on("SIGTERM", () => {
    log.info("shutting down");
    store.flushSync();
    channel.stop();
    agent.stop();
    process.exit(0);
  });
}

main().catch((err) => {
  log.fatal({ error: err }, "fatal error");
  process.exit(1);
});
