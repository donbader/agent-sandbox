# External Services Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow agents to reach pre-existing Docker containers via a new `external-services` feature plugin that joins external networks on the gateway.

**Architecture:** The plugin contributes external network names to a new `ExternalNetworks` field on `FeatureContributions`. The compose generator collects these and adds them to the gateway service's network list + top-level `networks:` section as `external: true`. Gateway DNS is updated to try Docker's embedded DNS (`127.0.0.11`) first.

**Tech Stack:** Go, YAML templates, go test + testify

---

### Task 1: Add ExternalNetworks to FeatureContributions

**Files:**
- Modify: `internal/resolve/plugin.go`

- [ ] **Step 1: Add the field**

Add `ExternalNetworks` to `FeatureContributions`:

```go
// In FeatureContributions struct, after CommandPluginDir:
ExternalNetworks []string // external Docker networks the gateway should join
```

- [ ] **Step 2: Verify build**

Run: `flox activate -- go build ./...`
Expected: PASS (no consumers yet)

- [ ] **Step 3: Commit**

```bash
git add internal/resolve/plugin.go
git commit -m "feat(resolve): add ExternalNetworks field to FeatureContributions"
```

---

### Task 2: Create the external-services plugin

**Files:**
- Create: `internal/plugins/external-services/plugin.go`
- Create: `internal/plugins/external-services/feature.yaml`
- Modify: `internal/plugins/register.go`

- [ ] **Step 1: Write plugin.go**

```go
// Package externalservices implements the external-services feature plugin.
// It exposes pre-existing Docker containers to the agent via the gateway's network.
package externalservices

import (
	"fmt"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

type ServiceConfig struct {
	Name    string `yaml:"name" schema:"Label and Docker DNS hostname for the service" required:"true"`
	Network string `yaml:"network" schema:"External Docker network the service is on" required:"true"`
}

type Config struct {
	Services []ServiceConfig `yaml:"services" schema:"External services to make reachable" required:"true"`
}

func init() {
	resolve.Register("external-services", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		if len(cfg.Services) == 0 {
			return nil, fmt.Errorf("external-services: at least one service is required")
		}

		var networks []string
		seen := map[string]bool{}
		for _, svc := range cfg.Services {
			if svc.Name == "" {
				return nil, fmt.Errorf("external-services: service name is required")
			}
			if svc.Network == "" {
				return nil, fmt.Errorf("external-services: network is required for service %q", svc.Name)
			}
			if !seen[svc.Network] {
				seen[svc.Network] = true
				networks = append(networks, svc.Network)
			}
		}

		return &resolve.FeatureContributions{
			ExternalNetworks: networks,
		}, nil
	})
}
```

- [ ] **Step 2: Write feature.yaml**

```yaml
name: external-services
description: Connect the agent to pre-existing Docker containers via external networks.
```

- [ ] **Step 3: Add import to register.go**

In `internal/plugins/register.go`, add the import:

```go
_ "github.com/donbader/agent-sandbox/internal/plugins/external-services"
```

- [ ] **Step 4: Verify build**

Run: `flox activate -- go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/external-services/ internal/plugins/register.go
git commit -m "feat(plugins): add external-services plugin"
```

---

### Task 3: Plumb ExternalNetworks through compose generation

**Files:**
- Modify: `internal/generate/compose.go`
- Modify: `internal/generate/helpers.go`
- Modify: `internal/generate/templates/docker-compose.gateway.tmpl`

- [ ] **Step 1: Add ExternalNetworks to ComposeBuilder**

In `internal/generate/compose.go`, add to the `ComposeBuilder` struct:

```go
ExternalNetworks []string // external Docker networks for gateway to join
```

And in `buildComposeBuilder()`, inside the `if g.Gateway {` block, after setting `cb.GatewayCertDir`:

```go
cb.ExternalNetworks = g.collectExternalNetworks()
```

- [ ] **Step 2: Add collectExternalNetworks helper**

In `internal/generate/helpers.go`:

```go
// collectExternalNetworks gathers deduplicated external network names from features.
func (g *Generator) collectExternalNetworks() []string {
	var networks []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, n := range f.ExternalNetworks {
			if !seen[n] {
				seen[n] = true
				networks = append(networks, n)
			}
		}
	}
	return networks
}
```

- [ ] **Step 3: Update compose template**

In `internal/generate/templates/docker-compose.gateway.tmpl`, update the gateway service networks section:

```
  {{ .GatewayName }}:
    build:
      context: .
      dockerfile: Dockerfile.gateway
    networks:
      internal:
      default:
{{- range .ExternalNetworks }}
      {{ . }}:
{{- end }}
```

And update the bottom `networks:` section:

```
networks:
  internal:
    internal: true
{{- range .ExternalNetworks }}
  {{ . }}:
    external: true
{{- end }}
```

- [ ] **Step 4: Verify build**

Run: `flox activate -- go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/compose.go internal/generate/helpers.go internal/generate/templates/docker-compose.gateway.tmpl
git commit -m "feat(generate): render external networks in gateway compose"
```

---

### Task 4: Update gateway DNS to try Docker embedded DNS first

**Files:**
- Modify: `gateway/internal/dns/dns.go`

- [ ] **Step 1: Change DNS forwarding to try 127.0.0.11 first**

Update `gateway/internal/dns/dns.go` to try Docker's embedded DNS before public DNS:

```go
// Package dns implements a simple DNS resolver that forwards queries upstream.
// It intercepts all DNS traffic from the agent to prevent DNS-based bypasses.
package dns

import (
	"fmt"
	"log/slog"
	"net"
)

// upstreamServers lists DNS servers to try in order.
// Docker embedded DNS resolves container names on joined networks.
// Public DNS resolves internet hostnames.
var upstreamServers = []string{"127.0.0.11:53", "8.8.8.8:53"}

// Server is a UDP DNS forwarder.
type Server struct {
	listen string
}

// NewServer creates a DNS server listening on the given address.
func NewServer(listen string) *Server {
	return &Server{listen: listen}
}

// ListenAndServe starts the DNS server.
func (s *Server) ListenAndServe() error {
	addr, err := net.ResolveUDPAddr("udp", s.listen)
	if err != nil {
		return fmt.Errorf("dns resolve addr: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("dns listen: %w", err)
	}
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 4096)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			slog.Debug("read error", "error", err)
			continue
		}

		query := make([]byte, n)
		copy(query, buf[:n])

		go s.handleQuery(conn, clientAddr, query)
	}
}

func (s *Server) handleQuery(conn *net.UDPConn, clientAddr *net.UDPAddr, query []byte) {
	slog.Debug("dns query", "client", clientAddr.String(), "size", len(query))

	resp := make([]byte, 4096)

	for i, upstream := range upstreamServers {
		upConn, err := net.Dial("udp", upstream)
		if err != nil {
			slog.Debug("dns dial upstream failed", "upstream", upstream, "error", err)
			continue
		}

		if _, err := upConn.Write(query); err != nil {
			_ = upConn.Close()
			slog.Debug("dns write upstream failed", "upstream", upstream, "error", err)
			continue
		}

		n, err := upConn.Read(resp)
		_ = upConn.Close()
		if err != nil {
			slog.Debug("dns read upstream failed", "upstream", upstream, "error", err)
			continue
		}

		// If Docker DNS returned an answer, use it immediately.
		// If NXDOMAIN from Docker DNS, try next upstream (public DNS).
		hasAnswer := n > 7 && (resp[6] > 0 || resp[7] > 0)
		isLast := i == len(upstreamServers)-1

		if hasAnswer || isLast {
			if _, err := conn.WriteToUDP(resp[:n], clientAddr); err != nil {
				slog.Error("dns write client", "error", err)
			}
			return
		}
	}

	slog.Error("dns all upstreams failed", "client", clientAddr.String())
}
```

- [ ] **Step 2: Verify build**

Run: `flox activate -- go build ./...`
Expected: PASS

- [ ] **Step 3: Run existing DNS tests**

Run: `flox activate -- go test ./gateway/internal/dns/...`
Expected: PASS (update tests if interface changed)

- [ ] **Step 4: Commit**

```bash
git add gateway/internal/dns/dns.go
git commit -m "feat(gateway/dns): try Docker embedded DNS before public DNS"
```

---

### Task 5: Write integration test for external-services plugin

**Files:**
- Create: `internal/plugins/external-services/plugin_test.go`

- [ ] **Step 1: Write tests**

```go
package externalservices

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalServices_ValidConfig(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"name": "rkgw", "network": "rkgw-external"},
			map[string]any{"name": "postgres", "network": "my-db-net"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"rkgw-external", "my-db-net"}, contrib.ExternalNetworks)
}

func TestExternalServices_DeduplicatesNetworks(t *testing.T) {
	contrib, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"name": "svc1", "network": "shared-net"},
			map[string]any{"name": "svc2", "network": "shared-net"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"shared-net"}, contrib.ExternalNetworks)
}

func TestExternalServices_EmptyServicesError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one service")
}

func TestExternalServices_MissingNameError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"network": "some-net"},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestExternalServices_MissingNetworkError(t *testing.T) {
	_, err := resolve.ResolveFeature(".", "external-services", "external-services", map[string]any{
		"services": []any{
			map[string]any{"name": "svc"},
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "network is required")
}
```

- [ ] **Step 2: Run tests**

Run: `flox activate -- go test ./internal/plugins/external-services/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/plugins/external-services/plugin_test.go
git commit -m "test(external-services): add plugin validation tests"
```

---

### Task 6: Write compose generation test

**Files:**
- Modify: `internal/generate/compose_test.go`

- [ ] **Step 1: Add test for external networks in compose output**

```go
func TestComposeGateway_ExternalNetworks(t *testing.T) {
	g := &Generator{
		Config:  &config.AgentConfig{Name: "test"},
		Runtime: &resolve.RuntimeConfig{BaseImage: "node:22-slim", AcpCmd: []string{"codex-acp"}},
		Features: []*resolve.FeatureContributions{
			{ExternalNetworks: []string{"rkgw-external", "my-db-net"}},
		},
		Gateway:        true,
		ChannelManager: false,
		GatewaySpec:    validGatewaySpec(),
		Dir:            t.TempDir(),
		OutDir:         t.TempDir(),
	}

	cb := g.buildComposeBuilder()
	assert.Equal(t, []string{"rkgw-external", "my-db-net"}, cb.ExternalNetworks)
}
```

(Use whatever `validGatewaySpec()` helper exists in the test file, or create one that satisfies validation.)

- [ ] **Step 2: Run tests**

Run: `flox activate -- go test ./internal/generate/... -run TestComposeGateway_ExternalNetworks`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/generate/compose_test.go
git commit -m "test(generate): verify external networks in compose builder"
```

---

### Task 7: End-to-end verification

**Files:** None (manual verification)

- [ ] **Step 1: Build CLI**

Run: `flox activate -- go build ./cmd/agent-sandbox/`

- [ ] **Step 2: Add external-services to test config**

Add to `/Users/corey/Projects/my-agent-team-v3/dorey/agent.yaml`:

```yaml
  - plugin: external-services
    services:
      - name: rkgw
        network: rkgw-deployment_default
```

(Use the actual network name from `docker network ls` for the rkgw gateway.)

- [ ] **Step 3: Generate and inspect compose**

Run: `./agent-sandbox generate -C /Users/corey/Projects/my-agent-team-v3`

Verify `.build/dorey/docker-compose.yml` contains:
- `rkgw-deployment_default:` under gateway's `networks:`
- `rkgw-deployment_default:` with `external: true` under top-level `networks:`

- [ ] **Step 4: Deploy and test connectivity**

Run: `./agent-sandbox -C /Users/corey/Projects/my-agent-team-v3 compose up --build -d`

Then verify: `docker exec agent-sandbox-dorey-gateway-1 ping -c1 rkgw` (or equivalent DNS resolution check)

- [ ] **Step 5: Commit config change**

```bash
cd /Users/corey/Projects/my-agent-team-v3
git add dorey/agent.yaml
git commit -m "feat: add rkgw as external service"
```
