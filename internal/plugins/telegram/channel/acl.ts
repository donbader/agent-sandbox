/**
 * Access control for Telegram channel.
 * Determines whether a user in a chat is allowed to interact with the bot.
 */
import { createLogger } from "../logger.js";

const log = createLogger("telegram:acl");

export interface GroupACL {
  allowed_users?: string[];
  require_mention?: boolean;
}

export interface AccessControlConfig {
  allowed_users?: string[];
  require_mention?: boolean;
  groups?: Record<string, GroupACL>;
}

export interface MessageContext {
  chatId: number;
  username: string | null;
  isGroup: boolean;
  text: string;
  botUsername: string | null;
}

/**
 * Check whether a message should be processed based on ACL rules.
 * Returns true if the message is allowed, false if it should be ignored.
 */
export function isAllowed(ctx: MessageContext, acl: AccessControlConfig): boolean {
  const chatIdStr = ctx.chatId.toString();
  const groupAcl = acl.groups?.[chatIdStr];
  const allowedUsers = groupAcl?.allowed_users ?? acl.allowed_users;
  const requireMention = groupAcl?.require_mention ?? acl.require_mention ?? false;

  // User allowlist check
  if (allowedUsers?.length && ctx.username) {
    if (!allowedUsers.includes(ctx.username)) {
      log.debug({ username: ctx.username, chatId: ctx.chatId }, "ignoring unauthorized user");
      return false;
    }
  }

  // Mention check for groups
  if (ctx.isGroup && requireMention && ctx.botUsername) {
    if (!ctx.text.includes(`@${ctx.botUsername}`)) {
      return false;
    }
  }

  return true;
}
