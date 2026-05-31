import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { createLogger } from "./logger.js";

const log = createLogger("agent-process");

export interface AgentMessage {
  type: string;
  chat_id?: string;
  text?: string;
  [key: string]: unknown;
}

type MessageHandler = (msg: AgentMessage) => void;

/**
 * Manages the agent as a child process, communicating via line-based JSON on stdin/stdout.
 */
export class AgentProcess {
  private cmd: string[];
  private proc: ChildProcess | null = null;
  private handler: MessageHandler | null = null;
  private restarting = false;

  constructor(cmd: string[]) {
    this.cmd = cmd;
  }

  start(): void {
    const [command, ...args] = this.cmd;
    if (!command) {
      throw new Error("agent-process: empty command");
    }

    log.info({ cmd: this.cmd.join(" ") }, "spawning agent");
    this.proc = spawn(command, args, {
      stdio: ["pipe", "pipe", "inherit"],
    });

    // Read stdout line by line
    if (this.proc.stdout) {
      const rl = createInterface({ input: this.proc.stdout });
      rl.on("line", (line) => {
        try {
          const msg = JSON.parse(line) as AgentMessage;
          if (this.handler) {
            this.handler(msg);
          }
        } catch {
          // Non-JSON output from agent — ignore
        }
      });
    }

    this.proc.on("exit", (code) => {
      log.info({ code }, "agent exited");
      if (!this.restarting) {
        // Auto-restart after delay
        this.restarting = true;
        setTimeout(() => {
          this.restarting = false;
          this.start();
        }, 2000);
      }
    });
  }

  send(msg: AgentMessage): void {
    if (this.proc?.stdin?.writable) {
      this.proc.stdin.write(JSON.stringify(msg) + "\n");
    }
  }

  onMessage(handler: MessageHandler): void {
    this.handler = handler;
  }

  stop(): void {
    this.restarting = true; // prevent auto-restart
    if (this.proc) {
      this.proc.kill("SIGTERM");
      this.proc = null;
    }
  }
}
