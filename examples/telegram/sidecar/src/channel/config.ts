/**
 * Typed configuration for the Telegram channel plugin.
 * Validates and normalizes raw config from channel-manager config.json.
 */
import type { ReactionTypeEmoji } from "@grammyjs/types";

type ReactionEmoji = ReactionTypeEmoji["emoji"];

export const VALID_REACTION_EMOJIS: Set<string> = new Set([
  "👍", "👎", "❤", "🔥", "🥰", "👏", "😁", "🤔", "🤯", "😱",
  "🤬", "😢", "🎉", "🤩", "🤮", "💩", "🙏", "👌", "🕊", "🤡",
  "🥱", "🥴", "😍", "🐳", "❤\u200D🔥", "🌚", "🌭", "💯", "🤣", "⚡",
  "🍌", "🏆", "💔", "🤨", "😐", "🍓", "🍾", "💋", "🖕", "😈",
  "😴", "😭", "🤓", "👻", "👨\u200D💻", "👀", "🎃", "🙈", "😇", "😨",
  "🤝", "✍", "🤗", "🫡", "🎅", "🎄", "☃", "💅", "🤪", "🗿",
  "🆒", "💘", "🙉", "🦄", "😘", "💊", "🙊", "😎", "👾",
  "🤷\u200D♂", "🤷", "🤷\u200D♀", "😡",
]);

export function isValidReactionEmoji(emoji: string): emoji is ReactionEmoji {
  return VALID_REACTION_EMOJIS.has(emoji);
}

export interface TelegramChannelConfig {
  accessControl: {
    allowed_users?: string[];
    require_mention?: boolean;
    groups?: Record<string, { allowed_users?: string[]; require_mention?: boolean }>;
  };
  ackEmoji: ReactionEmoji | null;
}

/**
 * Parse and validate raw channel config into typed TelegramChannelConfig.
 * Fails fast with clear errors on invalid config.
 */
export function parseConfig(raw: Record<string, unknown>): TelegramChannelConfig {
  const accessControl = (raw?.access_control as TelegramChannelConfig["accessControl"]) ?? {};

  const ackRaw = raw?.ack_emoji;
  let ackEmoji: ReactionEmoji | null;

  if (ackRaw === undefined) {
    ackEmoji = "👀";
  } else if (typeof ackRaw === "string" && isValidReactionEmoji(ackRaw)) {
    ackEmoji = ackRaw;
  } else if (!ackRaw) {
    ackEmoji = null;
  } else {
    throw new Error(
      `Invalid ack_emoji: ${JSON.stringify(ackRaw)}. Must be a valid Telegram reaction emoji or empty string to disable.`
    );
  }

  return { accessControl, ackEmoji };
}
