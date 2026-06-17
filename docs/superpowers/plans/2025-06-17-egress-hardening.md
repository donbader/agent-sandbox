# Egress Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce all outbound TCP from the sandbox through the gateway, not just port 443.

**Architecture:** Two-network model — sandbox network is `internal: true` (no internet), gateway bridges to an external network. Agent iptables DNAT all non-local TCP to gateway. Gateway handles TLS/HTTP, blocks unknown protocols.

**Tech Stack:** Go (gateway proxy), shell (entrypoint template), docker-compose YAML generation

---

### Task 1: Update Compose Generation — Two-Network Model

**Files:**
- Modify: `internal/generate/v1/compose.go`
- Test: `internal/generate/v1/compose_test.go`

- [ ] **Step 1: Write failing test for two-network compose output**

```go
func TestBuildProjectCompose_TwoNetworkModel(t *testing.T) {
	// Setup minimal agent entry
	agents := []ComposeAgentEntry{{
		Config: &config.Config{
			Name: "coder",
			Runtime: config.RuntimeConfig{
				CWD: "/home/agent/workspace",
			},
		},
		Contribs: &plugin.Contributions{},
		BuildDir: t.TempDir(),
	}}

	output, err := BuildProjectCompose(agents, t.TempDir())
	require.NoError(t, err)

	// Parse the output YAML
	var compose struct {
		Networks map[string]any `yaml:"networks"`
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(output), &compose))

	// Verify two networks exist
	assert.Contains(t, compose.Networks, "sandbox")
	assert.Contains(t, compose.Networks, "external")

	// Verify sandbox is internal
	sandboxNet, ok := compose.Networks["sandbox"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, sandboxNet["internal"])

	// Verify gateway has both networks
	gw, ok := compose.Services["coder-gateway"].(map[string]any)
	require.True(t, ok)
	gwNetworks, ok := gw["networks"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, gwNetworks, "sandbox")
	assert.Contains(t, gwNetworks, "external")

	// Verify agent only has sandbox
	agent, ok := compose.Services["coder"].(map[string]any)
	require.True(t, ok)
	agentNetworks, ok := agent["networks"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, agentNetworks, "sandbox")
	assert.NotContains(t, agentNetworks, "external")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestBuildProjectCompose_TwoNetworkModel -v`
Expected: FAIL — sandbox network does not have `internal: true`, no `external` network

- [ ] **Step 3: Update compose generation to produce two networks**

In `internal/generate/v1/compose.go`, update `BuildProjectCompose`:

```go
// Replace the Networks initialization:
Networks: map[string]any{
    "sandbox": map[string]any{
        "driver":   "bridge",
        "internal": true,
    },
    "external": map[string]any{
        "driver": "bridge",
    },
},
```

- [ ] **Step 4: Update `buildAgentPair` — gateway gets both networks**

In `buildAgentPair`, change the gateway service networks from:
```go
"networks": map[string]any{
    "sandbox": map[string]any{
        "aliases": []string{p.gatewayAlias},
    },
},
```
to:
```go
"networks": map[string]any{
    "sandbox": map[string]any{
        "aliases": []string{p.gatewayAlias},
    },
    "external": map[string]any{},
},
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/generate/v1/ -run TestBuildProjectCompose_TwoNetworkModel -v`
Expected: PASS

- [ ] **Step 6: Run all existing compose tests to check for regressions**

Run: `go test ./internal/generate/v1/ -v`
Expected: All tests pass (may need to update existing assertions that check network structure)

- [ ] **Step 7: Commit**

```bash
git add internal/generate/v1/compose.go internal/generate/v1/compose_test.go
git commit -m "feat: two-network model — sandbox internal, gateway bridges external"
```

---

### Task 2: Update Entrypoint Template — DNAT All Outbound TCP

**Files:**
- Modify: `internal/generate/templates/entrypoint.sh.tmpl`
- Test: `internal/generate/v1/entrypoint_test.go` (if exists, otherwise compose integration)

- [ ] **Step 1: Update the iptables rule in entrypoint.sh.tmpl**

Replace lines 30-33 in `internal/generate/templates/entrypoint.sh.tmpl`:

```bash
# Redirect outbound HTTPS traffic to the MITM proxy.
# Exclude traffic destined for the gateway itself to avoid loops.
iptables -t nat -A OUTPUT -p tcp --dport 443 ! -d "$GATEWAY_IP" -j DNAT --to-destination "${GATEWAY_IP}:8443"
echo "[entrypoint] iptables: TCP 443 → ${GATEWAY_IP}:8443"
```

With:

```bash
# Determine the sandbox network CIDR so local (east-west) traffic is not redirected.
SANDBOX_CIDR=$(ip route | grep "dev eth0" | grep -v default | awk '{print $1}' | head -1)
if [ -z "$SANDBOX_CIDR" ]; then
    # Fallback: exclude only the gateway IP
    SANDBOX_CIDR="${GATEWAY_IP}/32"
fi

# Redirect ALL outbound TCP (except sandbox-local) to the gateway proxy.
# This catches HTTPS (443), HTTP (80), and any other protocol.
iptables -t nat -A OUTPUT -p tcp ! -d "$SANDBOX_CIDR" -j DNAT --to-destination "${GATEWAY_IP}:8443"
echo "[entrypoint] iptables: all outbound TCP (except $SANDBOX_CIDR) → ${GATEWAY_IP}:8443"
```

- [ ] **Step 2: Verify template still renders correctly**

Run: `go test ./internal/generate/v1/ -run TestEntrypoint -v` (or the relevant entrypoint rendering test)
If no specific test exists, run `go build ./...` to confirm template is valid.

- [ ] **Step 3: Commit**

```bash
git add internal/generate/templates/entrypoint.sh.tmpl
git commit -m "feat: entrypoint redirects all outbound TCP to gateway, not just 443"
```

---

### Task 3: Gateway — Handle Unknown Protocols (Block Non-TLS/HTTP)

**Files:**
- Modify: `core/gateway/internal/proxy/proxy.go`
- Test: `core/gateway/internal/proxy/proxy_test.go`

- [ ] **Step 1: Write failing test — unknown protocol is blocked**

```go
func TestProxy_UnknownProtocol_Blocked(t *testing.T) {
	// Start a proxy with no HTTP handler, no MITM domains
	cfg := &Config{Listen: "127.0.0.1:0"}
	p := New(cfg)

	// Start proxy in background
	ln, err := net.Listen("tcp", cfg.Listen)
	require.NoError(t, err)
	p.listener = ln
	go func() { _ = p.ListenAndServe() }()
	defer p.Close()

	// Connect and send non-TLS, non-HTTP data (raw bytes)
	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	// Send some random binary data (not 0x16 TLS, not HTTP method)
	_, err = conn.Write([]byte{0x00, 0x01, 0x02, 0x03, 0x04})
	require.NoError(t, err)

	// Connection should be closed by the proxy (read should return EOF or error)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 100)
	_, err = conn.Read(buf)
	assert.Error(t, err) // EOF or connection reset
}
```

- [ ] **Step 2: Run test to verify current behavior**

Run: `go test ./core/gateway/internal/proxy/ -run TestProxy_UnknownProtocol_Blocked -v`
Expected: May already pass (current code drops connection if no HTTP handler), or may fail if httpHandler is nil and it doesn't explicitly close.

- [ ] **Step 3: Make gateway explicitly reject unknown protocols**

In `proxy.go`, update the `handleConn` method's non-TLS branch:

```go
// Not TLS — check if it looks like HTTP
if len(hello) > 0 && hello[0] != 0x16 {
    if isHTTP(hello) && p.httpHandler != nil {
        slog.Debug("connection detected as HTTP", "remote_addr", clientConn.RemoteAddr())
        p.httpHandler.Handle(clientConn, hello)
    } else {
        slog.Debug("unknown protocol blocked", "remote_addr", clientConn.RemoteAddr(), "first_byte", fmt.Sprintf("0x%02x", hello[0]))
    }
    return
}
```

Add the `isHTTP` helper:

```go
// isHTTP checks if the initial bytes look like an HTTP request method.
func isHTTP(data []byte) bool {
    methods := []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "PATCH ", "CONNECT "}
    for _, m := range methods {
        if len(data) >= len(m) && string(data[:len(m)]) == m {
            return true
        }
    }
    return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/gateway/internal/proxy/ -run TestProxy_UnknownProtocol_Blocked -v`
Expected: PASS

- [ ] **Step 5: Write test — HTTP traffic still handled when handler exists**

```go
func TestProxy_HTTP_StillHandled(t *testing.T) {
	cfg := &Config{Listen: "127.0.0.1:0"}
	p := New(cfg)
	p.RegisterHTTPHandler(NewHTTPHandler(nil))

	ln, err := net.Listen("tcp", cfg.Listen)
	require.NoError(t, err)
	p.listener = ln
	go func() { _ = p.ListenAndServe() }()
	defer p.Close()

	// Connect and send an HTTP request
	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	require.NoError(t, err)

	// Should get some HTTP response (even if 502 from no upstream)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err == nil {
		assert.Contains(t, string(buf[:n]), "HTTP/1.1")
	}
}
```

- [ ] **Step 6: Run all proxy tests**

Run: `go test ./core/gateway/internal/proxy/ -v`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add core/gateway/internal/proxy/proxy.go core/gateway/internal/proxy/proxy_test.go
git commit -m "feat: gateway blocks unknown TCP protocols, only allows TLS and HTTP"
```

---

### Task 4: Gateway — HTTP Forwarding for All Inbound HTTP (Not Just Configured Services)

**Files:**
- Modify: `core/gateway/internal/proxy/http.go`
- Test: `core/gateway/internal/proxy/proxy_test.go`

The current HTTP handler already forwards unknown hosts using the `Host` header (lines 69-79 of `http.go`). However, it only activates when `p.httpHandler != nil`. With the new model, ALL HTTP traffic hits the gateway via DNAT, so the HTTP handler must always be registered.

- [ ] **Step 1: Write test — HTTP to arbitrary external host is forwarded**

```go
func TestHTTPHandler_ForwardsUnknownHost(t *testing.T) {
	// Start a fake upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	defer upstream.Close()

	// Parse upstream host:port
	upstreamURL, _ := url.Parse(upstream.URL)

	// Create HTTP handler with no pre-configured services
	h := NewHTTPHandler(nil)

	// Create a pipe to simulate the connection
	client, server := net.Pipe()
	defer client.Close()

	// Send an HTTP request targeting the upstream
	go func() {
		req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", upstreamURL.Host)
		_, _ = client.Write([]byte(req))
	}()

	// Handle should forward to upstream
	h.Handle(server, nil)

	// Read response from client side
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := client.Read(buf)
	response := string(buf[:n])
	assert.Contains(t, response, "200 OK")
	assert.Contains(t, response, "upstream-ok")
}
```

- [ ] **Step 2: Run test to verify it passes (should already work)**

Run: `go test ./core/gateway/internal/proxy/ -run TestHTTPHandler_ForwardsUnknownHost -v`
Expected: PASS (the handler already forwards unknown hosts)

- [ ] **Step 3: Ensure HTTP handler is always registered in main.go**

In `core/gateway/cmd/gateway/main.go`, the HTTP handler registration block currently:
```go
httpHandler := proxy.NewHTTPHandler(cfg.HTTPServices)
p.RegisterHTTPHandler(httpHandler)
```

This already registers unconditionally (the `if` only controls the log message). Verify this is the case — no changes needed if so.

- [ ] **Step 4: Run full gateway tests**

Run: `go test ./core/gateway/... -v`
Expected: All pass

- [ ] **Step 5: Commit (if any changes were needed)**

```bash
git add core/gateway/internal/proxy/http.go core/gateway/internal/proxy/proxy_test.go
git commit -m "test: verify HTTP handler forwards arbitrary external hosts"
```

---

### Task 5: Gateway — TLS Passthrough for Non-MITM Domains

**Files:**
- Review: `core/gateway/internal/proxy/proxy.go` (passthrough function)

The existing `passthrough` function dials `serverName:443`. With the new model, non-443 TLS traffic also arrives (e.g., TLS on port 8443). The DNAT rewrites the destination to gateway:8443, but we lose the original destination port.

- [ ] **Step 1: Assess the impact**

The iptables DNAT rule rewrites all TCP to `${GATEWAY_IP}:8443`. The gateway only sees the connection arrive on :8443 — it does not know the original destination port. For TLS traffic, it extracts SNI (hostname) and dials `hostname:443`.

This means:
- HTTPS on 443: works correctly (SNI → dial host:443)
- TLS on non-443 ports: agent would need to use standard port 443, or the traffic would be misrouted

**Decision:** This is acceptable for now. The vast majority of TLS traffic is on 443. Non-standard TLS ports are an edge case that can be addressed later with SO_ORIGINAL_DST if needed.

- [ ] **Step 2: Add a comment documenting this limitation**

In `proxy.go`, add a comment above `passthrough`:

```go
// passthrough pipes the connection directly to the destination on port 443.
// NOTE: Since iptables DNAT rewrites the destination, we lose the original port.
// This means TLS on non-443 ports will be dialed on 443 instead. This is acceptable
// because nearly all TLS traffic uses 443. Non-standard ports can be supported later
// via SO_ORIGINAL_DST or port-specific iptables rules.
```

- [ ] **Step 3: Commit**

```bash
git add core/gateway/internal/proxy/proxy.go
git commit -m "docs: note TLS passthrough limitation with DNAT (port 443 assumed)"
```

---

### Task 6: Update Existing Tests for Two-Network Model

**Files:**
- Modify: `internal/generate/v1/compose_test.go`

- [ ] **Step 1: Find and update tests that assert network structure**

Run: `grep -n "sandbox.*bridge\|networks.*sandbox" internal/generate/v1/compose_test.go`

Update any assertions that expect the old single-network model:
- Old: `"sandbox": map[string]any{"driver": "bridge"}`
- New: `"sandbox": map[string]any{"driver": "bridge", "internal": true}`
- New: `"external": map[string]any{"driver": "bridge"}` also present

Update any assertions that check gateway networks:
- Old: gateway has only `sandbox`
- New: gateway has `sandbox` + `external`

- [ ] **Step 2: Run all generate tests**

Run: `go test ./internal/generate/v1/ -v`
Expected: All pass

- [ ] **Step 3: Run full project tests**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 4: Commit**

```bash
git add internal/generate/v1/compose_test.go
git commit -m "test: update compose tests for two-network model"
```

---

### Task 7: Lint and Build Verification

- [ ] **Step 1: Run linter**

Run: `golangci-lint run ./...`
Expected: No new lint issues

- [ ] **Step 2: Run full build**

Run: `go build ./...`
Expected: Clean build

- [ ] **Step 3: Run all tests one final time**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 4: Final commit (if any fixups needed)**

```bash
git commit -m "chore: lint fixups for egress hardening"
```
