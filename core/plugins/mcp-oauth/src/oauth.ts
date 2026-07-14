// OAuth token injection middleware.
// Matches requests by host against configured provider MCP URLs.
// Reads/refreshes tokens from disk, injects Authorization header.

/// <reference path="../../../gateway/types/gateway.d.ts" />

interface StoredToken {
  access_token: string;
  refresh_token?: string;
  expires_at: number;
  token_endpoint: string;
  client_id: string;
}

interface ProviderConfig {
  mcp_url?: string;
  authorize_endpoint?: string;
  token_endpoint?: string;
  client_id?: string;
  client_secret?: string;
  scopes?: string;
}

function domainFromURL(urlStr: string): string | null {
  const match = urlStr.match(/^https?:\/\/([^/:]+)/);
  return match ? match[1] : null;
}

function readToken(provider: string, tokenDir: string): StoredToken | null {
  try {
    const data = gw.fs.read(`${provider}.json`);
    const token = JSON.parse(data);
    gw.log.debug("oauth: read token for " + provider + " (expires_at=" + token.expires_at + ")");
    return token;
  } catch {
    gw.log.debug("oauth: no stored token for " + provider);
    return null;
  }
}

function writeToken(provider: string, token: StoredToken): void {
  gw.fs.write(`${provider}.json`, JSON.stringify(token, null, 2));
}

function refreshToken(stored: StoredToken, clientSecret?: string, tokenEndpoint?: string): StoredToken | null {
  if (!stored.refresh_token) {
    gw.log.info("oauth: no refresh_token available, cannot refresh");
    return null;
  }

  gw.log.info("oauth: refreshing expired token (client_id=" + stored.client_id + ")");

  const params = [
    "grant_type=refresh_token",
    "refresh_token=" + encodeURIComponent(stored.refresh_token),
    "client_id=" + encodeURIComponent(stored.client_id),
  ];
  if (clientSecret) {
    params.push("client_secret=" + encodeURIComponent(clientSecret));
  }

  // Use pinned endpoint from options/registration; fall back to stored only if unavailable
  // ponytail: stored fallback exists for tokens written before this fix; remove after migration
  const endpoint = tokenEndpoint || stored.token_endpoint;

  let resp: any;
  try {
    resp = gw.http.fetch(endpoint, {
      method: "POST",
      body: params.join("&"),
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
    });
  } catch (e: any) {
    gw.log.error("oauth: refresh request failed: " + e.message);
    return null;
  }

  if (resp.status !== 200) {
    gw.log.error("oauth: refresh returned status " + resp.status + " body=" + (resp.body || "").substring(0, 200));
    return null;
  }

  const tr = JSON.parse(resp.body);
  const expiresIn = tr.expires_in || 3600;
  gw.log.info("oauth: refresh succeeded (expires_in=" + expiresIn + "s)");
  return {
    access_token: tr.access_token,
    refresh_token: tr.refresh_token || stored.refresh_token,
    expires_at: Math.floor(Date.now() / 1000) + expiresIn,
    token_endpoint: endpoint,
    client_id: stored.client_id,
  };
}

function loadRegistration(provider: string): { authorize_endpoint: string; token_endpoint: string; client_id: string; client_secret: string } | null {
  try {
    const data = gw.fs.read(`${provider}.reg.json`);
    const reg = JSON.parse(data);
    if (reg.client_id) return reg;
  } catch {
    // no cached registration
  }
  return null;
}

function buildAuthorizeURL(
  providersJSON: string,
  providerName: string,
  provider: { authorize_endpoint: string; client_id: string; scopes?: string; mcp_url?: string },
  callbackURL: string
): string {
  // HMAC-based state for middleware-initiated flows
  const key = gw.crypto.sha256(providersJSON);
  const sig = gw.crypto.hmac(key, providerName).substring(0, 16);
  const state = sig + ":" + providerName;

  const params: string[] = [
    "client_id=" + encodeURIComponent(provider.client_id),
    "response_type=code",
    "state=" + encodeURIComponent(state),
    "redirect_uri=" + encodeURIComponent(callbackURL),
  ];
  if (provider.scopes) {
    params.push("scope=" + encodeURIComponent(provider.scopes));
  }
  if (provider.mcp_url) {
    params.push("resource=" + encodeURIComponent(provider.mcp_url));
  }
  return provider.authorize_endpoint + "?" + params.join("&");
}

export default function(ctx: GatewayContext, options: PluginOptions) {
  const providers: Record<string, ProviderConfig> = options.providers || {};
  const callbackURL = options.callback_url || "";
  const providersJSON = JSON.stringify(providers);

  const requestHost = ctx.request.host;

  // Find which provider matches this request's host
  let matchedName: string | null = null;
  let matchedCfg: ProviderConfig | null = null;

  for (const [name, cfg] of Object.entries(providers)) {
    if (!cfg.mcp_url) continue;
    const domain = domainFromURL(cfg.mcp_url);
    if (domain && domain === requestHost) {
      matchedName = name;
      matchedCfg = cfg;
      break;
    }
  }

  if (!matchedName || !matchedCfg) return;

  gw.log.debug("oauth: matched provider=" + matchedName + " for host=" + requestHost);

  // Try to read stored token
  const stored = readToken(matchedName, "");
  if (!stored) {
    gw.log.info("oauth: no token for " + matchedName + ", passing through without auth");
    // No token — let the request pass through unauthenticated.
    // The upstream server will return its own response (public endpoints work,
    // protected endpoints return a proper 401 with OAuth metadata).
    return;
  }

  const now = Math.floor(Date.now() / 1000);

  // Check if token is expired or about to expire (5 min buffer)
  if (now + 300 >= stored.expires_at) {
    gw.log.info("oauth: token for " + matchedName + " expired or expiring soon (expires_at=" + stored.expires_at + ", now=" + now + ")");
    let clientSecret = matchedCfg.client_secret || "";
    // Pin token endpoint from options/registration — never trust the stored token file
    let pinnedTokenEndpoint = matchedCfg.token_endpoint || "";
    if (!clientSecret || !pinnedTokenEndpoint) {
      const reg = loadRegistration(matchedName);
      if (reg) {
        if (!clientSecret && reg.client_secret) clientSecret = reg.client_secret;
        if (!pinnedTokenEndpoint && reg.token_endpoint) pinnedTokenEndpoint = reg.token_endpoint;
      }
    }
    const refreshed = refreshToken(stored, clientSecret, pinnedTokenEndpoint || undefined);
    if (refreshed) {
      writeToken(matchedName, refreshed);
      gw.secrets.register(refreshed.access_token);
      ctx.request.setHeader("Authorization", "Bearer " + refreshed.access_token);
      gw.log.info("oauth: injected refreshed token for " + matchedName);
      return;
    }
    // Refresh failed — pass through without auth, let upstream return 401
    gw.log.error("oauth: token expired and refresh failed for " + matchedName + ", passing through without auth");
    return;
  }

  // Token is valid — inject it
  gw.secrets.register(stored.access_token);
  ctx.request.setHeader("Authorization", "Bearer " + stored.access_token);
  gw.log.info("oauth: injected token for " + matchedName + " (expires_in=" + (stored.expires_at - now) + "s)");
}
