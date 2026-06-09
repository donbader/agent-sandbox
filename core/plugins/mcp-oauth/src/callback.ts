// OAuth callback handler — exchanges authorization code for token.
// GET /plugins/mcp-oauth/callback?code=X&state=Y

declare const gw: any;

import { consumePendingFlow, verifyHmacState } from "./pkce";

interface ProviderConfig {
  mcp_url?: string;
  authorize_endpoint?: string;
  token_endpoint?: string;
  client_id?: string;
  client_secret?: string;
  scopes?: string;
}

interface TokenResponse {
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
}

function loadRegistration(provider: string): { token_endpoint: string; client_id: string; client_secret: string } | null {
  try {
    const data = gw.fs.read(`${provider}.reg.json`);
    const reg = JSON.parse(data);
    if (reg.client_id && reg.token_endpoint) return reg;
  } catch {
    // no cached registration
  }
  return null;
}

function exchangeCode(
  tokenEndpoint: string,
  code: string,
  redirectURI: string,
  clientId: string,
  clientSecret: string,
  codeVerifier?: string
): TokenResponse {
  const params: string[] = [
    "grant_type=authorization_code",
    "code=" + encodeURIComponent(code),
    "client_id=" + encodeURIComponent(clientId),
    "redirect_uri=" + encodeURIComponent(redirectURI),
  ];
  if (codeVerifier) {
    params.push("code_verifier=" + encodeURIComponent(codeVerifier));
  }
  if (clientSecret) {
    params.push("client_secret=" + encodeURIComponent(clientSecret));
  }

  const resp = gw.http.fetch(tokenEndpoint, {
    method: "POST",
    body: params.join("&"),
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
  });

  if (resp.status !== 200) {
    throw new Error("token endpoint returned " + resp.status + ": " + resp.body);
  }

  return JSON.parse(resp.body);
}

function writeToken(provider: string, token: TokenResponse, tokenEndpoint: string, clientId: string): void {
  const expiresIn = token.expires_in || 3600;
  const stored: Record<string, any> = {
    access_token: token.access_token,
    expires_at: Math.floor(Date.now() / 1000) + expiresIn,
    token_endpoint: tokenEndpoint,
    client_id: clientId,
  };
  if (token.refresh_token) {
    stored.refresh_token = token.refresh_token;
  }
  gw.fs.write(`${provider}.json`, JSON.stringify(stored, null, 2));
}

function escapeHTML(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function successHTML(providerName: string): string {
  return `<!DOCTYPE html><html><body>
<h1>Authorization successful</h1>
<p>Provider <strong>${escapeHTML(providerName)}</strong> connected. You can close this tab.</p>
</body></html>`;
}

export default function(ctx: any, options: any) {
  const providers: Record<string, ProviderConfig> = options.providers || {};
  const callbackURL = options.callback_url || "";
  const providersJSON = JSON.stringify(providers);

  const query = ctx.request.query || {};
  const code = query["code"] || null;
  const state = query["state"] || null;

  if (!code) {
    ctx.response.status(400);
    ctx.response.body("missing code parameter");
    return;
  }
  if (!state) {
    ctx.response.status(400);
    ctx.response.body("missing state parameter");
    return;
  }

  // Try PKCE flow first (from login endpoint)
  const flow = consumePendingFlow(state);
  if (flow) {
    const providerCfg = providers[flow.provider] || {};
    let tokenEndpoint = providerCfg.token_endpoint || "";
    let clientId = providerCfg.client_id || "";
    let clientSecret = providerCfg.client_secret || "";

    // Fall back to cached registration
    if (!tokenEndpoint || !clientId) {
      const reg = loadRegistration(flow.provider);
      if (reg) {
        tokenEndpoint = reg.token_endpoint;
        clientId = reg.client_id;
        clientSecret = reg.client_secret;
      }
    }

    if (!tokenEndpoint) {
      ctx.response.status(500);
      ctx.response.body("provider token endpoint not configured");
      return;
    }

    let token: TokenResponse;
    try {
      token = exchangeCode(tokenEndpoint, code, flow.redirect_uri, clientId, clientSecret, flow.code_verifier);
    } catch (e: any) {
      gw.log.error("oauth-callback: PKCE token exchange failed for " + flow.provider + ": " + e.message);
      ctx.response.status(500);
      ctx.response.body("token exchange failed: " + e.message);
      return;
    }

    writeToken(flow.provider, token, tokenEndpoint, clientId);
    gw.secrets.register(token.access_token);
    ctx.response.status(200);
    ctx.response.header("Content-Type", "text/html; charset=utf-8");
    ctx.response.body(successHTML(flow.provider));
    return;
  }

  // Fall back to HMAC-based state (middleware-initiated flow)
  const providerName = verifyHmacState(providersJSON, state);
  if (!providerName) {
    ctx.response.status(403);
    ctx.response.body("invalid state signature");
    return;
  }

  const providerCfg = providers[providerName];
  if (!providerCfg) {
    ctx.response.status(400);
    ctx.response.body("unknown provider");
    return;
  }

  let tokenEndpoint = providerCfg.token_endpoint || "";
  let clientId = providerCfg.client_id || "";
  let clientSecret = providerCfg.client_secret || "";

  if (!tokenEndpoint || !clientId) {
    const reg = loadRegistration(providerName);
    if (reg) {
      tokenEndpoint = reg.token_endpoint;
      clientId = reg.client_id;
      clientSecret = reg.client_secret;
    }
  }

  if (!tokenEndpoint) {
    ctx.response.status(500);
    ctx.response.body("provider not configured");
    return;
  }

  // For HMAC flows, use the configured callback URL as redirect_uri
  const redirectURI = callbackURL || "";

  let token: TokenResponse;
  try {
    token = exchangeCode(tokenEndpoint, code, redirectURI, clientId, clientSecret);
  } catch (e: any) {
    gw.log.error("oauth-callback: token exchange failed for " + providerName + ": " + e.message);
    ctx.response.status(500);
    ctx.response.body("token exchange failed");
    return;
  }

  writeToken(providerName, token, tokenEndpoint, clientId);
  gw.secrets.register(token.access_token);
  ctx.response.status(200);
  ctx.response.header("Content-Type", "text/html; charset=utf-8");
  ctx.response.body(successHTML(providerName));
}
