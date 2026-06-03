import { spawn, type ChildProcess } from "node:child_process";
import { Writable, Readable } from "node:stream";
import * as acp from "@agentclientprotocol/sdk";
import { createLogger } from "./logger.js";

const log = createLogger("acp-client");

export interface AcpAgentConfig {
  cmd: string[];
  cwd: string;
}

/**
 * Implements the ACP Client interface for headless channel-manager use.
 * Auto-approves all permission requests and collects agent message chunks.
 * Exported for testing.
 */
export interface AgentCommand {
  name: string;
  description: string;
  inputHint?: string;
}

export interface PromptOptions {
  onSessionUpdate?: (notification: acp.SessionNotification) => void;
}

export class BridgeClient implements acp.Client {
  private chunkCallback: ((text: string) => void) | null = null;
  private commandsCallback: ((commands: AgentCommand[]) => void) | null = null;
  private sessionUpdateCallbacks = new Map<string, (notification: acp.SessionNotification) => void>();

  setChunkCallback(cb: ((text: string) => void) | null): void {
    this.chunkCallback = cb;
  }

  setCommandsCallback(cb: ((commands: AgentCommand[]) => void) | null): void {
    this.commandsCallback = cb;
  }

  setSessionUpdateCallbackForSession(sessionId: string, cb: ((notification: acp.SessionNotification) => void) | null): void {
    if (cb) {
      this.sessionUpdateCallbacks.set(sessionId, cb);
    } else {
      this.sessionUpdateCallbacks.delete(sessionId);
    }
  }

  async requestPermission(
    params: acp.RequestPermissionRequest
  ): Promise<acp.RequestPermissionResponse> {
    // Auto-approve: prefer allow_once/allow_always, fall back to first option
    const allowOption = params.options.find(
      (o) => o.kind === "allow_once" || o.kind === "allow_always"
    );
    const chosen = allowOption ?? params.options[0];
    if (!chosen) {
      throw new Error("requestPermission: no options provided");
    }
    log.debug({ optionId: chosen.optionId, kind: chosen.kind }, "auto-approving permission");
    return { outcome: { outcome: "selected", optionId: chosen.optionId } };
  }

  async sessionUpdate(params: acp.SessionNotification): Promise<void> {
    // Forward to session-specific callback
    const { sessionId, update } = params;
    const cb = this.sessionUpdateCallbacks.get(sessionId);
    if (cb) {
      try {
        cb(params);
      } catch {
        // Don't let caller errors break chunk collection
      }
    }

    if (
      update.sessionUpdate === "agent_message_chunk" &&
      update.content.type === "text"
    ) {
      this.chunkCallback?.(update.content.text);
    } else if (update.sessionUpdate === "available_commands_update") {
      const agentCommands: AgentCommand[] = update.availableCommands.map((c) => ({
        name: c.name,
        description: c.description ?? "",
        inputHint: c.input?.hint,
      }));
      log.info({ count: agentCommands.length }, "received agent commands");
      this.commandsCallback?.(agentCommands);
    }
  }
}

/**
 * Wraps an ACP ClientSideConnection, spawning the agent as a subprocess.
 * Handles initialize + session creation, prompt collection, and auto-restart.
 */
export class AcpAgent {
  private config: AcpAgentConfig;
  private proc: ChildProcess | null = null;
  private connection: acp.ClientSideConnection | null = null;
  private restarting = false;
  private acpHandler: BridgeClient;
  private pendingReject: ((err: Error) => void) | null = null;
  private agentCommands: AgentCommand[] = [];
  private commandsListeners: Array<(commands: AgentCommand[]) => void> = [];

  constructor(config: AcpAgentConfig) {
    this.config = config;
    this.acpHandler = new BridgeClient();
    this.acpHandler.setCommandsCallback((cmds) => {
      this.agentCommands = cmds;
      for (const listener of this.commandsListeners) {
        listener(cmds);
      }
    });
  }

  /** Get the current list of agent-declared commands. */
  getAgentCommands(): AgentCommand[] {
    return this.agentCommands;
  }

  /** Register a listener for when agent commands change. */
  onCommandsUpdate(cb: (commands: AgentCommand[]) => void): void {
    this.commandsListeners.push(cb);
  }

  async start(): Promise<void> {
    const [command, ...args] = this.config.cmd;
    if (!command) {
      throw new Error("acp-agent: empty command");
    }

    await this.spawnAndConnect(command, args);
  }

  private async spawnAndConnect(command: string, args: string[]): Promise<void> {
    const maxRetries = 10;
    const baseDelay = 2000;

    for (let attempt = 1; attempt <= maxRetries; attempt++) {
      try {
        log.info({ cmd: this.config.cmd.join(" "), attempt }, "spawning ACP agent");

        this.proc = spawn(command, args, { stdio: ["pipe", "pipe", "inherit"] });

        this.proc.on("exit", (code) => {
          log.info({ code }, "ACP agent exited");
          if (this.pendingReject) {
            this.pendingReject(
              new Error(`agent process exited with code ${String(code)}`)
            );
            this.pendingReject = null;
          }
          if (!this.restarting) {
            this.restarting = true;
            setTimeout(() => {
              this.restarting = false;
              this.spawnAndConnect(command, args).catch((err: unknown) => {
                log.error({ error: err }, "failed to restart ACP agent");
              });
            }, baseDelay);
          }
        });

        const input = Writable.toWeb(this.proc.stdin!);
        const output = Readable.toWeb(
          this.proc.stdout!
        ) as ReadableStream<Uint8Array>;
        const stream = acp.ndJsonStream(input, output);

        const client = this.acpHandler;
        this.connection = new acp.ClientSideConnection((_agent) => client, stream);

        await this.connection.initialize({
          protocolVersion: acp.PROTOCOL_VERSION,
          clientCapabilities: {},
        });

        log.info("ACP connection established");
        return;
      } catch (err: unknown) {
        // Kill the failed process before retrying
        if (this.proc) {
          this.proc.kill("SIGTERM");
          this.proc = null;
        }
        this.connection = null;

        if (attempt === maxRetries) {
          log.error({ error: err, attempt }, "ACP agent failed to start after max retries");
          throw err;
        }

        const delay = baseDelay * attempt;
        log.warn({ error: err, attempt, retryIn: delay }, "ACP agent failed to start, retrying");
        await new Promise((resolve) => setTimeout(resolve, delay));
      }
    }
  }

  /**
   * Sends a prompt to the agent and returns the full response text.
   * Collects all agent_message_chunk updates until the prompt completes.
   */
  async prompt(sessionId: string, text: string, options?: PromptOptions): Promise<string> {
    if (!this.connection) {
      throw new Error("ACP agent not started");
    }

    // Check prompt interceptor (wrapper commands, command plugins)
    if (this.promptInterceptor) {
      const intercepted = await this.promptInterceptor(text, sessionId);
      if (intercepted !== null) {
        // Emit as agent_message_chunk so channel stream controllers render it.
        if (options?.onSessionUpdate) {
          options.onSessionUpdate({
            sessionId,
            update: {
              sessionUpdate: "agent_message_chunk",
              content: { type: "text", text: intercepted },
            },
          } as acp.SessionNotification);
        }
        return intercepted;
      }
    }

    const chunks: string[] = [];

    this.acpHandler.setChunkCallback((chunk) => chunks.push(chunk));
    if (options?.onSessionUpdate) {
      this.acpHandler.setSessionUpdateCallbackForSession(sessionId, options.onSessionUpdate);
    }

    try {
      await new Promise<void>((resolve, reject) => {
        this.pendingReject = reject;

        this.connection!.prompt({
          sessionId,
          prompt: [{ type: "text", text }],
        })
          .then(() => resolve())
          .catch((err: unknown) => {
            reject(err instanceof Error ? err : new Error(String(err)));
          });
      });

      return chunks.join("");
    } finally {
      this.pendingReject = null;
      this.acpHandler.setChunkCallback(null);
      this.acpHandler.setSessionUpdateCallbackForSession(sessionId, null);
    }
  }

  /** Whether the agent has an active connection. */
  isReady(): boolean {
    return this.connection !== null;
  }

  /**
   * Set a prompt interceptor that can short-circuit prompts before they reach the agent.
   * Return a string to respond locally, or null to forward to agent.
   */
  private promptInterceptor: ((text: string, sessionId: string) => Promise<string | null>) | null = null;

  setPromptInterceptor(fn: (text: string, sessionId: string) => Promise<string | null>): void {
    this.promptInterceptor = fn;
  }

  getConnection(): acp.ClientSideConnection | null {
    return this.connection;
  }

  /** Reset: kill current process, restart fresh. Returns when new session is ready. */
  async reset(): Promise<void> {
    const [command, ...args] = this.config.cmd;
    if (!command) throw new Error("acp-agent: empty command");

    this.restarting = true;
    if (this.proc) {
      this.proc.kill("SIGTERM");
      this.proc = null;
    }
    this.connection = null;
    this.restarting = false;

    await this.spawnAndConnect(command, args);
  }

  /** Abort current operation by killing the process. Auto-restart will handle reconnection. */
  abort(): void {
    if (this.proc) {
      this.proc.kill("SIGTERM");
    }
  }

  stop(): void {
    this.restarting = true; // prevent auto-restart
    if (this.proc) {
      this.proc.kill("SIGTERM");
      this.proc = null;
    }
    this.connection = null;
  }
}
