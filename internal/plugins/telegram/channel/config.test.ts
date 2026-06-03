import { describe, it, expect } from "vitest";
import { parseConfig, isValidReactionEmoji } from "./config.js";

describe("parseConfig", () => {
  it("returns defaults when config is empty", () => {
    const result = parseConfig({});
    expect(result.ackEmoji).toBe("👀");
    expect(result.accessControl).toEqual({});
  });

  it("parses valid ack_emoji", () => {
    const result = parseConfig({ ack_emoji: "🔥" });
    expect(result.ackEmoji).toBe("🔥");
  });

  it("disables ack when ack_emoji is empty string", () => {
    const result = parseConfig({ ack_emoji: "" });
    expect(result.ackEmoji).toBeNull();
  });

  it("throws on invalid ack_emoji", () => {
    expect(() => parseConfig({ ack_emoji: "not-an-emoji" })).toThrow("Invalid ack_emoji");
  });

  it("parses access_control", () => {
    const result = parseConfig({
      access_control: {
        allowed_users: ["@alice"],
        require_mention: true,
        groups: { "123": { allowed_users: ["@bob"] } },
      },
    });
    expect(result.accessControl.allowed_users).toEqual(["@alice"]);
    expect(result.accessControl.require_mention).toBe(true);
    expect(result.accessControl.groups?.["123"]).toEqual({ allowed_users: ["@bob"] });
  });

  it("defaults access_control to empty object when missing", () => {
    const result = parseConfig({ ack_emoji: "👍" });
    expect(result.accessControl).toEqual({});
  });
});

describe("isValidReactionEmoji", () => {
  it("returns true for valid Telegram reaction emojis", () => {
    expect(isValidReactionEmoji("👀")).toBe(true);
    expect(isValidReactionEmoji("🔥")).toBe(true);
    expect(isValidReactionEmoji("👍")).toBe(true);
  });

  it("returns false for non-reaction emojis", () => {
    expect(isValidReactionEmoji("🥺")).toBe(false);
    expect(isValidReactionEmoji("abc")).toBe(false);
    expect(isValidReactionEmoji("")).toBe(false);
  });
});
