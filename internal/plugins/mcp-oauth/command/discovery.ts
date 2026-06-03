/**
 * RFC 9728 OAuth Protected Resource Metadata discovery.
 * Fetches authorization server metadata from the MCP server's origin.
 */
import type { AuthorizationServerMetadata } from "./types.js";
import type { PluginLogger } from "../../logger.js";

const DISCOVERY_TIMEOUT_MS = 15_000;

/**
 * Discover the OAuth authorization server metadata for an MCP server URL.
 * Fetches {origin}/.well-known/oauth-authorization-server and parses the response.
 */
export async function discoverAuthServer(mcpUrl: string, log: PluginLogger): Promise<AuthorizationServerMetadata> {
  const url = new URL(mcpUrl);
  const wellKnownUrl = `${url.origin}/.well-known/oauth-authorization-server`;

  log.debug({ wellKnownUrl }, "fetching OAuth authorization server metadata");

  const response = await fetch(wellKnownUrl, {
    signal: AbortSignal.timeout(DISCOVERY_TIMEOUT_MS),
  });
  if (!response.ok) {
    throw new Error(
      `OAuth discovery failed for ${wellKnownUrl}: HTTP ${response.status} ${response.statusText}`,
    );
  }

  const metadata = (await response.json()) as AuthorizationServerMetadata;

  if (!metadata.authorization_endpoint || !metadata.token_endpoint) {
    throw new Error(
      `Invalid OAuth metadata from ${wellKnownUrl}: missing authorization_endpoint or token_endpoint`,
    );
  }

  log.debug(
    { authorization_endpoint: metadata.authorization_endpoint, registration_endpoint: metadata.registration_endpoint ?? "(none)" },
    "OAuth discovery complete",
  );
  return metadata;
}
