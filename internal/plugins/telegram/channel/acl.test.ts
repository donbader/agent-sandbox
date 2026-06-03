import { describe, it, expect, vi } from "vitest";
import { isAllowed, type AccessControlConfig, type MessageContext } from "./acl.js";

vi.mock("../logger.js", () => ({
  createLogger: () => ({
    info: vi.fn(),
    debug: vi.fn(),
    error: vi.fn(),
    warn: vi.fn(),
  }),
}));

function makeCtx(overrides: Partial<MessageContext> = {}): MessageContext {
  return {
    chatId: 123,
    username: "@alice",
    isGroup: false,
    text: "hello",
    botUsername: "testbot",
    ...overrides,
  };
}

describe("isAllowed", () => {
  describe("no ACL configured", () => {
    it("allows all messages when ACL is empty", () => {
      expect(isAllowed(makeCtx(), {})).toBe(true);
    });

    it("allows messages with null username when no allowlist", () => {
      expect(isAllowed(makeCtx({ username: null }), {})).toBe(true);
    });
  });

  describe("user allowlist", () => {
    const acl: AccessControlConfig = { allowed_users: ["@alice", "@bob"] };

    it("allows users in the allowlist", () => {
      expect(isAllowed(makeCtx({ username: "@alice" }), acl)).toBe(true);
      expect(isAllowed(makeCtx({ username: "@bob" }), acl)).toBe(true);
    });

    it("denies users not in the allowlist", () => {
      expect(isAllowed(makeCtx({ username: "@eve" }), acl)).toBe(false);
    });

    it("denies users with null username when allowlist is configured", () => {
      expect(isAllowed(makeCtx({ username: null }), acl)).toBe(false);
    });
  });

  describe("group-specific overrides", () => {
    const acl: AccessControlConfig = {
      allowed_users: ["@alice"],
      groups: {
        "456": { allowed_users: ["@bob", "@carol"] },
      },
    };

    it("uses group-specific allowlist when chatId matches", () => {
      // @bob is in group 456's list but not top-level
      expect(isAllowed(makeCtx({ chatId: 456, username: "@bob" }), acl)).toBe(true);
    });

    it("denies top-level users not in group override", () => {
      // @alice is in top-level but not in group 456's override
      expect(isAllowed(makeCtx({ chatId: 456, username: "@alice" }), acl)).toBe(false);
    });

    it("falls back to top-level when chatId has no group override", () => {
      expect(isAllowed(makeCtx({ chatId: 789, username: "@alice" }), acl)).toBe(true);
      expect(isAllowed(makeCtx({ chatId: 789, username: "@eve" }), acl)).toBe(false);
    });
  });

  describe("require_mention", () => {
    const acl: AccessControlConfig = { require_mention: true };

    it("allows messages mentioning the bot in groups", () => {
      expect(isAllowed(
        makeCtx({ isGroup: true, text: "hey @testbot what do you think?" }),
        acl,
      )).toBe(true);
    });

    it("denies group messages without mention", () => {
      expect(isAllowed(
        makeCtx({ isGroup: true, text: "hey everyone" }),
        acl,
      )).toBe(false);
    });

    it("allows DMs regardless of mention", () => {
      expect(isAllowed(
        makeCtx({ isGroup: false, text: "no mention here" }),
        acl,
      )).toBe(true);
    });

    it("allows group messages when botUsername is null (not yet connected)", () => {
      expect(isAllowed(
        makeCtx({ isGroup: true, text: "no mention", botUsername: null }),
        acl,
      )).toBe(true);
    });
  });

  describe("group-specific require_mention override", () => {
    const acl: AccessControlConfig = {
      require_mention: false,
      groups: {
        "456": { require_mention: true },
      },
    };

    it("uses group-level require_mention over top-level", () => {
      expect(isAllowed(
        makeCtx({ chatId: 456, isGroup: true, text: "no mention" }),
        acl,
      )).toBe(false);

      expect(isAllowed(
        makeCtx({ chatId: 456, isGroup: true, text: "hey @testbot" }),
        acl,
      )).toBe(true);
    });

    it("uses top-level require_mention for other groups", () => {
      // require_mention is false at top level
      expect(isAllowed(
        makeCtx({ chatId: 789, isGroup: true, text: "no mention" }),
        acl,
      )).toBe(true);
    });
  });
});
