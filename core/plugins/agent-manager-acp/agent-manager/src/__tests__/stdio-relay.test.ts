import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { EventEmitter } from "node:events";
import { Readable, Writable } from "node:stream";

// Mock agent process
class MockAgentProcess extends EventEmitter {
  sent: any[] = [];
  isRunning = true;

  send(msg: any): boolean {
    this.sent.push(msg);
    return true;
  }

  restart(): void {
    this.emit("exit", 0, null);
  }
}

// We need to test StdioRelay with mocked stdin/stdout
describe("StdioRelay", () => {
  let agent: MockAgentProcess;
  let stdoutData: string[];
  let originalStdin: NodeJS.ReadableStream;
  let originalStdout: NodeJS.WritableStream;
  let mockStdin: Readable;
  let mockStdout: Writable;

  beforeEach(() => {
    agent = new MockAgentProcess();
    stdoutData = [];

    // Save originals
    originalStdin = process.stdin;
    originalStdout = process.stdout;

    // Create mock streams
    mockStdin = new Readable({ read() {} });
    mockStdout = new Writable({
      write(chunk, _enc, cb) {
        stdoutData.push(chunk.toString());
        cb();
      },
    });

    // Replace process streams
    Object.defineProperty(process, "stdin", { value: mockStdin, writable: true });
    Object.defineProperty(process, "stdout", { value: mockStdout, writable: true });
  });

  afterEach(() => {
    Object.defineProperty(process, "stdin", { value: originalStdin, writable: true });
    Object.defineProperty(process, "stdout", { value: originalStdout, writable: true });
  });

  async function loadRelay() {
    // Dynamic import to pick up mocked process streams
    const { StdioRelay } = await import("../stdio-relay.js");
    return StdioRelay;
  }

  it("forwards agent messages to stdout as ndjson", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    // Simulate agent emitting a notification
    agent.emit("message", {
      jsonrpc: "2.0",
      method: "session/update",
      params: { sessionId: "s1", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hello" } } },
    });

    expect(stdoutData.length).toBe(1);
    const parsed = JSON.parse(stdoutData[0].trim());
    expect(parsed.method).toBe("session/update");
  });

  it("intercepts initialize and returns cached result", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1, agentCapabilities: {} });
    relay.start();

    // Simulate parent sending initialize via stdin
    mockStdin.push(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }) + "\n");

    await new Promise((r) => setTimeout(r, 10));

    expect(stdoutData.length).toBe(1);
    const parsed = JSON.parse(stdoutData[0].trim());
    expect(parsed.id).toBe(1);
    expect(parsed.result).toEqual({ protocolVersion: 1, agentCapabilities: {} });
    // Should NOT forward to agent
    expect(agent.sent.length).toBe(0);
  });

  it("intercepts auth/authenticate and returns empty result", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    mockStdin.push(JSON.stringify({ jsonrpc: "2.0", id: 2, method: "auth/authenticate", params: { id: "key", secret: "s" } }) + "\n");

    await new Promise((r) => setTimeout(r, 10));

    const parsed = JSON.parse(stdoutData[0].trim());
    expect(parsed.id).toBe(2);
    expect(parsed.result).toEqual({});
    expect(agent.sent.length).toBe(0);
  });

  it("injects cwd into session/new params", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/my/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    mockStdin.push(JSON.stringify({ jsonrpc: "2.0", id: 3, method: "session/new", params: {} }) + "\n");

    await new Promise((r) => setTimeout(r, 10));

    // Should forward to agent with injected cwd
    expect(agent.sent.length).toBe(1);
    expect(agent.sent[0].params.cwd).toBe("/my/workspace");
    expect(agent.sent[0].params.mcpServers).toEqual([]);
  });

  it("forwards session/prompt to agent", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    const promptMsg = { jsonrpc: "2.0", id: 4, method: "session/prompt", params: { sessionId: "s1", prompt: [{ type: "text", text: "hello" }] } };
    mockStdin.push(JSON.stringify(promptMsg) + "\n");

    await new Promise((r) => setTimeout(r, 10));

    expect(agent.sent.length).toBe(1);
    expect(agent.sent[0].method).toBe("session/prompt");
  });

  it("intercepts /restart command", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    const restartMsg = { jsonrpc: "2.0", id: 5, method: "session/prompt", params: { sessionId: "s1", prompt: [{ type: "text", text: "/restart" }] } };
    mockStdin.push(JSON.stringify(restartMsg) + "\n");

    await new Promise((r) => setTimeout(r, 10));

    // restart() emits exit first (error notification), then the intercepted response
    const responses = stdoutData.map((d) => JSON.parse(d.trim()));
    const restartResponse = responses.find((r: any) => r.id === 5);
    expect(restartResponse).toBeDefined();
    expect(restartResponse.result.stopReason).toBe("end_turn");
    expect(agent.sent.length).toBe(0);
  });

  it("broadcasts error on agent exit", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    agent.emit("exit", 1, "SIGKILL");

    expect(stdoutData.length).toBe(1);
    const parsed = JSON.parse(stdoutData[0].trim());
    expect(parsed.method).toBe("session/update");
    expect(parsed.params.update.sessionUpdate).toBe("error");
    expect(parsed.params.update.error.message).toContain("exited");
  });

  it("broadcasts error on auth-related stderr", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    agent.emit("stderr", "Error: 401 Unauthorized from API");

    expect(stdoutData.length).toBe(1);
    const parsed = JSON.parse(stdoutData[0].trim());
    expect(parsed.params.update.error.message).toContain("authentication error");
  });

  it("broadcasts error on rate limit stderr", async () => {
    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    agent.emit("stderr", "429 Too Many Requests");

    expect(stdoutData.length).toBe(1);
    const parsed = JSON.parse(stdoutData[0].trim());
    expect(parsed.params.update.error.message).toContain("rate limited");
  });

  it("does not exit on stdin close when exitOnClose=false", async () => {
    const exitSpy = vi.spyOn(process, "exit").mockImplementation(() => undefined as never);

    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: false });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    mockStdin.push(null); // close stdin

    await new Promise((r) => setTimeout(r, 10));

    expect(exitSpy).not.toHaveBeenCalled();
    exitSpy.mockRestore();
  });

  it("exits on stdin close when exitOnClose=true", async () => {
    const exitSpy = vi.spyOn(process, "exit").mockImplementation(() => undefined as never);

    const { StdioRelay } = await import("../stdio-relay.js");
    const relay = new StdioRelay(agent as any, "/workspace", { exitOnClose: true });
    relay.setInitResult({ protocolVersion: 1 });
    relay.start();

    mockStdin.push(null); // close stdin

    await new Promise((r) => setTimeout(r, 10));

    expect(exitSpy).toHaveBeenCalledWith(0);
    exitSpy.mockRestore();
  });
});
