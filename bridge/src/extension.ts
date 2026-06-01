import { createLogger } from "./logger.js";

// --- Types ---

export type ChatId = string;

/** Generic agent event (ACP session updates). */
export interface AgentEvent {
  type: string;
  [key: string]: unknown;
}

/** Command handler for bot commands (e.g., /status, /new). */
export interface CommandHandler {
  description?: string;
  handler(ctx: ExtensionContext, chatId: ChatId, args: string): Promise<string | void>;
}

/** Bridge plugin interface. Plugins extend bridge behavior via hooks and commands. */
export interface BridgeExtension {
  name: string;
  /** Bot commands this plugin provides. Key = command name without slash. */
  commands?: Record<string, CommandHandler>;
  /** Called once when the bridge boots. */
  onBoot?(ctx: ExtensionContext): Promise<void>;
  /** Called when a turn starts (user message received). */
  onTurnStart?(ctx: ExtensionContext, chatId: ChatId): void;
  /** Called when a turn ends (agent response complete). */
  onTurnEnd?(ctx: ExtensionContext, chatId: ChatId): void;
  /** Called for agent events (ACP session updates). */
  onEvent?(ctx: ExtensionContext, chatId: ChatId, event: AgentEvent): void;
}

/** Controlled API surface exposed to plugins. */
export interface ExtensionContext {
  /** Send a message to a chat (through the channel). */
  sendMessage(chatId: ChatId, text: string): void;
  /** Get bridge config. */
  readonly config: Record<string, unknown>;
}

/** Plugin registry — manages plugins, dispatches events, routes commands. */
export class ExtensionRegistry {
  private plugins: BridgeExtension[] = [];
  private commandMap = new Map<string, { plugin: BridgeExtension; handler: CommandHandler }>();
  private log = createLogger("extensions");

  register(plugin: BridgeExtension): void {
    this.plugins.push(plugin);
    if (plugin.commands) {
      for (const [name, handler] of Object.entries(plugin.commands)) {
        if (this.commandMap.has(name)) {
          this.log.warn(
            { command: name, existing: this.commandMap.get(name)!.plugin.name, skipped: plugin.name },
            "command already registered"
          );
          continue;
        }
        this.commandMap.set(name, { plugin, handler });
      }
    }
    this.log.info(
      { plugin: plugin.name, commands: plugin.commands ? Object.keys(plugin.commands) : [] },
      "registered plugin"
    );
  }

  getCommand(name: string): CommandHandler | undefined {
    return this.commandMap.get(name)?.handler;
  }

  getCommandNames(): string[] {
    return [...this.commandMap.keys()];
  }

  getPlugins(): readonly BridgeExtension[] {
    return this.plugins;
  }

  async boot(ctx: ExtensionContext): Promise<void> {
    for (const plugin of this.plugins) {
      if (plugin.onBoot) {
        try {
          await plugin.onBoot(ctx);
        } catch (err) {
          this.log.error({ plugin: plugin.name, error: err }, "onBoot failed");
        }
      }
    }
  }

  notifyTurnStart(ctx: ExtensionContext, chatId: ChatId): void {
    for (const plugin of this.plugins) {
      if (plugin.onTurnStart) {
        try {
          plugin.onTurnStart(ctx, chatId);
        } catch (err) {
          this.log.error({ plugin: plugin.name, error: err }, "onTurnStart error");
        }
      }
    }
  }

  notifyTurnEnd(ctx: ExtensionContext, chatId: ChatId): void {
    for (const plugin of this.plugins) {
      if (plugin.onTurnEnd) {
        try {
          plugin.onTurnEnd(ctx, chatId);
        } catch (err) {
          this.log.error({ plugin: plugin.name, error: err }, "onTurnEnd error");
        }
      }
    }
  }

  notifyEvent(ctx: ExtensionContext, chatId: ChatId, event: AgentEvent): void {
    for (const plugin of this.plugins) {
      if (plugin.onEvent) {
        try {
          plugin.onEvent(ctx, chatId, event);
        } catch (err) {
          this.log.error({ plugin: plugin.name, error: err }, "onEvent error");
        }
      }
    }
  }
}
