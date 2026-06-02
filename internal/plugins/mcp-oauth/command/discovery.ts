/**
 * RFC 9728 OAuth Protected Resource Metadata discovery.
 * Fetches authorization server metadata from the MCP server's origin.
 */
import type { AuthorizationServerMetadata } from "./types.js";

/**
 * Discover the OAuth authorization server metadata for an MCP server URL.
 * Fetches {origin}/.well-known/oauth-authorization-server and parses the response.
 */
export async function discoverAuthServer(mcpUrl: string): Promise<AuthorizationServerMetadata> {
  const url = new URL(mcpUrl);
  const wellKnownUrl = `${url.origin}/.well-known/oauth-authorization-server`;

  const response = await fetch(wellKnownUrl);
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

  return metadata;
}
