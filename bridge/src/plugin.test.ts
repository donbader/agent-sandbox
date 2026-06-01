import { describe, it, expect, vi, beforeEach } from "vitest";
import { PluginRegistry } from "./plugin.js";
import type { BridgePlugin, PluginContext, CommandHandler } from "./plugin.js";

const mockCtx: PluginContext = {
  sendMessage: vi.fn(),
  config: {},
};

function makePlugin(overrides: Partial<BridgePlugin> = {}): BridgePlugin {
  return { name: "test-plugin", ...overrides };
}

function makeCommand(description?: string): CommandHandler {
  return {
    description,
    handler: vi.fn().mockResolvedValue("ok"),
  };
}

// ---------------------------------------------------------------------------
// register
// ---------------------------------------------------------------------------

describe("PluginRegistry.register", () => {
  it("adds plugin to the plugins list", () => {
    const registry = new PluginRegistry();
    const plugin = makePlugin();
    registry.register(plugin);
    expect(registry.getPlugins()).toContain(plugin);
  });

  it("registers commands from the plugin", () => {
    const registry = new PluginRegistry();
    const cmd = makeCommand("do something");
    registry.register(makePlugin({ commands: { foo: cmd } }));
    expect(registry.getCommand("foo")).toBe(cmd);
  });

  it("registers multiple commands from one plugin", () => {
    const registry = new PluginRegistry();
    const a = makeCommand();
    const b = makeCommand();
    registry.register(makePlugin({ commands: { a, b } }));
    expect(registry.getCommand("a")).toBe(a);
    expect(registry.getCommand("b")).toBe(b);
  });

  it("first registration wins on duplicate command name", () => {
    const registry = new PluginRegistry();
    const first = makeCommand("first");
    const second = makeCommand("second");
    registry.register(makePlugin({ name: "plugin-a", commands: { foo: first } }));
    registry.register(makePlugin({ name: "plugin-b", commands: { foo: second } }));
    expect(registry.getCommand("foo")).toBe(first);
  });

  it("registers plugin with no commands without error", () => {
    const registry = new PluginRegistry();
    expect(() => registry.register(makePlugin())).not.toThrow();
  });
});

// ---------------------------------------------------------------------------
// getCommand / getCommandNames
// ---------------------------------------------------------------------------

describe("PluginRegistry.getCommand", () => {
  it("returns undefined for unknown command", () => {
    const registry = new PluginRegistry();
    expect(registry.getCommand("unknown")).toBeUndefined();
  });

  it("returns the registered handler", () => {
    const registry = new PluginRegistry();
    const cmd = makeCommand();
    registry.register(makePlugin({ commands: { ping: cmd } }));
    expect(registry.getCommand("ping")).toBe(cmd);
  });
});

describe("PluginRegistry.getCommandNames", () => {
  it("returns empty array when no commands registered", () => {
    const registry = new PluginRegistry();
    expect(registry.getCommandNames()).toEqual([]);
  });

  it("returns all registered command names", () => {
    const registry = new PluginRegistry();
    registry.register(makePlugin({ commands: { foo: makeCommand(), bar: makeCommand() } }));
    expect(registry.getCommandNames().sort()).toEqual(["bar", "foo"]);
  });

  it("does not include duplicate command names", () => {
    const registry = new PluginRegistry();
    registry.register(makePlugin({ name: "a", commands: { foo: makeCommand() } }));
    registry.register(makePlugin({ name: "b", commands: { foo: makeCommand() } }));
    expect(registry.getCommandNames().filter(n => n === "foo")).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// boot
// ---------------------------------------------------------------------------

describe("PluginRegistry.boot", () => {
  it("calls onBoot on all plugins", async () => {
    const registry = new PluginRegistry();
    const boot1 = vi.fn().mockResolvedValue(undefined);
    const boot2 = vi.fn().mockResolvedValue(undefined);
    registry.register(makePlugin({ name: "p1", onBoot: boot1 }));
    registry.register(makePlugin({ name: "p2", onBoot: boot2 }));
    await registry.boot(mockCtx);
    expect(boot1).toHaveBeenCalledWith(mockCtx);
    expect(boot2).toHaveBeenCalledWith(mockCtx);
  });

  it("skips plugins without onBoot", async () => {
    const registry = new PluginRegistry();
    registry.register(makePlugin());
    await expect(registry.boot(mockCtx)).resolves.toBeUndefined();
  });

  it("continues booting remaining plugins when one throws", async () => {
    const registry = new PluginRegistry();
    const boot2 = vi.fn().mockResolvedValue(undefined);
    registry.register(makePlugin({ name: "bad", onBoot: async () => { throw new Error("boom"); } }));
    registry.register(makePlugin({ name: "good", onBoot: boot2 }));
    await registry.boot(mockCtx);
    expect(boot2).toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// notifyTurnStart
// ---------------------------------------------------------------------------

describe("PluginRegistry.notifyTurnStart", () => {
  it("calls onTurnStart on all plugins", () => {
    const registry = new PluginRegistry();
    const start1 = vi.fn();
    const start2 = vi.fn();
    registry.register(makePlugin({ name: "p1", onTurnStart: start1 }));
    registry.register(makePlugin({ name: "p2", onTurnStart: start2 }));
    registry.notifyTurnStart(mockCtx, "chat-1");
    expect(start1).toHaveBeenCalledWith(mockCtx, "chat-1");
    expect(start2).toHaveBeenCalledWith(mockCtx, "chat-1");
  });

  it("continues notifying remaining plugins when one throws", () => {
    const registry = new PluginRegistry();
    const start2 = vi.fn();
    registry.register(makePlugin({ name: "bad", onTurnStart: () => { throw new Error("boom"); } }));
    registry.register(makePlugin({ name: "good", onTurnStart: start2 }));
    expect(() => registry.notifyTurnStart(mockCtx, "chat-1")).not.toThrow();
    expect(start2).toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// notifyTurnEnd
// ---------------------------------------------------------------------------

describe("PluginRegistry.notifyTurnEnd", () => {
  it("calls onTurnEnd on all plugins", () => {
    const registry = new PluginRegistry();
    const end1 = vi.fn();
    const end2 = vi.fn();
    registry.register(makePlugin({ name: "p1", onTurnEnd: end1 }));
    registry.register(makePlugin({ name: "p2", onTurnEnd: end2 }));
    registry.notifyTurnEnd(mockCtx, "chat-1");
    expect(end1).toHaveBeenCalledWith(mockCtx, "chat-1");
    expect(end2).toHaveBeenCalledWith(mockCtx, "chat-1");
  });

  it("continues notifying remaining plugins when one throws", () => {
    const registry = new PluginRegistry();
    const end2 = vi.fn();
    registry.register(makePlugin({ name: "bad", onTurnEnd: () => { throw new Error("boom"); } }));
    registry.register(makePlugin({ name: "good", onTurnEnd: end2 }));
    expect(() => registry.notifyTurnEnd(mockCtx, "chat-1")).not.toThrow();
    expect(end2).toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// notifyEvent
// ---------------------------------------------------------------------------

describe("PluginRegistry.notifyEvent", () => {
  it("calls onEvent on all plugins", () => {
    const registry = new PluginRegistry();
    const ev1 = vi.fn();
    const ev2 = vi.fn();
    registry.register(makePlugin({ name: "p1", onEvent: ev1 }));
    registry.register(makePlugin({ name: "p2", onEvent: ev2 }));
    const event = { type: "agent_message_chunk", text: "hi" };
    registry.notifyEvent(mockCtx, "chat-1", event);
    expect(ev1).toHaveBeenCalledWith(mockCtx, "chat-1", event);
    expect(ev2).toHaveBeenCalledWith(mockCtx, "chat-1", event);
  });

  it("continues notifying remaining plugins when one throws", () => {
    const registry = new PluginRegistry();
    const ev2 = vi.fn();
    registry.register(makePlugin({ name: "bad", onEvent: () => { throw new Error("boom"); } }));
    registry.register(makePlugin({ name: "good", onEvent: ev2 }));
    expect(() => registry.notifyEvent(mockCtx, "chat-1", { type: "test" })).not.toThrow();
    expect(ev2).toHaveBeenCalled();
  });
});
