/**
 * RFC 7591 OAuth Dynamic Client Registration.
 * Registers a new client with the authorization server when no client_id is pre-configured.
 */
import type { PluginLogger } from "../../logger.js";

const REGISTRATION_TIMEOUT_MS = 15_000;

export interface ClientRegistrationResponse {
  client_id: string;
  client_secret?: string;
  client_id_issued_at?: number;
  client_secret_expires_at?: number;
}

/**
 * Register a new OAuth client via RFC 7591 Dynamic Client Registration.
 * Used when no client_id is pre-configured for a provider.
 */
export async function registerClient(
  registrationEndpoint: string,
  redirectUri: string,
  log: PluginLogger,
  clientName?: string,
): Promise<ClientRegistrationResponse> {
  const body = {
    redirect_uris: [redirectUri],
    grant_types: ["authorization_code"],
    response_types: ["code"],
    token_endpoint_auth_method: "none",
    ...(clientName && { client_name: clientName }),
  };

  log.debug({ registrationEndpoint, clientName }, "starting dynamic client registration");

  const response = await fetch(registrationEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(REGISTRATION_TIMEOUT_MS),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(
      `Dynamic client registration failed (HTTP ${response.status}): ${text}`,
    );
  }

  const data = (await response.json()) as ClientRegistrationResponse;

  if (!data.client_id) {
    throw new Error("Registration response missing client_id");
  }

  log.debug({ clientId: data.client_id }, "dynamic client registration succeeded");
  return data;
}
