import { spawn, ChildProcess } from "node:child_process";
import { createInterface, Interface } from "node:readline";
import { EventEmitter } from "node:events";
import { createLogger } from "./logger.js";

const log = createLogger("agent-process");

export interface JsonRpcMessage {
  jsonrpc: "2.0";
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

/**
 * AgentProcess manages the downstream agent subprocess via ACP over stdio.
 *
 * Reads newline-delimited JSON-RPC messages from agent stdout and emits them.
 * Writes JSON-RPC messages to agent stdin.
 */
export class AgentProcess extends EventEmitter {
  private proc: ChildProcess | null = null;
  private reader: Interface | null = null;
  private cmd: string[];
  private cwd: string;

  constructor(cmd: string[], cwd: string) {
    super();
    this.cmd = cmd;
    this.cwd = cwd;
  }

  async start(): Promise<void> {
    const [bin, ...args] = this.cmd;
    log.info({ bin, args, cwd: this.cwd }, "spawning agent process");

    const proc = spawn(bin, args, {
      cwd: this.cwd,
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env },
    });

    // Catch EPIPE/write errors on stdin to prevent unhandled exceptions
    // when the agent dies between the writable check and the actual write.
    proc.stdin?.on("error", (err: NodeJS.ErrnoException) => {
      if (err.code === "EPIPE" || err.code === "ERR_STREAM_DESTROYED") {
        log.debug({ err: err.code }, "agent stdin write error (process already exited)");
      } else {
        log.error({ err }, "agent stdin error");
      }
    });

    proc.stderr?.on("data", (chunk: Buffer) => {
      const text = chunk.toString().trim();
      if (text) {
        log.debug({ agent_stderr: text }, "agent stderr");
        this.emit("stderr", text);
      }
    });

    proc.on("exit", (code, signal) => {
      log.warn({ code, signal }, "agent process exited");
      // Only clear references if this is still the current process.
      // Prevents a stale exit event from nulling a newly-spawned process.
      if (this.proc === proc) {
        this.proc = null;
        this.reader = null;
      }
      this.emit("exit", code, signal);
    });

    proc.on("error", (err) => {
      log.error({ err }, "agent process error");
      this.emit("error", err);
    });

    // Read newline-delimited JSON-RPC from agent stdout
    const reader = createInterface({ input: proc.stdout! });
    reader.on("line", (line) => {
      if (!line.trim()) return;
      try {
        const msg: JsonRpcMessage = JSON.parse(line);
        this.emit("message", msg);
      } catch (err) {
        log.warn({ line: line.slice(0, 100) }, "non-JSON line from agent stdout");
      }
    });

    this.proc = proc;
    this.reader = reader;

    // Wait briefly for process to be ready (or fail fast on spawn error)
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => resolve(), 500);
      proc.on("error", (err) => {
        clearTimeout(timeout);
        reject(new Error(`Failed to spawn agent: ${err.message}`));
      });
    });

    log.info("agent process started");
  }

  /** Send a JSON-RPC message to the agent via stdin. */
  send(msg: JsonRpcMessage): boolean {
    if (!this.proc?.stdin?.writable) {
      return false;
    }
    try {
      this.proc.stdin.write(JSON.stringify(msg) + "\n");
      return true;
    } catch {
      return false;
    }
  }

  /** Send a JSON-RPC request and wait for the matching response. */
  sendAndWait(msg: JsonRpcMessage, timeoutMs = 10000): Promise<JsonRpcMessage> {
    return new Promise((resolve, reject) => {
      if (!this.send(msg)) {
        reject(new Error("Agent not running"));
        return;
      }
      const timer = setTimeout(() => {
        this.removeListener("message", handler);
        reject(new Error(`Timeout waiting for response to ${msg.method} (id=${msg.id})`));
      }, timeoutMs);
      const handler = (response: JsonRpcMessage) => {
        if (response.id === msg.id) {
          clearTimeout(timer);
          this.removeListener("message", handler);
          resolve(response);
        }
      };
      this.on("message", handler);
    });
  }

  async stop(): Promise<void> {
    const proc = this.proc;
    if (!proc) return;

    this.reader?.close();
    this.reader = null;
    this.proc = null;

    // Send SIGTERM and wait for actual exit before returning.
    // This prevents race conditions where a new process is spawned
    // while the old one still holds resources (lock files, ports, etc).
    proc.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      proc.on("exit", () => resolve());
      // Failsafe: don't wait forever if process ignores SIGTERM
      setTimeout(() => {
        proc.kill("SIGKILL");
        resolve();
      }, 5000);
    });
  }

  async restart(): Promise<void> {
    log.info("restarting agent process");
    await this.stop();
    await this.start();
  }

  get isRunning(): boolean {
    return this.proc !== null && this.proc.exitCode === null;
  }
}
