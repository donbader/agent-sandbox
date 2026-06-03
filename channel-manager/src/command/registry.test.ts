import { describe, it, expect, beforeEach, vi } from "vitest";
import {
  registerPlugin,
  handleCommand,
  handleMessage,
  initPlugins,
  resetRegistry,
} from "./registry.js";
import type { CommandPlugin } from "./types.js";

beforeEach(() => {
  resetRegistry();
});

describe("registerPlugin + handleCommand", () => {
  it("routes command to correct plugin handler", async () => {
    const handler = vi.fn();
    const plugin: CommandPlugin = {
      name: "test",
      commands: { hello: handler },
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    await handleCommand("/hello world", "chat1", reply);

    expect(handler).toHaveBeenCalledWith({
      args: "world",
      chatId: "chat1",
      reply: expect.any(Function),
    });
  });

  it("returns captured reply from handler", async () => {
    const plugin: CommandPlugin = {
      name: "test",
      commands: {
        greet: async (ctx) => {
          ctx.reply("hi there");
        },
      },
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    const result = await handleCommand("/greet", "chat1", reply);

    expect(result).toBe("hi there");
    expect(reply).toHaveBeenCalledWith("hi there");
  });

  it("returns null for unknown command", async () => {
    const plugin: CommandPlugin = {
      name: "test",
      commands: { known: vi.fn() },
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    const result = await handleCommand("/unknown", "chat1", reply);

    expect(result).toBeNull();
    expect(reply).not.toHaveBeenCalled();
  });

  it("returns null for non-command text", async () => {
    const reply = vi.fn();
    const result = await handleCommand("hello world", "chat1", reply);
    expect(result).toBeNull();
  });

  it("parses command with no args", async () => {
    const handler = vi.fn();
    const plugin: CommandPlugin = {
      name: "test",
      commands: { status: handler },
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    await handleCommand("/status", "chat1", reply);

    expect(handler).toHaveBeenCalledWith({
      args: "",
      chatId: "chat1",
      reply: expect.any(Function),
    });
  });
});

describe("handleMessage", () => {
  it("routes to plugin onMessage and returns true if handled", async () => {
    const plugin: CommandPlugin = {
      name: "interceptor",
      commands: {},
      onMessage: vi.fn().mockResolvedValue(true),
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    const handled = await handleMessage("some text", "chat1", reply);

    expect(handled).toBe(true);
    expect(plugin.onMessage).toHaveBeenCalledWith("some text", "chat1", reply);
  });

  it("returns false when no plugin handles the message", async () => {
    const plugin: CommandPlugin = {
      name: "passive",
      commands: {},
      onMessage: vi.fn().mockResolvedValue(false),
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    const handled = await handleMessage("text", "chat1", reply);
    expect(handled).toBe(false);
  });

  it("returns false when no plugins have onMessage", async () => {
    const plugin: CommandPlugin = {
      name: "basic",
      commands: {},
    };
    registerPlugin(plugin);

    const reply = vi.fn();
    const handled = await handleMessage("text", "chat1", reply);
    expect(handled).toBe(false);
  });
});

describe("initPlugins", () => {
  it("calls init on all plugins with config and logger", () => {
    const init1 = vi.fn();
    const init2 = vi.fn();
    registerPlugin({ name: "a", commands: {}, init: init1 });
    registerPlugin({ name: "b", commands: {}, init: init2 });

    const config = { key: "value" };
    const mockLogger = { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn(), child: vi.fn() };
    const createLogger = vi.fn(() => mockLogger);
    initPlugins(config, createLogger);

    expect(createLogger).toHaveBeenCalledWith("a");
    expect(createLogger).toHaveBeenCalledWith("b");
    expect(init1).toHaveBeenCalledWith(config, mockLogger);
    expect(init2).toHaveBeenCalledWith(config, mockLogger);
  });

  it("skips plugins without init", () => {
    registerPlugin({ name: "no-init", commands: {} });
    const createLogger = vi.fn(() => ({ debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn(), child: vi.fn() }));
    // Should not throw
    expect(() => initPlugins({}, createLogger)).not.toThrow();
  });
});
