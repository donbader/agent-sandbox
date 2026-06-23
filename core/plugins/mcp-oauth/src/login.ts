// Login route handler — initiates OAuth PKCE flow.
// GET /plugins/mcp-oauth/login/{provider}

/// <reference path="../../../gateway/types/gateway.d.ts" />

import {
  generateCodeVerifier,
  codeChallengeS256,
  generateState,
  storePendingFlow,
  PendingFlow,
} from "./pkce";

interface ProviderConfig {
  mcp_url?: string;
  skip_resource?: boolean;
  authorize_endpoint?: string;
  token_endpoint?: string;
  client_id?: string;
  client_secret?: string;
  scopes?: string;
}

interface Registration {
  authorize_endpoint: string;
  token_endpoint: string;
  client_id: string;
  client_secret: string;
}

function discoverAndRegister(mcpURL: string, callbackURL: string, providerName: string): Registration | null {
  // Discovery: fetch /.well-known/oauth-authorization-server from the MCP URL's origin
  const originMatch = mcpURL.match(/^(https?:\/\/[^/]+)/);
  if (!originMatch) return null;
  const origin = originMatch[1];

  let metaResp: any;
  try {
    metaResp = gw.http.fetch(origin + "/.well-known/oauth-authorization-server", {
      method: "GET",
      headers: { "Accept": "application/json" },
    });
  } catch (e: any) {
    gw.log.error("oauth-login: discovery fetch failed: " + e.message);
    return null;
  }

  if (metaResp.status !== 200) {
    gw.log.error("oauth-login: discovery returned " + metaResp.status);
    return null;
  }

  const meta = JSON.parse(metaResp.body);
  const regEndpoint = meta.registration_endpoint;
  const authEndpoint = meta.authorization_endpoint;
  const tokenEndpoint = meta.token_endpoint;

  if (!regEndpoint || !authEndpoint || !tokenEndpoint) {
    gw.log.error("oauth-login: metadata missing required endpoints");
    return null;
  }

  // Dynamic Client Registration
  const regBody = JSON.stringify({
    client_name: "agent-sandbox:" + providerName,
    redirect_uris: [callbackURL],
    grant_types: ["authorization_code", "refresh_token"],
    response_types: ["code"],
    token_endpoint_auth_method: "client_secret_post",
  });

  let regResp: any;
  try {
    regResp = gw.http.fetch(regEndpoint, {
      method: "POST",
      body: regBody,
      headers: { "Content-Type": "application/json" },
    });
  } catch (e: any) {
    gw.log.error("oauth-login: DCR fetch failed: " + e.message);
    return null;
  }

  if (regResp.status !== 200 && regResp.status !== 201) {
    gw.log.error("oauth-login: DCR returned " + regResp.status + ": " + regResp.body);
    return null;
  }

  const reg = JSON.parse(regResp.body);
  if (!reg.client_id) {
    gw.log.error("oauth-login: DCR response missing client_id");
    return null;
  }

  const result: Registration = {
    authorize_endpoint: authEndpoint,
    token_endpoint: tokenEndpoint,
    client_id: reg.client_id,
    client_secret: reg.client_secret || "",
  };

  // Cache registration for reuse
  gw.fs.write(`${providerName}.reg.json`, JSON.stringify(result, null, 2));

  return result;
}

function loadCachedRegistration(providerName: string): Registration | null {
  try {
    const data = gw.fs.read(`${providerName}.reg.json`);
    const reg = JSON.parse(data);
    if (reg.client_id) return reg;
  } catch {
    // no cached registration
  }
  return null;
}

function deriveCallbackURL(ctx: GatewayContext, options: PluginOptions): string {
  if (options.callback_url) return options.callback_url;

  // Allow explicit callback_url via query parameter (used by internal callers like openacp-mcp-oauth)
  const query = ctx.request.query || {};
  if (query["callback_url"]) return query["callback_url"];

  // Derive from request headers
  const proto = ctx.request.headers["x-forwarded-proto"] || "http";
  let host = ctx.request.headers["x-forwarded-host"] || ctx.request.host || "127.0.0.1";
  // Normalize localhost to 127.0.0.1 — some OAuth providers reject "localhost"
  host = host.replace("localhost", "127.0.0.1");
  return proto + "://" + host + "/plugins/mcp-oauth/callback";
}

export default function(ctx: GatewayContext, options: PluginOptions) {
  const providers: Record<string, ProviderConfig> = options.providers || {};

  // Extract provider name from path: /plugins/mcp-oauth/login/{provider}
  const path = ctx.request.path || "";
  const parts = path.split("/").filter((p: string) => p !== "");
  // Path is like: plugins/mcp-oauth/login/{provider}
  // After the route path prefix is stripped, we may get just "/{provider}" or "/{provider}"
  // The route is mounted at /login, so path relative to route could be /{provider}
  const providerName = parts[parts.length - 1];

  // If path ends with "login" (no provider specified), list available providers
  if (!providerName || providerName === "login") {
    const available = Object.keys(providers);
    ctx.response.status(400);
    ctx.response.header("Content-Type", "application/json");
    ctx.response.body(JSON.stringify({
      error: "provider name required",
      available,
      usage: "GET /plugins/mcp-oauth/login/<provider_name>",
    }));
    return;
  }

  const providerCfg = providers[providerName];
  if (!providerCfg) {
    const available = Object.keys(providers);
    ctx.response.status(404);
    ctx.response.header("Content-Type", "application/json");
    ctx.response.body(JSON.stringify({
      error: "unknown provider: " + providerName,
      available,
    }));
    return;
  }

  const callbackURL = deriveCallbackURL(ctx, options);

  // Ensure we have client credentials
  let authorizeEndpoint = providerCfg.authorize_endpoint || "";
  let tokenEndpoint = providerCfg.token_endpoint || "";
  let clientId = providerCfg.client_id || "";
  let clientSecret = providerCfg.client_secret || "";

  // If we have client_id but missing endpoints, discover them from .well-known metadata
  if (clientId && (!authorizeEndpoint || !tokenEndpoint) && providerCfg.mcp_url) {
    const originMatch = providerCfg.mcp_url.match(/^(https?:\/\/[^/]+)/);
    if (originMatch) {
      const origin = originMatch[1];
      try {
        const metaResp = gw.http.fetch(origin + "/.well-known/oauth-authorization-server", {
          method: "GET",
          headers: { "Accept": "application/json" },
        });
        if (metaResp.status === 200) {
          const meta = JSON.parse(metaResp.body);
          if (!authorizeEndpoint && meta.authorization_endpoint) {
            authorizeEndpoint = meta.authorization_endpoint;
          }
          if (!tokenEndpoint && meta.token_endpoint) {
            tokenEndpoint = meta.token_endpoint;
          }
          gw.log.info("oauth-login: discovered endpoints for " + providerName + " (authorize=" + authorizeEndpoint + ", token=" + tokenEndpoint + ")");
        }
      } catch (e: any) {
        gw.log.error("oauth-login: metadata discovery failed for " + providerName + ": " + e.message);
      }
    }
  }

  if (!clientId) {
    // Try cached registration
    const cached = loadCachedRegistration(providerName);
    if (cached) {
      authorizeEndpoint = cached.authorize_endpoint;
      tokenEndpoint = cached.token_endpoint;
      clientId = cached.client_id;
      clientSecret = cached.client_secret;
    } else {
      // Perform discovery + DCR
      if (!providerCfg.mcp_url) {
        ctx.response.status(500);
        ctx.response.header("Content-Type", "application/json");
        ctx.response.body(JSON.stringify({
          error: "no mcp_url configured for provider " + providerName,
        }));
        return;
      }
      const reg = discoverAndRegister(providerCfg.mcp_url, callbackURL, providerName);
      if (!reg) {
        ctx.response.status(500);
        ctx.response.header("Content-Type", "application/json");
        ctx.response.body(JSON.stringify({
          error: "client registration failed for " + providerName,
        }));
        return;
      }
      authorizeEndpoint = reg.authorize_endpoint;
      tokenEndpoint = reg.token_endpoint;
      clientId = reg.client_id;
      clientSecret = reg.client_secret;
    }
  }

  // Validate authorize endpoint uses HTTPS
  if (!authorizeEndpoint.startsWith("https://")) {
    ctx.response.status(500);
    ctx.response.header("Content-Type", "application/json");
    ctx.response.body(JSON.stringify({
      error: "authorize endpoint must use https, got: " + authorizeEndpoint,
    }));
    return;
  }

  // Generate PKCE
  const codeVerifier = generateCodeVerifier();
  const codeChallenge = codeChallengeS256(codeVerifier);
  const state = generateState();

  // Store pending flow to disk
  const flow: PendingFlow = {
    provider: providerName,
    code_verifier: codeVerifier,
    redirect_uri: callbackURL,
    expires_at: Date.now() + 10 * 60 * 1000,
  };
  storePendingFlow(state, flow);

  // Build authorize URL
  const params: string[] = [
    "client_id=" + encodeURIComponent(clientId),
    "response_type=code",
    "state=" + encodeURIComponent(state),
    "redirect_uri=" + encodeURIComponent(callbackURL),
    "code_challenge=" + encodeURIComponent(codeChallenge),
    "code_challenge_method=S256",
  ];
  if (providerCfg.mcp_url && !providerCfg.skip_resource) {
    params.push("resource=" + encodeURIComponent(providerCfg.mcp_url));
  }
  if (providerCfg.scopes) {
    params.push("scope=" + encodeURIComponent(providerCfg.scopes));
  } else if (providerCfg.mcp_url) {
    // Discover scopes from .well-known/oauth-protected-resource (RFC 9728)
    const originMatch = providerCfg.mcp_url.match(/^(https?:\/\/[^/]+)/);
    if (originMatch) {
      try {
        const prResp = gw.http.fetch(originMatch[1] + "/.well-known/oauth-protected-resource", {
          method: "GET",
          headers: { "Accept": "application/json" },
        });
        if (prResp.status === 200) {
          const prMeta = JSON.parse(prResp.body);
          if (prMeta.scopes_supported && prMeta.scopes_supported.length > 0) {
            const discoveredScopes = prMeta.scopes_supported.join(" ");
            params.push("scope=" + encodeURIComponent(discoveredScopes));
            gw.log.info("oauth-login: discovered scopes for " + providerName + " from protected-resource metadata (" + prMeta.scopes_supported.length + " scopes)");
          }
        }
      } catch (e: any) {
        gw.log.debug("oauth-login: could not fetch protected-resource metadata for " + providerName + ": " + e.message);
      }
    }
  }

  const authorizeURL = authorizeEndpoint + "?" + params.join("&");

  gw.log.info("oauth-login: initiated PKCE flow for " + providerName + " (client_id=" + clientId + ", callback=" + callbackURL + ")");

  ctx.response.status(200);
  ctx.response.header("Content-Type", "application/json");
  ctx.response.body(JSON.stringify({
    authorize_url: authorizeURL,
    provider: providerName,
    instructions: "Open the authorize_url in your browser to complete login.",
  }));
}
