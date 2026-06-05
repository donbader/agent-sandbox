# Gateway Internals

The gateway is a transparent egress proxy that runs as a separate container alongside the agent. All outbound HTTPS from the agent routes through it via iptables DNAT (port 443 → gateway:8443). Its job is to intercept specific TLS connections for credential injection while passing everything else through untouched.

## Architecture

```
Agent outbound TCP :443 → iptables DNAT → Gateway TCP listener (:8443)
                                               │
                                               ├─ Read TLS ClientHello
                                               ├─ Extract SNI (server name)
                                               │
                                               ├─ SNI matches MITM domain?
                                               │    YES → mitm.Handler (terminate TLS, rewrite HTTP, forward)
                                               │    NO  → passthrough (pipe bytes directly to upstream)
                                               │
Gateway health endpoint (:8080/health) ← Agent entrypoint polls before setting up iptables
```

## Module Layout

```
core/gateway/
├── cmd/gateway/main.go            ← entrypoint, wiring, health endpoint
├── middlewares/custom/stub.go     ← compilation target for all middleware
└── internal/
    ├── proxy/
    │   ├── config.go              ← Config structs, YAML loading
    │   ├── proxy.go               ← TCP listener, SNI routing, passthrough
    │   ├── http.go                ← Plain HTTP proxy handler
    │   ├── sni.go                 ← TLS ClientHello SNI extraction
    │   └── forward.go             ← Generic TCP port forwarder
    ├── mitm/
    │   ├── mitm.go                ← MITM handler (TLS termination + HTTP pipeline)
    │   └── cert.go                ← CA generation, on-demand cert generation, CertCache
    ├── redact/
    │   └── handler.go             ← slog handler that masks secrets in logs
    └── dns/
        └── dns.go                 ← UDP DNS forwarder

core/plugins/
├── github-pat/middlewares/        ← GitHub PAT auth (compile-time plugin)
└── mcp-oauth/middlewares/         ← OAuth token refresh (compile-time plugin)
```

## Key Interfaces

### RequestHandler (proxy package)

The central routing contract. The proxy iterates registered handlers on every connection:

```go
type RequestHandler interface {
    Matches(host string) bool
    Handle(clientConn net.Conn, initialData []byte, serverName string)
}
```

### Middleware (sdk/gateway package)

All request modification flows through a unified middleware system with domain scoping:

```go
type MiddlewareDef struct {
    Name    string
    Domains []string
    Func    MiddlewareFunc
}

type MiddlewareFunc func(ctx *MiddlewareContext) error
```

All middleware is compiled into the gateway binary at Docker build time. There are no runtime "built-in" types — everything uses the same `init()` self-registration pattern:

- **Auth-header middleware** — generated as `.go` files by the CLI from `gateway.services[].headers` config
- **OAuth middleware** — ships as a plugin template in `core/plugins/mcp-oauth/middlewares/oauth.go`
- **Custom middleware** — user-provided `.go` templates declared in `plugin.yaml`

Domain-scoped: each middleware only runs for requests matching its configured domains.

## Connection Flow

1. Agent makes outbound TCP connection (e.g., `curl https://github.com`)
2. iptables DNAT rule redirects port 443 → gateway:8443
3. Gateway's TCP listener accepts the connection
4. Reads first 4096 bytes — expects a TLS ClientHello
5. Calls `extractSNI()` to parse the server name from the ClientHello
6. Iterates registered `RequestHandler`s, calling `Matches(serverName)`
7. **Match found** → delegates to handler (currently only `mitm.Handler`)
8. **No match** → passthrough: dials `serverName:443`, replays the ClientHello bytes, then pipes bidirectionally (`io.Copy` both directions with half-close)

The passthrough path preserves end-to-end TLS — the gateway never sees plaintext for non-MITM'd hosts.

## DNS

The gateway includes a DNS forwarder on port 53, but in the current v1 architecture DNS resolution uses Docker's built-in DNS. The agent's `/etc/resolv.conf` points to Docker's embedded DNS server (127.0.0.11), which resolves container names (e.g., `gateway`) automatically.

The gateway DNS server exists as infrastructure for future use cases (DNS-based routing, filtering) but is not required for the transparent proxy to function.

## MITM Pipeline

When a connection matches a MITM domain, `mitm.Handler` takes over:

1. **Certificate generation** — At startup, the gateway generates an ECDSA P-256 CA keypair and writes the CA cert to a shared volume (`/shared/certs/ca.crt`). Per-domain leaf certs are generated on demand via `CertCache.GetOrCreate(domain, caCert)` (thread-safe, double-checked locking).
2. **TLS handshake** — wraps the client connection in a `prefixConn` (replays the already-read ClientHello bytes), then performs a `tls.Server` handshake using the generated cert.
3. **HTTP request loop** (keep-alive aware):
   - `http.ReadRequest` from the decrypted stream
   - Call `applyMiddleware(req)` — finds matching middleware by domain, executes in order
   - Forward the modified request to real upstream via fresh TLS connection
   - Write upstream response back to agent over the MITM'd connection
   - Repeat until `Connection: close` or EOF

The agent's TLS client trusts the gateway CA (installed at container startup via `update-ca-certificates` from the shared certs volume), so the MITM is transparent.

## Log Redaction

Gateway logs are protected with two layers via the `redact.Handler` (wraps `slog`):

1. **Key-based** — attributes named `token`, `authorization`, `api_key`, etc. are always redacted
2. **Value-based** — all string attribute values are scanned for known secret substrings (collected from middleware via `gateway.RegisterSecret()` at startup) and replaced with `[REDACTED]`

This prevents credentials from leaking into container logs even if a handler inadvertently logs request details.

## Adding New Middleware

All middleware follows the same pattern: a `.go` file in `package custom` with an `init()` function that calls `gateway.RegisterMiddleware()`. The gateway binary is recompiled during Docker build, so new middleware is picked up automatically when you `agent-sandbox compose up --build`.

### As a plugin (recommended)

1. Create a directory under `core/plugins/<name>/middlewares/`
2. Write a `.go` file using Go template syntax for configuration:

```go
package custom

import (
    "strings"
    "github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func init() {
    secret := "{{ .options.api_key }}"
    domains := strings.Split("{{ .domainsList }}", ",")

    gateway.RegisterSecret(secret)
    gateway.RegisterMiddleware(gateway.MiddlewareDef{
        Name:    "my-middleware",
        Domains: domains,
        Func: func(ctx *gateway.MiddlewareContext) error {
            ctx.Request.Header.Set("Authorization", "Bearer "+secret)
            return nil
        },
    })
}
```

3. Declare the middleware in `plugin.yaml`:

```yaml
contributes:
  gateway:
    services:
      - url: https://api.example.com
        middlewares:
          - custom: "./middlewares/my-middleware.go"
```

### Via gateway.services headers (auto-generated)

For simple header injection, just declare headers in `agent.yaml`:

```yaml
gateway:
  services:
    - url: https://api.example.com
      headers:
        Authorization: "Bearer ${MY_API_KEY}"
```

The CLI generates a self-registering `.go` file at build time that bakes in the secret value. Template variables supported in header values:
- `${value}` — raw env var value
- `${base64_basic}` — base64("x-access-token:\<value\>") for git HTTP auth

### Design principles

- No runtime env var lookup — secrets are compiled into the gateway binary
- The gateway container needs no env var passthrough for middleware secrets
- Middleware without `{{` delimiters is copied as-is (backward compatible)
- No `type` field or runtime switch — all middleware self-registers at compile time
