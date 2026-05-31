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
 * Implements the ACP Client interface for headless bridge use.
 * Auto-approves all permission requests and collects agent message chunks.
 * Exported for testing.
 */
export class BridgeClient implements acp.Client {
  private chunkCallback: ((text: string) => void) | null = null;

  setChunkCallback(cb: ((text: string) => void) | null): void {
    this.chunkCallback = cb;
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
    const { update } = params;
    if (
      update.sessionUpdate === "agent_message_chunk" &&
      update.content.type === "text"
    ) {
      this.chunkCallback?.(update.content.text);
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
  private sessionId: string | null = null;
  private restarting = false;
  private bridgeClient: BridgeClient;
  private pendingReject: ((err: Error) => void) | null = null;

  constructor(config: AcpAgentConfig) {
    this.config = config;
    this.bridgeClient = new BridgeClient();
  }

  async start(): Promise<void> {
    const [command, ...args] = this.config.cmd;
    if (!command) {
      throw new Error("acp-agent: empty command");
    }

    log.info({ cmd: this.config.cmd.join(" ") }, "spawning ACP agent");

    this.proc = spawn(command, args, { stdio: ["pipe", "pipe", "inherit"] });

    this.proc.on("exit", (code) => {
      log.info({ code }, "ACP agent exited");
      // Reject any in-flight prompt
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
          this.start().catch((err: unknown) => {
            log.error({ error: err }, "failed to restart ACP agent");
          });
        }, 2000);
      }
    });

    const input = Writable.toWeb(this.proc.stdin!);
    const output = Readable.toWeb(
      this.proc.stdout!
    ) as ReadableStream<Uint8Array>;
    const stream = acp.ndJsonStream(input, output);

    const client = this.bridgeClient;
    this.connection = new acp.ClientSideConnection((_agent) => client, stream);

    await this.connection.initialize({
      protocolVersion: acp.PROTOCOL_VERSION,
      clientCapabilities: {},
    });

    const { sessionId } = await this.connection.newSession({
      cwd: this.config.cwd,
      mcpServers: [],
    });
    this.sessionId = sessionId;

    log.info({ sessionId }, "ACP session created");
  }

  /**
   * Sends a prompt to the agent and returns the full response text.
   * Collects all agent_message_chunk updates until the prompt completes.
   */
  async prompt(text: string): Promise<string> {
    if (!this.connection || !this.sessionId) {
      throw new Error("ACP agent not started");
    }

    const chunks: string[] = [];

    return new Promise<string>((resolve, reject) => {
      this.bridgeClient.setChunkCallback((chunk) => chunks.push(chunk));
      this.pendingReject = reject;

      this.connection!.prompt({
        sessionId: this.sessionId!,
        prompt: [{ type: "text", text }],
      })
        .then(() => {
          this.pendingReject = null;
          this.bridgeClient.setChunkCallback(null);
          resolve(chunks.join(""));
        })
        .catch((err: unknown) => {
          this.pendingReject = null;
          this.bridgeClient.setChunkCallback(null);
          reject(err instanceof Error ? err : new Error(String(err)));
        });
    });
  }

  stop(): void {
    this.restarting = true; // prevent auto-restart
    if (this.proc) {
      this.proc.kill("SIGTERM");
      this.proc = null;
    }
    this.connection = null;
    this.sessionId = null;
  }
}
