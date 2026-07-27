/// <reference path="../../../gateway/types/gateway.d.ts" />

/**
 * AGW client middleware — injects STX Agent Gateway Bearer token.
 *
 * Intercepts outbound requests to the AGW host and replaces the dummy
 * x-api-key with a real Authorization: Bearer header. The token is
 * registered as a secret so it never appears in gateway logs.
 *
 * Options:
 *   token: string   — AGW bearer token (resolved from env var ref)
 *   host: string    — AGW hostname (default: agent-gateway.stx-ai.net)
 *   provider: string — AGW provider segment (default: kiro)
 */

const handler: MiddlewareHandler = (ctx, options) => {
  const token = options.token as string | undefined;

  if (!token) {
    gw.log.error("agw-client: token option is required but not set");
    ctx.abort(500, JSON.stringify({ error: "agw_token_missing", message: "AGW token is not configured" }));
    return;
  }

  // Register the real token for log scrubbing — must happen before any
  // request is forwarded so it's never captured in debug output.
  gw.secrets.register(token);

  // Replace dummy api key with real Bearer token.
  ctx.request.setHeader("Authorization", "Bearer " + token);

  // Remove the dummy anthropic key so it doesn't leak to AGW.
  ctx.request.setHeader("x-api-key", "");

  gw.log.debug("agw-client: injected Bearer token for AGW request");
};

export default handler;
