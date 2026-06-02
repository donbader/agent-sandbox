import { describe, it, expect, vi, beforeEach } from "vitest";
import { BridgeClient, AcpAgent } from "./acp-client.js";
import type { PromptOptions } from "./acp-client.js";
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
    // Access the internal acpHandler to trigger the callback
    const acpHandler = (agent as any).acpHandler as BridgeClient;
    await acpHandler.sessionUpdate({
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

    const acpHandler = (agent as any).acpHandler as BridgeClient;
    await acpHandler.sessionUpdate({
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

// ---------------------------------------------------------------------------
// BridgeClient — sessionUpdateCallbackForSession (forwards notifications per session)
// ---------------------------------------------------------------------------

describe("BridgeClient.sessionUpdateCallbackForSession", () => {
  let client: BridgeClient;

  beforeEach(() => {
    client = new BridgeClient();
  });

  it("forwards agent_message_chunk to session-specific callback", async () => {
    const cb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb);

    const notification: SessionNotification = {
      sessionId: "s1",
      update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } },
    } as SessionNotification;

    await client.sessionUpdate(notification);
    expect(cb).toHaveBeenCalledWith(notification);
  });

  it("forwards agent_thought_chunk to session-specific callback", async () => {
    const cb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb);

    const notification = {
      sessionId: "s1",
      update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "thinking..." } },
    } as SessionNotification;

    await client.sessionUpdate(notification);
    expect(cb).toHaveBeenCalledWith(notification);
  });

  it("forwards tool_call to session-specific callback", async () => {
    const cb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb);

    const notification = {
      sessionId: "s1",
      update: { sessionUpdate: "tool_call", toolName: "read_file", toolCallId: "tc-1" },
    } as unknown as SessionNotification;

    await client.sessionUpdate(notification);
    expect(cb).toHaveBeenCalledWith(notification);
  });

  it("forwards tool_call_update to session-specific callback", async () => {
    const cb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb);

    const notification = {
      sessionId: "s1",
      update: { sessionUpdate: "tool_call_update", toolCallId: "tc-1", content: { type: "text", text: "result" } },
    } as unknown as SessionNotification;

    await client.sessionUpdate(notification);
    expect(cb).toHaveBeenCalledWith(notification);
  });

  it("session-specific callback is called BEFORE chunkCallback for agent_message_chunk", async () => {
    const order: string[] = [];
    client.setSessionUpdateCallbackForSession("s1", () => order.push("sessionUpdate"));
    client.setChunkCallback(() => order.push("chunk"));

    await client.sessionUpdate({
      sessionId: "s1",
      update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } },
    } as SessionNotification);

    expect(order).toEqual(["sessionUpdate", "chunk"]);
  });

  it("agent_message_chunk still collected by chunkCallback when session callback is set", async () => {
    const chunks: string[] = [];
    const sessionCb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", sessionCb);
    client.setChunkCallback((t) => chunks.push(t));

    await client.sessionUpdate({
      sessionId: "s1",
      update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "Hello" } },
    } as SessionNotification);

    expect(chunks).toEqual(["Hello"]);
    expect(sessionCb).toHaveBeenCalledTimes(1);
  });

  it("clears session callback with null", async () => {
    const cb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb);
    client.setSessionUpdateCallbackForSession("s1", null);

    await client.sessionUpdate({
      sessionId: "s1",
      update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "thinking" } },
    } as SessionNotification);

    expect(cb).not.toHaveBeenCalled();
  });

  it("routes notifications to correct session callback (concurrent sessions)", async () => {
    const cb1 = vi.fn();
    const cb2 = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb1);
    client.setSessionUpdateCallbackForSession("s2", cb2);

    const notification1 = {
      sessionId: "s1",
      update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "for s1" } },
    } as SessionNotification;

    const notification2 = {
      sessionId: "s2",
      update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "for s2" } },
    } as SessionNotification;

    await client.sessionUpdate(notification1);
    await client.sessionUpdate(notification2);

    expect(cb1).toHaveBeenCalledTimes(1);
    expect(cb1).toHaveBeenCalledWith(notification1);
    expect(cb2).toHaveBeenCalledTimes(1);
    expect(cb2).toHaveBeenCalledWith(notification2);
  });

  it("does not forward to unregistered session", async () => {
    const cb = vi.fn();
    client.setSessionUpdateCallbackForSession("s1", cb);

    await client.sessionUpdate({
      sessionId: "s-unknown",
      update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } },
    } as SessionNotification);

    expect(cb).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// AcpAgent.prompt() — PromptOptions with onSessionUpdate
// ---------------------------------------------------------------------------

describe("AcpAgent.prompt() with PromptOptions", () => {
  it("rejects prompt() when not started even with options", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    const options: PromptOptions = { onSessionUpdate: vi.fn() };
    await expect(agent.prompt("session-id", "hello", options)).rejects.toThrow("ACP agent not started");
  });

  it("prompt() works without options (backwards compat)", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    // Just verify signature accepts 2 args — actual prompt needs connection
    await expect(agent.prompt("session-id", "hello")).rejects.toThrow("ACP agent not started");
  });

  it("onSessionUpdate receives all notification types during prompt", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    const acpHandler = (agent as any).acpHandler as BridgeClient;

    // Simulate connection so prompt doesn't throw "not started"
    const fakeConnection = {
      prompt: vi.fn().mockImplementation(async () => {
        // Simulate notifications arriving during prompt
        await acpHandler.sessionUpdate({
          sessionId: "s1",
          update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "thinking" } },
        } as SessionNotification);
        await acpHandler.sessionUpdate({
          sessionId: "s1",
          update: { sessionUpdate: "tool_call", toolName: "bash", toolCallId: "tc-1" },
        } as unknown as SessionNotification);
        await acpHandler.sessionUpdate({
          sessionId: "s1",
          update: { sessionUpdate: "tool_call_update", toolCallId: "tc-1", content: { type: "text", text: "output" } },
        } as unknown as SessionNotification);
        await acpHandler.sessionUpdate({
          sessionId: "s1",
          update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "Done!" } },
        } as SessionNotification);
      }),
    };
    (agent as any).connection = fakeConnection;

    const updates: SessionNotification[] = [];
    const options: PromptOptions = {
      onSessionUpdate: (notification) => updates.push(notification),
    };

    const result = await agent.prompt("s1", "do something", options);

    // All notification types forwarded
    expect(updates).toHaveLength(4);
    expect(updates[0].update.sessionUpdate).toBe("agent_thought_chunk");
    expect(updates[1].update.sessionUpdate).toBe("tool_call");
    expect(updates[2].update.sessionUpdate).toBe("tool_call_update");
    expect(updates[3].update.sessionUpdate).toBe("agent_message_chunk");

    // agent_message_chunk still collected for return value
    expect(result).toBe("Done!");
  });

  it("onSessionUpdate callback is cleared after prompt resolves", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    const acpHandler = (agent as any).acpHandler as BridgeClient;

    const fakeConnection = {
      prompt: vi.fn().mockImplementation(async () => {
        await acpHandler.sessionUpdate({
          sessionId: "s1",
          update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } },
        } as SessionNotification);
      }),
    };
    (agent as any).connection = fakeConnection;

    const onSessionUpdate = vi.fn();
    await agent.prompt("s1", "hello", { onSessionUpdate });

    // After prompt resolves, callback should be cleared
    // Simulate another notification — should NOT be forwarded
    await acpHandler.sessionUpdate({
      sessionId: "s1",
      update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "later" } },
    } as SessionNotification);

    // Only 1 call (from during the prompt), not 2
    expect(onSessionUpdate).toHaveBeenCalledTimes(1);
  });

  it("onSessionUpdate callback is cleared after prompt rejects", async () => {
    const agent = new AcpAgent({ cmd: ["echo", "hi"], cwd: "/tmp" });
    const acpHandler = (agent as any).acpHandler as BridgeClient;

    const fakeConnection = {
      prompt: vi.fn().mockRejectedValue(new Error("agent crashed")),
    };
    (agent as any).connection = fakeConnection;

    const onSessionUpdate = vi.fn();
    await expect(
      agent.prompt("s1", "hello", { onSessionUpdate })
    ).rejects.toThrow("agent crashed");

    // After reject, callback should be cleared
    await acpHandler.sessionUpdate({
      sessionId: "s1",
      update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "later" } },
    } as SessionNotification);

    expect(onSessionUpdate).not.toHaveBeenCalled();
  });
});
