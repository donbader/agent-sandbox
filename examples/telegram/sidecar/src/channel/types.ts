import type { AcpAgent } from "../acp-client.js";

export interface Channel {
  start(): Promise<void>;
  stop(): void;
}

/** Factory function type for channel plugins. */
export type ChannelFactory = (
  config: Record<string, unknown>,
  agent: AcpAgent
) => Channel;
