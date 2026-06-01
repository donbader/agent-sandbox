import { describe, it, expect, vi, beforeEach } from "vitest";
import { BridgeClient, AcpAgent } from "./acp-client.js";
import type { RequestPermissionRequest, SessionNotification } from "@agentclientprotocol/sdk";

// Helper to build minimal RequestPermissionRequest test fixtures.
// The full type requires sessionId + toolCall which are irrelevant to options logic.
function makePermissionRequest(
  options: RequestPermissionRequest["options"]
): RequestPermissionRequest {
  return { options } as RequestPermissionRequest;
}

// ---------------------------------------------------------------------------
// BridgeClient — requestPermission
// ---------------------------------------------------------------------------

describe("BridgeClient.requestPermission", () => {
  let client: BridgeClient;

  beforeEach(() => {
    client = new BridgeClient();
  });

  it("selects allow_once option when present", async () => {
    const params = makePermissionRequest([
      { optionId: "opt-reject", kind: "reject_once", name: "Reject" },
      { optionId: "opt-allow", kind: "allow_once", name: "Allow" },
    ]);
    const result = await client.requestPermission(params);
    expect(result.outcome).toEqual({ outcome: "selected", optionId: "opt-allow" });
  });

  it("selects allow_always option when present", async () => {
    const params = makePermissionRequest([
      { optionId: "opt-reject", kind: "reject_always", name: "Reject" },
      { optionId: "opt-allow", kind: "allow_always", name: "Allow Always" },
    ]);
    const result = await client.requestPermission(params);
    expect(result.outcome).toEqual({ outcome: "selected", optionId: "opt-allow" });
  });

  it("picks first allow option in array order", async () => {
    const params = makePermissionRequest([
      { optionId: "opt-always", kind: "allow_always", name: "Allow Always" },
      { optionId: "opt-once", kind: "allow_once", name: "Allow Once" },
    ]);
    const result = await client.requestPermission(params);
    // allow_always is first in the array, so it wins
    expect(result.outcome).toEqual({ outcome: "selected", optionId: "opt-always" });
  });

  it("falls back to first option when no allow option exists", async () => {
    const params = makePermissionRequest([
      { optionId: "opt-reject-once", kind: "reject_once", name: "Reject Once" },
      { optionId: "opt-reject-always", kind: "reject_always", name: "Reject Always" },
    ]);
    const result = await client.requestPermission(params);
    expect(result.outcome).toEqual({ outcome: "selected", optionId: "opt-reject-once" });
  });

  it("throws when options array is empty", async () => {
    const params = makePermissionRequest([]);
    await expect(client.requestPermission(params)).rejects.toThrow(
      "requestPermission: no options provided"
    );
  });
});

// ---------------------------------------------------------------------------
// BridgeClient — sessionUpdate
// ---------------------------------------------------------------------------

describe("BridgeClient.sessionUpdate", () => {
  let client: BridgeClient;

  beforeEach(() => {
    client = new BridgeClient();
  });

  // Helper to build a SessionNotification for a given update type
  const makeNotification = (
    sessionUpdate: string,
    text: string
  ): SessionNotification =>
    ({
      sessionId: "sess-1",
      update: { sessionUpdate, content: { type: "text", text } },
    }) as SessionNotification;

  it("calls chunkCallback for agent_message_chunk with text content", async () => {
    const cb = vi.fn();
    client.setChunkCallback(cb);
    await client.sessionUpdate(makeNotification("agent_message_chunk", "Hello"));
    expect(cb).toHaveBeenCalledWith("Hello");
  });

  it("does not call chunkCallback for agent_thought_chunk", async () => {
    const cb = vi.fn();
    client.setChunkCallback(cb);
    await client.sessionUpdate(makeNotification("agent_thought_chunk", "thinking..."));
    expect(cb).not.toHaveBeenCalled();
  });

  it("does not throw when no chunkCallback is set", async () => {
    await expect(
      client.sessionUpdate(makeNotification("agent_message_chunk", "Hello"))
    ).resolves.toBeUndefined();
  });

  it("clears chunkCallback after setChunkCallback(null)", async () => {
    const cb = vi.fn();
    client.setChunkCallback(cb);
    client.setChunkCallback(null);
    await client.sessionUpdate(makeNotification("agent_message_chunk", "Hello"));
    expect(cb).not.toHaveBeenCalled();
  });

  it("accumulates multiple chunks in order", async () => {
    const collected: string[] = [];
    client.setChunkCallback((t) => collected.push(t));

    await client.sessionUpdate(makeNotification("agent_message_chunk", "Hello"));
    await client.sessionUpdate(makeNotification("agent_message_chunk", ", "));
    await client.sessionUpdate(makeNotification("agent_message_chunk", "world"));

    expect(collected).toEqual(["Hello", ", ", "world"]);
  });
});

// ---------------------------------------------------------------------------
// AcpAgent — basic guards
// ---------------------------------------------------------------------------

describe("AcpAgent", () => {
  it("rejects prompt() when not started", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    await expect(agent.prompt("session-id", "hello")).rejects.toThrow("ACP agent not started");
  });

  it("stop() does not throw when called before start()", () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    expect(() => agent.stop()).not.toThrow();
  });

  it("getAgentCommands() returns empty array initially", () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    expect(agent.getAgentCommands()).toEqual([]);
  });

  it("onCommandsUpdate() is called when agent sends available_commands_update", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    const listener = vi.fn();
    agent.onCommandsUpdate(listener);

    // Simulate the BridgeClient receiving an available_commands_update
    // Access the internal bridgeClient to trigger the callback
    const bridgeClient = (agent as any).bridgeClient as BridgeClient;
    await bridgeClient.sessionUpdate({
      sessionId: "test-session",
      update: {
        sessionUpdate: "available_commands_update",
        availableCommands: [
          { name: "model", description: "Switch model" },
          { name: "compact", description: "Compact context", input: { hint: "level" } },
        ],
      },
    } as any);

    expect(listener).toHaveBeenCalledWith([
      { name: "model", description: "Switch model", inputHint: undefined },
      { name: "compact", description: "Compact context", inputHint: "level" },
    ]);
    expect(agent.getAgentCommands()).toEqual([
      { name: "model", description: "Switch model", inputHint: undefined },
      { name: "compact", description: "Compact context", inputHint: "level" },
    ]);
  });

  it("supports multiple onCommandsUpdate listeners", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    const listener1 = vi.fn();
    const listener2 = vi.fn();
    agent.onCommandsUpdate(listener1);
    agent.onCommandsUpdate(listener2);

    const bridgeClient = (agent as any).bridgeClient as BridgeClient;
    await bridgeClient.sessionUpdate({
      sessionId: "test-session",
      update: {
        sessionUpdate: "available_commands_update",
        availableCommands: [{ name: "status", description: "Show status" }],
      },
    } as any);

    expect(listener1).toHaveBeenCalledTimes(1);
    expect(listener2).toHaveBeenCalledTimes(1);
  });
});

// ---------------------------------------------------------------------------
// BridgeClient — available_commands_update
// ---------------------------------------------------------------------------

describe("BridgeClient.sessionUpdate — available_commands_update", () => {
  let client: BridgeClient;

  beforeEach(() => {
    client = new BridgeClient();
  });

  it("calls commandsCallback with parsed commands", async () => {
    const cb = vi.fn();
    client.setCommandsCallback(cb);

    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "available_commands_update",
        availableCommands: [
          { name: "model", description: "Switch model", input: { hint: "model name" } },
          { name: "compact", description: "Compact context" },
        ],
      },
    } as any);

    expect(cb).toHaveBeenCalledWith([
      { name: "model", description: "Switch model", inputHint: "model name" },
      { name: "compact", description: "Compact context", inputHint: undefined },
    ]);
  });

  it("does not throw when commandsCallback is null", async () => {
    await expect(
      client.sessionUpdate({
        sessionId: "s1",
        update: {
          sessionUpdate: "available_commands_update",
          availableCommands: [{ name: "test", description: "" }],
        },
      } as any)
    ).resolves.toBeUndefined();
  });

  it("handles empty availableCommands array", async () => {
    const cb = vi.fn();
    client.setCommandsCallback(cb);

    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "available_commands_update",
        availableCommands: [],
      },
    } as any);

    expect(cb).toHaveBeenCalledWith([]);
  });

  it("handles missing description gracefully", async () => {
    const cb = vi.fn();
    client.setCommandsCallback(cb);

    await client.sessionUpdate({
      sessionId: "s1",
      update: {
        sessionUpdate: "available_commands_update",
        availableCommands: [{ name: "test" }],
      },
    } as any);

    expect(cb).toHaveBeenCalledWith([{ name: "test", description: "", inputHint: undefined }]);
  });
});
