/**
 * Command plugin registry — routes commands and messages to registered plugins.
 */
import type { CommandPlugin, CommandReply } from "./types.js";
import type { PluginLogger } from "../logger.js";

const plugins: CommandPlugin[] = [];

export function registerPlugin(plugin: CommandPlugin): void {
  plugins.push(plugin);
}

/**
 * Attempt to handle a slash command. Parses the first word as command name,
 * routes to the appropriate plugin handler.
 * Returns response string if handled, null otherwise.
 */
export async function handleCommand(
  text: string,
  chatId: string,
  reply: CommandReply,
): Promise<string | null> {
  const trimmed = text.trim();
  if (!trimmed.startsWith("/")) return null;

  const spaceIdx = trimmed.indexOf(" ");
  const cmdName = spaceIdx === -1 ? trimmed.slice(1) : trimmed.slice(1, spaceIdx);
  const args = spaceIdx === -1 ? "" : trimmed.slice(spaceIdx + 1).trim();

  for (const plugin of plugins) {
    const handler = plugin.commands[cmdName];
    if (handler) {
      let response: string | null = null;
      const captureReply: CommandReply = (msg) => {
        response = msg;
        reply(msg);
      };
      await handler({ args, chatId, reply: captureReply });
      return response;
    }
  }

  return null;
}

/**
 * Route non-command messages through plugin interceptors (e.g. paste-back).
 * Returns true if any plugin handled the message.
 */
export async function handleMessage(
  text: string,
  chatId: string,
  reply: CommandReply,
): Promise<boolean> {
  for (const plugin of plugins) {
    if (plugin.onMessage) {
      const handled = await plugin.onMessage(text, chatId, reply);
      if (handled) return true;
    }
  }
  return false;
}

/**
 * Initialize all registered plugins with config and a logger.
 */
export function initPlugins(config: Record<string, unknown>, createLogger: (name: string) => PluginLogger): void {
  for (const plugin of plugins) {
    if (plugin.init) {
      plugin.init(config, createLogger(plugin.name));
    }
  }
}

/**
 * Reset registry state (for testing).
 */
export function resetRegistry(): void {
  plugins.length = 0;
}
