import { spawn, ChildProcess } from "node:child_process";
import { createLogger } from "./logger.js";

const log = createLogger("agent-process");

/**
 * AgentProcess manages the downstream agent subprocess via ACP over stdio.
 * It uses the ACP ClientSideConnection to communicate with the agent.
 */
export class AgentProcess {
  private proc: ChildProcess | null = null;
  private cmd: string[];
  private cwd: string;

  constructor(cmd: string[], cwd: string) {
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
    });

    // Wait briefly for process to be ready
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => resolve(), 500);
      this.proc!.on("error", (err) => {
        clearTimeout(timeout);
        reject(new Error(`Failed to spawn agent: ${err.message}`));
      });
    });

    log.info("agent process started");
  }

  async stop(): Promise<void> {
    if (this.proc) {
      this.proc.kill("SIGTERM");
      this.proc = null;
    }
  }

  async restart(): Promise<void> {
    log.info("restarting agent process");
    await this.stop();
    await this.start();
  }

  get stdin() {
    return this.proc?.stdin ?? null;
  }

  get stdout() {
    return this.proc?.stdout ?? null;
  }

  get isRunning(): boolean {
    return this.proc !== null && this.proc.exitCode === null;
  }
}
