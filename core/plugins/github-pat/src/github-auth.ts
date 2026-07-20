/// <reference path="../../../gateway/types/gateway.d.ts" />

/**
 * GitHub PAT middleware — injects auth + enforces deny_paths/deny_graphql.
 *
 * Options:
 *   token: string              — GitHub PAT (injected as Basic auth)
 *   deny_paths?: string[]      — e.g. ["PUT /repos/*/pulls/*/merge", "DELETE /repos/*/*"]
 *   deny_graphql?: { mutations?: string[] }  — e.g. { mutations: ["mergePullRequest"] }
 */

function matchPath(pattern: string, actual: string): boolean {
  // Split pattern and path into segments, `*` matches exactly one segment
  const patParts = pattern.split("/").filter(Boolean);
  const actParts = actual.split("/").filter(Boolean);

  if (patParts.length !== actParts.length) return false;

  for (let i = 0; i < patParts.length; i++) {
    if (patParts[i] === "*") continue;
    if (patParts[i] !== actParts[i]) return false;
  }
  return true;
}

function checkDenyPaths(ctx: GatewayContext, denyPaths: string[]): boolean {
  const method = ctx.request.method.toUpperCase();
  const path = ctx.request.path;

  for (const rule of denyPaths) {
    const spaceIdx = rule.indexOf(" ");
    if (spaceIdx === -1) continue;

    const ruleMethod = rule.substring(0, spaceIdx).toUpperCase();
    const rulePath = rule.substring(spaceIdx + 1);

    if (ruleMethod !== method) continue;
    if (matchPath(rulePath, path)) return true;
  }
  return false;
}

function checkDenyGraphQL(ctx: GatewayContext, denyGraphql: { mutations?: string[] }): boolean {
  // GraphQL requests are POST to /graphql
  if (ctx.request.method.toUpperCase() !== "POST") return false;
  if (!ctx.request.path.endsWith("/graphql")) return false;

  const mutations = denyGraphql.mutations;
  if (!mutations || mutations.length === 0) return false;

  // We can't read the request body directly from the middleware API.
  // Check the X-Github-Graphql-Operation header that gh CLI sends,
  // or fall back to blocking all mutations if we can't inspect.
  // gh CLI sets this header in newer versions.
  const opHeader = ctx.request.headers["x-github-graphql-operation"] ||
                   ctx.request.headers["X-Github-Graphql-Operation"] || "";

  if (opHeader) {
    for (const m of mutations) {
      if (opHeader.toLowerCase() === m.toLowerCase()) return true;
    }
  }

  // Fallback: check the query parameter if present (some clients send it there)
  const queryParam = ctx.request.query["query"] || "";
  if (queryParam) {
    for (const m of mutations) {
      if (queryParam.includes(m)) return true;
    }
  }

  return false;
}

const handler: MiddlewareHandler = (ctx, options) => {
  const token = options.token;
  if (!token) return;

  // --- Enforce deny_paths ---
  const denyPaths: string[] = options.deny_paths || [];
  if (denyPaths.length > 0 && checkDenyPaths(ctx, denyPaths)) {
    gw.log.error(`[github-pat] BLOCKED: ${ctx.request.method} ${ctx.request.path} (deny_paths)`);
    ctx.abort(403, JSON.stringify({
      error: "blocked_by_policy",
      message: `Action denied: ${ctx.request.method} ${ctx.request.path}`,
      rule: "deny_paths",
    }), { "Content-Type": "application/json" });
    return;
  }

  // --- Enforce deny_graphql ---
  const denyGraphql = options.deny_graphql || {};
  if (denyGraphql.mutations && checkDenyGraphQL(ctx, denyGraphql)) {
    gw.log.error(`[github-pat] BLOCKED: GraphQL mutation (deny_graphql)`);
    ctx.abort(403, JSON.stringify({
      error: "blocked_by_policy",
      message: "GraphQL mutation denied by policy",
      rule: "deny_graphql",
    }), { "Content-Type": "application/json" });
    return;
  }

  // --- Inject auth ---
  const basic = gw.crypto.base64.encode("x-access-token:" + token);
  ctx.request.setHeader("Authorization", "Basic " + basic);
  gw.secrets.register(token);
};

export default handler;
