// Status route handler — checks if a provider has a stored OAuth token.
// GET /plugins/mcp-oauth/status/{provider}

/// <reference path="../../../gateway/types/gateway.d.ts" />

export default function(ctx: GatewayContext, options: PluginOptions) {
  const providers: Record<string, any> = options.providers || {};

  // Extract provider name from path (same pattern as login.ts)
  const path = ctx.request.path || "";
  const parts = path.split("/").filter((p: string) => p !== "");
  const providerName = parts[parts.length - 1];

  // If no provider specified, return status for all
  if (!providerName || providerName === "status") {
    const statuses: Record<string, { connected: boolean; expired: boolean; has_refresh_token: boolean }> = {};
    for (const name of Object.keys(providers)) {
      statuses[name] = getProviderStatus(name);
    }
    ctx.response.status(200);
    ctx.response.header("Content-Type", "application/json");
    ctx.response.body(JSON.stringify(statuses));
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

  const status = getProviderStatus(providerName);
  ctx.response.status(200);
  ctx.response.header("Content-Type", "application/json");
  ctx.response.body(JSON.stringify(status));
}

function getProviderStatus(providerName: string): { connected: boolean; expired: boolean; has_refresh_token: boolean } {
  let data: string;
  try {
    data = gw.fs.read(providerName + ".json");
  } catch {
    return { connected: false, expired: false, has_refresh_token: false };
  }

  if (!data || data.trim() === "") {
    return { connected: false, expired: false, has_refresh_token: false };
  }

  try {
    const token = JSON.parse(data);
    const now = Math.floor(Date.now() / 1000);
    const expired = token.expires_at ? token.expires_at < now : false;
    const has_refresh_token = !!(token.refresh_token);
    return { connected: true, expired, has_refresh_token };
  } catch {
    return { connected: false, expired: false, has_refresh_token: false };
  }
}
