/**
 * Auto-generated gateway type definitions.
 * DO NOT EDIT — regenerate with: go generate ./core/gateway/...
 *
 * Source annotations: @ts-method and @ts-prop in core/gateway/internal/jsruntime/
 */

interface GatewayCryptoBase64 {
  encode(data: string): string;
  decode(encoded: string): string;
}

interface GatewayCryptoBase64url {
  encode(data: string): string;
  decode(encoded: string): string;
}

interface GatewayFS {
  read(path: string): string;
  write(path: string, content: string): void;
}

interface GatewayHTTP {
  fetch(url: string, opts?: { method?: string; body?: string; headers?: Record<string, string> }): { status: number; headers: Record<string, string>; body: string };
}

interface GatewayLog {
  info(msg: string): void;
  error(msg: string): void;
  debug(msg: string): void;
}

interface GatewayRequest {
  readonly method: string;
  readonly url: string;
  readonly host: string;
  readonly path: string;
  readonly query: Record<string, string>;
  readonly headers: Record<string, string>;
  readonly body: string;
  setHeader(key: string, value: string): void;
  setPath(newPath: string): void;
}

interface GatewayResponse {
  status(code: number): void;
  header(key: string, value: string): void;
  body(content: string): void;
}

interface GatewaySecrets {
  register(secret: string): void;
}

interface GatewayContext {
  request: GatewayRequest;
  response: GatewayResponse;
  env(key: string): string | undefined;
  abort(status: number, body?: string, headers?: Record<string, string>): void;
}

interface GatewayCrypto {
  sha256(data: string, encoding?: "hex" | "base64url" | "base64"): string;
  hmac(key: string, data: string): string;
  randomBytes(n: number): Uint8Array;
  base64: GatewayCryptoBase64;
  base64url: GatewayCryptoBase64url;
}

interface GatewayHostAPI {
  crypto: GatewayCrypto;
  fs: GatewayFS;
  http: GatewayHTTP;
  secrets: GatewaySecrets;
  log: GatewayLog;
}

type PluginOptions = Record<string, any>;

declare const gw: GatewayHostAPI;

type MiddlewareHandler = (ctx: GatewayContext, options: PluginOptions) => void;
type RouteHandler = (ctx: GatewayContext, options: PluginOptions) => void;
