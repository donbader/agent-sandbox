// Disconnect route handler — revokes and removes stored OAuth token for a provider.
// GET /plugins/mcp-oauth/disconnect/{provider}

/// <reference path="../../../gateway/types/gateway.d.ts" />

export default function(ctx: GatewayContext, options: PluginOptions) {
  const providers: Record<string, any> = options.providers || {};

  // Extract provider name from path
  const path = ctx.request.path || "";
  const parts = path.split("/").filter((p: string) => p !== "");
  const providerName = parts[parts.length - 1];

  if (!providerName || providerName === "disconnect") {
    ctx.response.status(400);
    ctx.response.header("Content-Type", "application/json");
    ctx.response.body(JSON.stringify({
      error: "provider name required",
      available: Object.keys(providers),
    }));
    return;
  }

  if (!providers[providerName]) {
    ctx.response.status(404);
    ctx.response.header("Content-Type", "application/json");
    ctx.response.body(JSON.stringify({
      error: "unknown provider: " + providerName,
      available: Object.keys(providers),
    }));
    return;
  }

  // Read existing token to attempt revocation
  let tokenData: any = null;
  try {
    const raw = gw.fs.read(providerName + ".json");
    if (raw && raw.trim()) {
      tokenData = JSON.parse(raw);
    }
  } catch {
    // no token to revoke
  }

  let revoked = false;

  if (tokenData && tokenData.access_token) {
    // Try to find revocation endpoint via OAuth discovery
    const providerCfg = providers[providerName];
    const revocationEndpoint = findRevocationEndpoint(providerCfg);

    if (revocationEndpoint) {
      revoked = revokeToken(revocationEndpoint, tokenData);
    } else {
      gw.log.info("oauth-disconnect: no revocation endpoint found for " + providerName + ", clearing local token only");
    }
  }

  // Clear local token file
  gw.fs.write(providerName + ".json", "");

  // Also clear cached registration so next login does fresh DCR
  try {
    gw.fs.write(providerName + ".reg.json", "");
  } catch {
    // ignore if reg file doesn't exist
  }

  gw.log.info("oauth-disconnect: cleared token for " + providerName + " (revoked=" + revoked + ")");

  ctx.response.status(200);
  ctx.response.header("Content-Type", "application/json");
  ctx.response.body(JSON.stringify({
    disconnected: true,
    revoked,
    provider: providerName,
  }));
}

function findRevocationEndpoint(providerCfg: any): string | null {
  if (!providerCfg || !providerCfg.mcp_url) return null;

  const originMatch = providerCfg.mcp_url.match(/^(https?:\/\/[^/]+)/);
  if (!originMatch) return null;

  try {
    const metaResp = gw.http.fetch(originMatch[1] + "/.well-known/oauth-authorization-server", {
      method: "GET",
      headers: { "Accept": "application/json" },
    });
    if (metaResp.status !== 200) return null;
    const meta = JSON.parse(metaResp.body);
    return meta.revocation_endpoint || null;
  } catch {
    return null;
  }
}

function revokeToken(revocationEndpoint: string, tokenData: any): boolean {
  // Try revoking refresh_token first (more thorough), fall back to access_token
  const tokenToRevoke = tokenData.refresh_token || tokenData.access_token;
  const tokenType = tokenData.refresh_token ? "refresh_token" : "access_token";

  const params: string[] = [
    "token=" + encodeURIComponent(tokenToRevoke),
    "token_type_hint=" + encodeURIComponent(tokenType),
  ];

  if (tokenData.client_id) {
    params.push("client_id=" + encodeURIComponent(tokenData.client_id));
  }

  try {
    const resp = gw.http.fetch(revocationEndpoint, {
      method: "POST",
      body: params.join("&"),
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
    });

    // RFC 7009: 200 means success (even if token was already invalid)
    if (resp.status === 200) {
      gw.log.info("oauth-disconnect: token revoked successfully");
      return true;
    } else {
      gw.log.error("oauth-disconnect: revocation returned " + resp.status + ": " + resp.body);
      return false;
    }
  } catch (e: any) {
    gw.log.error("oauth-disconnect: revocation request failed: " + e.message);
    return false;
  }
}
