/**
 * Type definitions for agent-sandbox gateway middlewares and route handlers.
 *
 * Usage: reference this file in your middleware for autocompletion:
 *   /// <reference path="./gateway.d.ts" />
 */

/** Request context passed to middleware functions. */
interface GatewayRequest {
  /** HTTP method (read-only). Use cannot be reassigned. */
  readonly method: string;
  /** Full request URL (read-only). Use setPath() to modify. */
  readonly url: string;
  /** Request host (read-only). */
  readonly host: string;
  /** URL path (read-only). Use setPath() to modify. */
  readonly path: string;
  /** Query parameters (read-only). */
  readonly query: Record<string, string>;
  /** Request headers (read-only snapshot). Use setHeader() to modify. */
  readonly headers: Record<string, string>;
  /** Set or overwrite a request header. */
  setHeader(key: string, value: string): void;
  /** Rewrite the URL path before forwarding upstream. */
  setPath(newPath: string): void;
}

/** Response context for route handlers. */
interface GatewayResponse {
  /** Set the HTTP status code. */
  status(code: number): void;
  /** Set a response header. */
  header(key: string, value: string): void;
  /** Set the response body. */
  body(content: string): void;
}

/** Context object passed to middleware and route handler functions. */
interface GatewayContext {
  /** The incoming HTTP request. */
  request: GatewayRequest;
  /** The outgoing HTTP response (only available in route handlers). */
  response: GatewayResponse;
  /** Read a gateway environment variable. */
  env(key: string): string | undefined;
  /** Abort the request immediately with a status code and optional body. */
  abort(status: number, body?: string, headers?: Record<string, string>): void;
}

/** Cryptographic utilities. */
interface GatewayCrypto {
  /** SHA-256 hash. encoding: "hex" (default), "base64url", "base64". */
  sha256(data: string, encoding?: "hex" | "base64url" | "base64"): string;
  /** HMAC-SHA256, returns hex-encoded string. */
  hmac(key: string, data: string): string;
  /** Generate random bytes. Max 1MB. */
  randomBytes(n: number): Uint8Array;
  /** Base64 encoding/decoding (standard, with padding). */
  base64: {
    encode(data: string): string;
    decode(encoded: string): string;
  };
  /** Base64url encoding/decoding (no padding). */
  base64url: {
    encode(data: string): string;
    decode(encoded: string): string;
  };
}

/** Filesystem scoped to the plugin's data directory. */
interface GatewayFS {
  /** Read a file relative to the plugin data dir. */
  read(path: string): string;
  /** Write a file relative to the plugin data dir. */
  write(path: string, content: string): void;
}

/** HTTP fetch response. */
interface GatewayFetchResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

/** HTTP client. */
interface GatewayHTTP {
  /** Synchronous HTTP fetch. 30s timeout. Response body capped at 1MB. */
  fetch(url: string, opts?: {
    method?: string;
    body?: string;
    headers?: Record<string, string>;
  }): GatewayFetchResponse;
}

/** Secret management. */
interface GatewaySecrets {
  /** Register a value to be scrubbed from gateway logs. */
  register(secret: string): void;
}

/** Structured logging. */
interface GatewayLog {
  info(msg: string): void;
  error(msg: string): void;
  debug(msg: string): void;
}

/** Global gateway host API object. */
interface GatewayHostAPI {
  crypto: GatewayCrypto;
  fs: GatewayFS;
  http: GatewayHTTP;
  secrets: GatewaySecrets;
  log: GatewayLog;
}

/** Plugin options passed as the second argument to middleware/handler functions. */
type PluginOptions = Record<string, any>;

/** The global `gw` object available in all middlewares and route handlers. */
declare const gw: GatewayHostAPI;

/**
 * Middleware function signature.
 *
 * @example
 * ```typescript
 * const handler: MiddlewareHandler = (ctx, options) => {
 *   ctx.request.setHeader("Authorization", "Bearer " + options.token);
 *   gw.secrets.register(options.token);
 * };
 * export default handler;
 * ```
 */
type MiddlewareHandler = (ctx: GatewayContext, options: PluginOptions) => void;

/**
 * Route handler function signature.
 *
 * @example
 * ```typescript
 * const handler: RouteHandler = (ctx, options) => {
 *   ctx.response.status(200);
 *   ctx.response.header("Content-Type", "application/json");
 *   ctx.response.body(JSON.stringify({ ok: true }));
 * };
 * export default handler;
 * ```
 */
type RouteHandler = (ctx: GatewayContext, options: PluginOptions) => void;
