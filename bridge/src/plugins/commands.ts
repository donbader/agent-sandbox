import type { BridgePlugin, PluginContext, ChatId } from "../plugin.js";

/** Core bot commands: /new, /stop, /status, /version, /help */
const commandsPlugin: BridgePlugin = {
  name: "core-commands",
  commands: {
    new: {
      description: "Start a new conversation",
      async handler(_ctx: PluginContext, _chatId: ChatId) {
        return "✨ New session started.";
      },
    },
    stop: {
      description: "Stop the current operation",
      async handler(_ctx: PluginContext, _chatId: ChatId) {
        return "⏹ Stopped.";
      },
    },
    status: {
      description: "Show current status",
      async handler(_ctx: PluginContext, _chatId: ChatId) {
        return "📊 Status: ready";
      },
    },
    version: {
      description: "Show bridge version",
      async handler() {
        return "🏗 agent-sandbox bridge v0.22.0";
      },
    },
    help: {
      description: "Show available commands",
      async handler(_ctx: PluginContext, _chatId: ChatId) {
        return "Use /help to see all commands";
      },
    },
  },
};

export default commandsPlugin;
