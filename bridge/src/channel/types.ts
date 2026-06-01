export interface CommandDef {
  name: string;
  description: string;
}

/**
 * Channel is the interface for messaging channels (Telegram, Slack, etc).
 */
export interface Channel {
  /** Start the channel (e.g., begin polling). */
  start(): Promise<void>;

  /** Stop the channel gracefully. */
  stop(): void;

  /** Register a handler for incoming messages. */
  onMessage(handler: (chatId: string, text: string) => void): void;

  /** Send a message to a specific chat. */
  sendMessage(chatId: string, text: string): void;

  /** Register commands with the platform (e.g., Telegram bot menu). Optional. */
  registerCommands?(commands: CommandDef[]): Promise<void>;
}
