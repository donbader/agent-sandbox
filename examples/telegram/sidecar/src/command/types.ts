/**
 * Command plugin interface for channel-manager.
 * Feature plugins contribute command modules that get wired in at build time.
 */
import type { PluginLogger } from "../logger.js";

export type CommandReply = (msg: string) => void;

export interface CommandContext {
  args: string;
  chatId: string;
  reply: CommandReply;
}

export type CommandHandler = (ctx: CommandContext) => Promise<void>;

export interface CommandPlugin {
  name: string;
  /** Map of command name (without /) to handler */
  commands: Record<string, CommandHandler>;
  /** Initialize with config and a logger tagged with the plugin instance name. */
  init?(config: Record<string, unknown>, logger: PluginLogger): void;
  /** Intercept non-command messages (e.g. OAuth callback paste-back). Return true if handled. */
  onMessage?(text: string, chatId: string, reply: CommandReply): Promise<boolean>;
  /** Cleanup on shutdown */
  destroy?(): void;
}
