/**
 * Safe prompt helper — wraps agent.prompt() with error handling.
 * Returns the response string on success, or an error message on failure.
 */
import type { AcpAgent, PromptOptions } from "./acp-client.js";
import { createLogger } from "./logger.js";

const log = createLogger("safe-prompt");

/**
 * Prompt the agent and return the response.
 * On failure, logs the error and returns a user-facing error message.
 */
export async function safePrompt(
  agent: AcpAgent,
  sessionId: string,
  text: string,
  options?: PromptOptions
): Promise<{ ok: true; response: string } | { ok: false; error: string }> {
  try {
    const response = await agent.prompt(sessionId, text, options);
    return { ok: true, response };
  } catch (err: unknown) {
    log.error({ error: err, sessionId }, "agent prompt failed");
    return { ok: false, error: "⚠️ Agent unavailable. Try again shortly." };
  }
}
