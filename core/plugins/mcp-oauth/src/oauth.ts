// OAuth token injection middleware.
// Matches requests by host against configured provider MCP URLs.
// Reads/refreshes tokens from disk, injects Authorization header.

declare const gw: any;

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
    return JSON.parse(data);
  } catch {
    return null;
  }
}

function writeToken(provider: string, token: StoredToken): void {
  gw.fs.write(`${provider}.json`, JSON.stringify(token, null, 2));
}

function refreshToken(stored: StoredToken, clientSecret?: string): StoredToken | null {
  if (!stored.refresh_token) return null;

  const params = [
    "grant_type=refresh_token",
    "refresh_token=" + encodeURIComponent(stored.refresh_token),
    "client_id=" + encodeURIComponent(stored.client_id),
  ];
  if (clientSecret) {
    params.push("client_secret=" + encodeURIComponent(clientSecret));
  }

  let resp: any;
  try {
    resp = gw.http.fetch(stored.token_endpoint, {
      method: "POST",
      body: params.join("&"),
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
    });
  } catch (e: any) {
    gw.log.error("oauth: refresh request failed: " + e.message);
    return null;
  }

  if (resp.status !== 200) {
    gw.log.error("oauth: refresh returned status " + resp.status);
    return null;
  }

  const tr = JSON.parse(resp.body);
  const expiresIn = tr.expires_in || 3600;
  return {
    access_token: tr.access_token,
    refresh_token: tr.refresh_token || stored.refresh_token,
    expires_at: Math.floor(Date.now() / 1000) + expiresIn,
    token_endpoint: stored.token_endpoint,
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

export default function(ctx: any, options: any) {
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

  // Try to read stored token
  const stored = readToken(matchedName, "");
  if (!stored) {
    // No token — check if we have registration info to build authorize URL
    let authorizeEndpoint = matchedCfg.authorize_endpoint || "";
    let clientId = matchedCfg.client_id || "";
    const scopes = matchedCfg.scopes || "";

    if (!authorizeEndpoint || !clientId) {
      const reg = loadRegistration(matchedName);
      if (reg) {
        authorizeEndpoint = reg.authorize_endpoint;
        clientId = reg.client_id;
      }
    }

    if (authorizeEndpoint && clientId) {
      const authorizeURL = buildAuthorizeURL(providersJSON, matchedName, {
        authorize_endpoint: authorizeEndpoint,
        client_id: clientId,
        scopes: scopes,
        mcp_url: matchedCfg.mcp_url,
      }, callbackURL);
      ctx.abort(401, JSON.stringify({
        error: "oauth_required",
        provider: matchedName,
        authorize_url: authorizeURL,
        hint: "For PKCE login, use: curl http://<gateway>/plugins/mcp-oauth/login/" + matchedName,
      }));
    } else {
      ctx.abort(401, JSON.stringify({
        error: "oauth_required",
        provider: matchedName,
        hint: "No token found. Use: curl http://<gateway>/plugins/mcp-oauth/login/" + matchedName,
      }));
    }
    return;
  }

  const now = Math.floor(Date.now() / 1000);

  // Check if token is expired or about to expire (5 min buffer)
  if (now + 300 >= stored.expires_at) {
    const clientSecret = matchedCfg.client_secret || "";
    const refreshed = refreshToken(stored, clientSecret);
    if (refreshed) {
      writeToken(matchedName, refreshed);
      gw.secrets.register(refreshed.access_token);
      ctx.request.setHeader("Authorization", "Bearer " + refreshed.access_token);
      return;
    }
    // Refresh failed — return 401
    gw.log.error("oauth: token expired and refresh failed for " + matchedName);
    ctx.abort(401, JSON.stringify({
      error: "oauth_token_expired",
      provider: matchedName,
      hint: "Token refresh failed. Re-authenticate: curl http://<gateway>/plugins/mcp-oauth/login/" + matchedName,
    }));
    return;
  }

  // Token is valid — inject it
  gw.secrets.register(stored.access_token);
  ctx.request.setHeader("Authorization", "Bearer " + stored.access_token);
}
