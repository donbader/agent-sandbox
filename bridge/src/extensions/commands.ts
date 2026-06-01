import type { BridgeExtension, ExtensionContext, ChatId } from "../extension.js";
import { createLogger } from "../logger.js";

const log = createLogger("commands");

const commandsExtension: BridgeExtension = {
  name: "commands",
  commands: {
    new: {
      description: "Start a new conversation (reset agent session)",
      async handler(ctx: ExtensionContext, chatId: ChatId, _args: string) {
        try {
          await ctx.agent.reset(chatId);
          return "✨ New session started.";
        } catch (err) {
          log.error({ error: err }, "/new failed");
          return "❌ Failed to reset session.";
        }
      },
    },
    stop: {
      description: "Stop the current operation",
      async handler(ctx: ExtensionContext, _chatId: ChatId, _args: string) {
        ctx.agent.abort();
        return "⏹ Stopped.";
      },
    },
    status: {
      description: "Show agent status",
      async handler(ctx: ExtensionContext, _chatId: ChatId, _args: string) {
        const ready = ctx.agent.isReady();
        return ready ? "✅ Agent: connected" : "⏳ Agent: starting...";
      },
    },
  },
};

export default commandsExtension;
