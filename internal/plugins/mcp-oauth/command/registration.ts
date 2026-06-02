/**
 * RFC 7591 OAuth Dynamic Client Registration.
 * Registers a new client with the authorization server when no client_id is pre-configured.
 */

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
  clientName?: string,
): Promise<ClientRegistrationResponse> {
  const body = {
    redirect_uris: [redirectUri],
    grant_types: ["authorization_code"],
    response_types: ["code"],
    token_endpoint_auth_method: "none",
    ...(clientName && { client_name: clientName }),
  };

  const response = await fetch(registrationEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
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

  return data;
}
