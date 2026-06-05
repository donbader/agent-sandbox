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

    this.proc = spawn(bin, args, {
      cwd: this.cwd,
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env },
    });

    this.proc.stderr?.on("data", (chunk: Buffer) => {
      log.debug({ agent_stderr: chunk.toString().trim() }, "agent stderr");
    });

    this.proc.on("exit", (code, signal) => {
      log.warn({ code, signal }, "agent process exited");
      this.proc = null;
      this.reader = null;
      this.emit("exit", code, signal);
    });

    this.proc.on("error", (err) => {
      log.error({ err }, "agent process error");
      this.emit("error", err);
    });

    // Read newline-delimited JSON-RPC from agent stdout
    this.reader = createInterface({ input: this.proc.stdout! });
    this.reader.on("line", (line) => {
      if (!line.trim()) return;
      try {
        const msg: JsonRpcMessage = JSON.parse(line);
        this.emit("message", msg);
      } catch (err) {
        log.warn({ line: line.slice(0, 100) }, "non-JSON line from agent stdout");
      }
    });

    // Wait briefly for process to be ready (or fail fast on spawn error)
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => resolve(), 500);
      this.proc!.on("error", (err) => {
        clearTimeout(timeout);
        reject(new Error(`Failed to spawn agent: ${err.message}`));
      });
    });

    log.info("agent process started");
  }

  /** Send a JSON-RPC message to the agent via stdin. */
  send(msg: JsonRpcMessage): boolean {
    if (!this.proc?.stdin) {
      return false;
    }
    this.proc.stdin.write(JSON.stringify(msg) + "\n");
    return true;
  }

  async stop(): Promise<void> {
    if (this.proc) {
      this.reader?.close();
      this.proc.kill("SIGTERM");
      this.proc = null;
      this.reader = null;
    }
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
