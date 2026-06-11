# Gateway — Developer Notes

**Generated:** 2025-06-12 | **Commit:** a67c01e | **Branch:** main

## Overview

The gateway is a transparent egress proxy that intercepts all outbound HTTPS from the agent container via iptables DNAT. It performs TLS MITM on configured domains to inject credentials and rewrite requests, while passing all other traffic through untouched.

## TypeScript Middleware API

### Type Definitions

Types are auto-generated from `@ts-method` and `@ts-prop` annotations in Go source. Never edit `gateway.d.ts` directly.

```bash
# Regenerate after modifying the JS runtime API
go generate ./core/gateway/...
```

### Writing a Middleware

```typescript
/// <reference path="../../../gateway/types/gateway.d.ts" />

const handler: MiddlewareHandler = (ctx, options) => {
  const token = options.token;
  if (!token) return;

  ctx.request.setHeader("Authorization", "Bearer " + token);
  gw.secrets.register(token);
};
export default handler;
```

### Key Rules

- `ctx.request.path`, `ctx.request.url` are **read-only** — assignment is a silent no-op
- Use `ctx.request.setPath(newPath)` to rewrite URL paths
- Use `ctx.request.setHeader(key, val)` to modify headers
- Always call `gw.secrets.register(value)` for any secret injected into requests

### Adding New API Surface

When adding methods or properties to the JS runtime:

1. Add a `// @ts-method` or `// @ts-prop` annotation on the line before the `.Set()` call:
   ```go
   // @ts-method ctx.request.setFoo(bar: string): void
   _ = requestObj.Set("setFoo", func(call goja.FunctionCall) goja.Value { ... })
   ```
2. For structural wiring (assembling sub-objects), use `// @ts-skip`:
   ```go
   // @ts-skip (structural wiring)
   _ = gwObj.Set("crypto", cryptoObj)
   ```
3. Run `go generate ./core/gateway/...` to regenerate `gateway.d.ts`
4. Run `go run ./cmd/lint-ts-annotations` to verify all `.Set()` calls are annotated

### Annotation Format

| Annotation | Usage | Example |
|-----------|-------|---------|
| `@ts-method` | Callable functions | `// @ts-method gw.crypto.sha256(data: string): string` |
| `@ts-prop` | Read-only properties | `// @ts-prop ctx.request.path: readonly path: string` |
| `@ts-skip` | Structural wiring (no TS output) | `// @ts-skip (structural wiring)` |

### Path Convention

The annotation path determines which TypeScript interface receives the member:

| Path prefix | Interface |
|-------------|-----------|
| `ctx.request.*` | `GatewayRequest` |
| `ctx.response.*` | `GatewayResponse` |
| `ctx.*` | `GatewayContext` |
| `gw.crypto.*` | `GatewayCrypto` |
| `gw.crypto.base64.*` | `GatewayCryptoBase64` |
| `gw.crypto.base64url.*` | `GatewayCryptoBase64url` |
| `gw.fs.*` | `GatewayFS` |
| `gw.http.*` | `GatewayHTTP` |
| `gw.secrets.*` | `GatewaySecrets` |
| `gw.log.*` | `GatewayLog` |

## Module Layout

```
core/gateway/
├── cmd/gateway/main.go                 ← entrypoint, wiring
├── types/gateway.d.ts                  ← AUTO-GENERATED (go generate)
└── internal/
    ├── jsruntime/
    │   ├── request.go                  ← ctx.request + ctx.response + ctx.env/abort
    │   ├── hostapi.go                  ← gw.* (crypto, fs, http, secrets, log)
    │   └── vm.go                       ← goja VM wrapper
    ├── pluginloader/
    │   └── loader.go                   ← esbuild bundling, plugin registration
    ├── mitm/
    │   ├── mitm.go                     ← MITM handler, request forwarding
    │   └── cert.go                     ← CA generation, per-domain certs
    ├── proxy/
    │   ├── proxy.go                    ← TCP listener, SNI routing
    │   ├── http.go                     ← Plain HTTP proxy
    │   └── sni.go                      ← TLS ClientHello parsing
    ├── redact/
    │   └── handler.go                  ← slog secret scrubbing
    └── dns/
        └── dns.go                      ← DNS forwarder
```

## CI Checks

| Command | Purpose |
|---------|---------|
| `go run ./cmd/lint-ts-annotations` | Ensures all `.Set()` calls have `@ts-method`, `@ts-prop`, or `@ts-skip` |
| `go generate ./core/gateway/...` | Regenerates `gateway.d.ts` — CI should verify no diff |
| `go build ./core/gateway/...` | Standard build check |

## Docker Cache

The gateway Dockerfile includes `ARG GATEWAY_HASH=<sha256>` computed from the binary content at generate time. This ensures Docker invalidates the COPY layer when the binary changes across rebuilds.

## Key Design Decisions

- **Per-request VM isolation** — fresh goja VM per request, no shared mutable state
- **Read-only request properties** — `DefineDataProperty` with `writable=false` prevents silent assignment bugs
- **RawPath clearing** — `setPath()` clears `URL.RawPath` to ensure Go's `RequestURI()` uses the rewritten path
- **esbuild bundling** — TS plugins are transpiled to ES5 at gateway startup, not at generate time
- **No env var passthrough** — secrets are baked into `plugins.yaml` options, not container env
