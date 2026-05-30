# ADR-003: Go Proxy Over nginx/OpenResty

## Status
Accepted

## Context
We need an L7 proxy that can:
- Intercept TLS connections (read SNI, optionally MITM)
- Evaluate egress rules (first match wins)
- Inject credentials (headers, URL rewrite)
- Manage OAuth tokens (storage, auto-refresh)
- Handle transparent proxy mode (iptables redirect, SO_ORIGINAL_DST)

Options considered:
1. **Go proxy** (`goproxy` library or custom) — same language as CLI
2. **nginx + OpenResty** (Lua scripting) — battle-tested, high performance
3. **Envoy + ext_authz** — powerful but complex configuration
4. **mitmproxy** (Python) — great MITM support but heavy runtime

## Decision
Use a Go proxy (custom or based on `github.com/elazarl/goproxy`).

## Consequences

**Positive:**
- Same language as the CLI — can embed proxy in the same binary
- Single binary deployment (no nginx, no Lua, no Python runtime)
- Full programmatic control over TLS MITM, rule evaluation, OAuth refresh
- Easy to test (Go testing framework, table-driven tests)
- OAuth token refresh is natural in Go (goroutines, timers, channels)
- Credential storage in memory (single process, no shared state issues)

**Negative:**
- Less battle-tested than nginx for high-throughput proxying
- Must handle edge cases in HTTP/TLS ourselves
- No hot-reload without custom implementation

**Why not nginx/OpenResty:**
- Lua is painful for complex logic (OAuth refresh, token storage, error handling)
- Multi-worker architecture creates shared state problems for credential storage
- Separate deployment artifact from the CLI
- Harder to unit test Lua scripts

**Why acceptable:**
- We're proxying for a single agent (not thousands of clients)
- Performance isn't the bottleneck (agent makes ~10-100 requests/minute)
- Go's `crypto/tls` and `net/http` are well-tested standard library code
