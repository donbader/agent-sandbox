export interface OAuthProviderConfig {
  mcp_url: string;
  client_id?: string;
  client_secret?: string;
  token_file?: string; // defaults to /data/oauth-tokens/<name>.json
}

export interface OAuthConfig {
  providers: Record<string, OAuthProviderConfig>;
  token_dir?: string; // defaults to /data/oauth-tokens
}

export interface StoredToken {
  access_token: string;
  refresh_token?: string;
  expires_at: number;
  token_endpoint: string;
  client_id: string;
  client_secret?: string;
}

export interface AuthorizationServerMetadata {
  issuer: string;
  authorization_endpoint: string;
  token_endpoint: string;
  registration_endpoint?: string;
  scopes_supported?: string[];
}

export interface PendingFlow {
  provider: string;
  chatId: string;
  codeVerifier: string;
  state: string;
  tokenEndpoint: string;
  clientId: string;
  clientSecret?: string;
  redirectUri: string;
  startedAt: number;
}
