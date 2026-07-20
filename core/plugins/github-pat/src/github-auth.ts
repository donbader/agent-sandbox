/// <reference path="../../../gateway/types/gateway.d.ts" />

/**
 * GitHub PAT middleware — injects auth + enforces deny_paths/deny_graphql.
 *
 * Options:
 *   token: string              — GitHub PAT (injected as Basic auth)
 *   deny_paths?: string[]      — e.g. ["PUT /repos/*/*/pulls/*/merge"]
 *   deny_graphql?: { mutations?: string[] }  — BEST-EFFORT only (see note below)
 *
 * NOTE on deny_graphql:
 *   The gateway middleware API does not expose ctx.request.body, so GraphQL
 *   mutation detection relies on: (1) the X-Github-Graphql-Operation header
 *   that some clients (including `gh` CLI) send, and (2) query string params.
 *   A raw HTTP client that omits these headers can bypass this check.
 *   For full enforcement, the Go runtime needs ctx.request.body support.
 *   deny_paths blocking of REST equivalents (e.g. PUT /repos/*/*/pulls/*/merge)
 *   remains the primary enforcement mechanism.
 */

function normalizePath(path: string): string {
  // Resolve . and .. segments to prevent traversal bypass
  const parts = path.split("/");
  const resolved: string[] = [];
  for (const part of parts) {
    if (part === "." || part === "") continue;
    if (part === "..") {
      resolved.pop();
    } else {
      resolved.push(part);
    }
  }
  return "/" + resolved.join("/");
}

function matchPath(pattern: string, actual: string): boolean {
  const patParts = pattern.split("/").filter(Boolean);
  const actParts = actual.split("/").filter(Boolean);

  if (patParts.length !== actParts.length) return false;

  for (let i = 0; i < patParts.length; i++) {
    if (patParts[i] === "*") continue;
    if (patParts[i] !== actParts[i]) return false;
  }
  return true;
}

function checkDenyPaths(ctx: GatewayContext, denyPaths: string[]): string | null {
  const method = ctx.request.method.toUpperCase();
  const path = normalizePath(ctx.request.path);

  for (const rule of denyPaths) {
    const spaceIdx = rule.indexOf(" ");
    if (spaceIdx === -1) {
      gw.log.error(`[github-pat] malformed deny_paths rule (missing METHOD): "${rule}"`);
      continue;
    }

    const ruleMethod = rule.substring(0, spaceIdx).toUpperCase();
    const rulePath = rule.substring(spaceIdx + 1);

    if (ruleMethod !== method) continue;
    if (matchPath(rulePath, path)) return rule;
  }
  return null;
}

function checkDenyGraphQL(ctx: GatewayContext, denyGraphql: { mutations?: string[] }): string | null {
  // GraphQL requests are POST to /graphql or /api/graphql
  if (ctx.request.method.toUpperCase() !== "POST") return null;
  const path = normalizePath(ctx.request.path);
  if (!path.endsWith("/graphql")) return null;

  const mutations = denyGraphql.mutations;
  if (!mutations || mutations.length === 0) return null;

  // Check X-Github-Graphql-Operation header (gh CLI sends this)
  // Headers may be lowercase in the map depending on Go's canonicalization
  const headers = ctx.request.headers;
  const opHeader = headers["x-github-graphql-operation"] ||
                   headers["X-Github-Graphql-Operation"] ||
                   headers["X-GITHUB-GRAPHQL-OPERATION"] || "";

  if (opHeader) {
    const opLower = opHeader.toLowerCase();
    for (const m of mutations) {
      if (opLower === m.toLowerCase()) return m;
    }
  }

  // Check query parameters (some clients pass operation name here)
  const queryOp = ctx.request.query["operationName"] ||
                  ctx.request.query["operation_name"] || "";
  if (queryOp) {
    const qLower = queryOp.toLowerCase();
    for (const m of mutations) {
      if (qLower === m.toLowerCase()) return m;
    }
  }

  return null;
}

const handler: MiddlewareHandler = (ctx, options) => {
  const token = options.token;
  if (!token) return;

  // --- Enforce deny_paths ---
  const denyPaths: string[] = options.deny_paths || [];
  if (denyPaths.length > 0) {
    const matched = checkDenyPaths(ctx, denyPaths);
    if (matched) {
      gw.log.error(`[github-pat] BLOCKED: ${ctx.request.method} ${ctx.request.path} (matched: ${matched})`);
      ctx.abort(403, JSON.stringify({
        error: "blocked_by_policy",
        message: `Action denied: ${ctx.request.method} ${ctx.request.path}`,
        rule: "deny_paths",
      }), { "Content-Type": "application/json" });
      return;
    }
  }

  // --- Enforce deny_graphql (best-effort, see header comment) ---
  const denyGraphql = options.deny_graphql || {};
  if (denyGraphql.mutations) {
    const matched = checkDenyGraphQL(ctx, denyGraphql);
    if (matched) {
      gw.log.error(`[github-pat] BLOCKED: GraphQL mutation "${matched}" (best-effort detection)`);
      ctx.abort(403, JSON.stringify({
        error: "blocked_by_policy",
        message: `GraphQL mutation denied: ${matched}`,
        rule: "deny_graphql",
      }), { "Content-Type": "application/json" });
      return;
    }
  }

  // --- Inject auth (only after all deny checks pass) ---
  const basic = gw.crypto.base64.encode("x-access-token:" + token);
  ctx.request.setHeader("Authorization", "Basic " + basic);
  gw.secrets.register(token);
};

export default handler;
