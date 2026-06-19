# Unified Plugin Gateway Interface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace plugin `contributes.gateway.services` + `contributes.gateway.middlewares` with a unified `contributes.gateway.egress` format matching the user-facing schema, with `middlewares` attached directly to egress rules.

**Architecture:** Add `Middlewares []MiddlewareEntry` to `EgressRule` struct. Replace `GatewayContrib.Services` and `GatewayContrib.Middlewares` with `GatewayContrib.Egress []EgressRule`. Update generator to merge plugin egress rules into user egress rules. Update gateway runtime to derive middleware domains from egress rules. Add `migrate` command for automatic conversion.

**Tech Stack:** Go 1.22+, YAML, cobra CLI, testify

---

### Task 1: Add MiddlewareEntry to EgressRule

**Files:**
- Modify: `internal/config/egress.go:13-20` (EgressRule struct)
- Modify: `internal/config/egress.go:182-185` (NeedsMITM)
- Test: `internal/config/egress_test.go`

- [ ] **Step 1: Write failing test for NeedsMITM with middlewares**

```go
// In internal/config/egress_test.go — add new test
func TestNeedsMITM_WithMiddlewares(t *testing.T) {
	rule := EgressRule{
		Hosts: []string{"api.telegram.org"},
		Middlewares: []MiddlewareEntry{
			{Script: "./src/rewrite.ts"},
		},
	}
	assert.True(t, rule.NeedsMITM())
}

func TestNeedsMITM_NoMiddlewares(t *testing.T) {
	rule := EgressRule{
		Hosts: []string{"example.com"},
	}
	assert.False(t, rule.NeedsMITM())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `flox activate -- go test ./internal/config/ -run TestNeedsMITM_WithMiddlewares -v`
Expected: FAIL — `MiddlewareEntry` undefined

- [ ] **Step 3: Add MiddlewareEntry type and Middlewares field to EgressRule**

```go
// In internal/config/egress.go — add before EgressRule struct

// MiddlewareEntry declares a TypeScript middleware attached to an egress rule.
type MiddlewareEntry struct {
	Script string `yaml:"script" json:"script" jsonschema:"required,title=script,description=Path to TypeScript middleware file"`
}
```

Update EgressRule struct to add Middlewares field after Target:

```go
type EgressRule struct {
	Hosts       []string          `yaml:"hosts" json:"hosts" jsonschema:"required,title=hosts,description=Host patterns to match (domain globs or CIDRs). Use ['*'] as catch-all."`
	Deny        bool              `yaml:"deny,omitempty" json:"deny,omitempty" jsonschema:"title=deny,description=If true block matching traffic"`
	Headers     map[string]string `yaml:"headers,omitempty" json:"headers,omitempty" jsonschema:"title=headers,description=Headers injected by gateway (implies MITM + allow)"`
	DenyPaths   []string          `yaml:"deny_paths,omitempty" json:"deny_paths,omitempty" jsonschema:"title=deny_paths,description=URL path patterns to block (implies MITM). Format: METHOD /path/glob or /path/glob"`
	Middlewares []MiddlewareEntry `yaml:"middlewares,omitempty" json:"middlewares,omitempty" jsonschema:"title=middlewares,description=TypeScript middleware scripts (implies MITM)"`
	Network     string            `yaml:"network,omitempty" json:"network,omitempty" jsonschema:"title=network,description=Compose network to attach gateway to (for internal services)"`
	Target      string            `yaml:"target,omitempty" json:"target,omitempty" jsonschema:"title=target,description=Forwarding destination (host:port) for internal services. Omit for standard HTTPS passthrough."`
}
```

Update NeedsMITM:

```go
func (r *EgressRule) NeedsMITM() bool {
	return len(r.Headers) > 0 || len(r.DenyPaths) > 0 || len(r.Middlewares) > 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `flox activate -- go test ./internal/config/ -run TestNeedsMITM -v`
Expected: PASS

- [ ] **Step 5: Add validation rule — deny + middlewares conflict**

```go
// In ValidateEgressRules, add after the deny+deny_paths check:
if rule.Deny && len(rule.Middlewares) > 0 {
	errs = append(errs, fmt.Sprintf("gateway.egress[%d]: cannot have both deny: true and middlewares", i))
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/egress.go internal/config/egress_test.go
git commit -m "feat: add Middlewares field to EgressRule (implies MITM)"
```

---

### Task 2: Replace GatewayContrib with Egress-based struct

**Files:**
- Modify: `internal/plugin/types.go:86-111`
- Modify: `internal/plugin/merge.go:38-42`
- Test: `internal/plugin/merge_test.go`

- [ ] **Step 1: Update GatewayContrib struct**

Replace the struct in `internal/plugin/types.go`:

```go
type GatewayContrib struct {
	Egress            []config.EgressRule `yaml:"egress"`
	NamespacedVolumes []string            `yaml:"namespaced_volumes"`
	RawVolumes        []string            `yaml:"raw_volumes"`
	Routes            []RouteEntry        `yaml:"routes"`
}
```

Remove `GatewayService` and `GatewayMiddleware` types entirely (lines 101-111).

- [ ] **Step 2: Update MergeContributions in merge.go**

Replace lines 38-42:

```go
merged.Gateway.Egress = append(merged.Gateway.Egress, c.Gateway.Egress...)
merged.Gateway.NamespacedVolumes = append(merged.Gateway.NamespacedVolumes, c.Gateway.NamespacedVolumes...)
merged.Gateway.RawVolumes = append(merged.Gateway.RawVolumes, c.Gateway.RawVolumes...)
merged.Gateway.Routes = append(merged.Gateway.Routes, c.Gateway.Routes...)
```

- [ ] **Step 3: Write test for merged egress rules**

```go
func TestMergeContributions_GatewayEgress(t *testing.T) {
	a := &Contributions{
		Gateway: GatewayContrib{
			Egress: []config.EgressRule{
				{Hosts: []string{"api.github.com"}, Middlewares: []config.MiddlewareEntry{{Script: "./src/auth.ts"}}},
			},
		},
	}
	b := &Contributions{
		Gateway: GatewayContrib{
			Egress: []config.EgressRule{
				{Hosts: []string{"api.telegram.org"}, Middlewares: []config.MiddlewareEntry{{Script: "./src/rewrite.ts"}}},
			},
		},
	}

	merged := MergeContributions(a, b)

	require.Len(t, merged.Gateway.Egress, 2)
	assert.Equal(t, "api.github.com", merged.Gateway.Egress[0].Hosts[0])
	assert.Equal(t, "api.telegram.org", merged.Gateway.Egress[1].Hosts[0])
}
```

- [ ] **Step 4: Fix all compilation errors**

Run: `flox activate -- go build ./...`

This will surface every reference to the old `GatewayContrib.Services`, `GatewayContrib.Middlewares`, `GatewayService`, and `GatewayMiddleware` types. Fix each one (detailed in subsequent tasks).

- [ ] **Step 5: Run tests**

Run: `flox activate -- go test ./internal/plugin/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/plugin/types.go internal/plugin/merge.go internal/plugin/merge_test.go
git commit -m "refactor: replace GatewayContrib.Services/Middlewares with Egress"
```

---

### Task 3: Update BuildGatewayConfig to merge plugin egress rules

**Files:**
- Modify: `internal/generate/v1/gateway_config.go:17-128`
- Test: `internal/generate/v1/gateway_config_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestBuildGatewayConfig_PluginEgressRules(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Egress: []config.EgressRule{
				{Hosts: []string{"api.example.com"}, Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
				{Hosts: []string{"*"}},
			},
		},
	}

	pluginContribs := &plugin.Contributions{
		Gateway: plugin.GatewayContrib{
			Egress: []config.EgressRule{
				{Hosts: []string{"api.telegram.org"}, Middlewares: []config.MiddlewareEntry{{Script: "./src/rewrite.ts"}}},
			},
		},
	}

	gwCfg := BuildGatewayConfig(cfg, pluginContribs)

	// Plugin rule should be inserted before catch-all
	require.Len(t, gwCfg.EgressRules, 3)
	assert.Equal(t, []string{"api.example.com"}, gwCfg.EgressRules[0].Hosts)
	assert.Equal(t, []string{"api.telegram.org"}, gwCfg.EgressRules[1].Hosts)
	assert.Equal(t, []string{"*"}, gwCfg.EgressRules[2].Hosts)
	// Middleware should be preserved on the rule
	require.Len(t, gwCfg.EgressRules[1].Middlewares, 1)
	assert.Equal(t, "./src/rewrite.ts", gwCfg.EgressRules[1].Middlewares[0].Script)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `flox activate -- go test ./internal/generate/v1/ -run TestBuildGatewayConfig_PluginEgressRules -v`
Expected: FAIL

- [ ] **Step 3: Rewrite BuildGatewayConfig**

Replace the plugin services section (lines 102-128) with:

```go
// Merge plugin-contributed egress rules
if contribs != nil {
	for _, rule := range contribs.Gateway.Egress {
		// Normalize hosts (accept URLs, extract hostname)
		normalized := normalizeEgressHosts(rule)
		out.EgressRules = insertPluginEgressRule(out.EgressRules, normalized)
	}
}
```

Remove `GatewayConfigOutput.MiddlewareDomains` field (no longer needed — middlewares are on the rules themselves).

Remove `GatewayConfigOutput.Services` field — replace with just `EgressRules` and `AuthHeaders`.

Add helper:

```go
// insertPluginEgressRule inserts a plugin rule before the catch-all, or appends if no catch-all.
func insertPluginEgressRule(rules []config.EgressRule, rule config.EgressRule) []config.EgressRule {
	if len(rules) > 0 && len(rules[len(rules)-1].Hosts) == 1 && rules[len(rules)-1].Hosts[0] == "*" {
		rules = append(rules[:len(rules)-1], rule, rules[len(rules)-1])
	} else {
		rules = append(rules, rule)
	}
	return rules
}
```

- [ ] **Step 4: Update WriteGatewayRuntimeConfig**

The MITM domain collection simplifies — just iterate `EgressRules` and check `NeedsMITM()`:

```go
mitmSet := make(map[string]bool)
for _, rule := range gwCfg.EgressRules {
	if !rule.NeedsMITM() {
		continue
	}
	for _, host := range rule.Hosts {
		if host != "*" && !strings.Contains(host, "/") {
			mitmSet[host] = true
		}
	}
}
```

Remove the separate plugin services loop and middleware domains loop entirely.

- [ ] **Step 5: Run tests**

Run: `flox activate -- go test ./internal/generate/v1/ -v`
Expected: PASS (update old tests to match new struct)

- [ ] **Step 6: Commit**

```bash
git add internal/generate/v1/gateway_config.go internal/generate/v1/gateway_config_test.go
git commit -m "refactor: BuildGatewayConfig uses plugin egress rules directly"
```

---

---

### Task 4: Update writePluginsYAML to derive domains from egress rules

**Files:**
- Modify: `internal/generate/v1/gateway_build.go:246-314`

- [ ] **Step 1: Rewrite writePluginsYAML middleware collection**

Replace lines 274-283 in `gateway_build.go`:

```go
// Derive middleware entries from plugin's egress rules
for _, rule := range rp.rendered.Gateway.Egress {
	for _, mw := range rule.Middlewares {
		entry.Gateway.Middlewares = append(entry.Gateway.Middlewares, pluginsYAMLMiddleware{
			Script:  mw.Script,
			Domains: normalizeHosts(rule.Hosts),
		})
	}
}
```

Add helper at file scope:

```go
// normalizeHosts extracts bare hostnames from hosts that may contain URLs.
func normalizeHosts(hosts []string) []string {
	var result []string
	for _, h := range hosts {
		if d := extractDomain(h); d != "" && d != "*" {
			result = append(result, d)
		}
	}
	return result
}
```

- [ ] **Step 2: Update hasGatewayTSContribs**

```go
func hasGatewayTSContribs(rp *resolvedPlugin) bool {
	for _, rule := range rp.rendered.Gateway.Egress {
		if len(rule.Middlewares) > 0 {
			return true
		}
	}
	if len(rp.rendered.Gateway.Routes) > 0 {
		return true
	}
	return false
}
```

- [ ] **Step 3: Remove the `_ = rp.rendered.Gateway.Services` dead code line**

- [ ] **Step 4: Run tests**

Run: `flox activate -- go test ./internal/generate/v1/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/gateway_build.go
git commit -m "refactor: writePluginsYAML derives middleware domains from egress rules"
```

---

### Task 5: Host normalization (accept URLs in hosts field)

**Files:**
- Modify: `internal/generate/v1/gateway_config.go`
- Test: `internal/generate/v1/gateway_config_test.go`

- [ ] **Step 1: Write test for URL-in-hosts normalization**

```go
func TestNormalizeEgressHosts(t *testing.T) {
	rule := config.EgressRule{
		Hosts: []string{"https://mcp.notion.com/mcp", "api.github.com"},
		Middlewares: []config.MiddlewareEntry{{Script: "./src/oauth.ts"}},
	}

	normalized := normalizeEgressHosts(rule)

	assert.Equal(t, []string{"mcp.notion.com", "api.github.com"}, normalized.Hosts)
	// Middlewares preserved
	require.Len(t, normalized.Middlewares, 1)
}
```

- [ ] **Step 2: Implement normalizeEgressHosts**

```go
// normalizeEgressHosts converts any URLs in rule.Hosts to bare hostnames.
func normalizeEgressHosts(rule config.EgressRule) config.EgressRule {
	normalized := rule
	normalized.Hosts = make([]string, 0, len(rule.Hosts))
	for _, h := range rule.Hosts {
		if d := extractDomain(h); d != "" {
			normalized.Hosts = append(normalized.Hosts, d)
		} else {
			normalized.Hosts = append(normalized.Hosts, h)
		}
	}
	return normalized
}
```

- [ ] **Step 3: Run tests**

Run: `flox activate -- go test ./internal/generate/v1/ -run TestNormalizeEgressHosts -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/generate/v1/gateway_config.go internal/generate/v1/gateway_config_test.go
git commit -m "feat: normalize URLs to hostnames in egress hosts field"
```

---

### Task 6: Update built-in plugins

**Files:**
- Modify: `core/plugins/github-pat/plugin.yaml`
- Modify: `core/plugins/mcp-oauth/plugin.yaml`

- [ ] **Step 1: Update github-pat/plugin.yaml**

```yaml
name: github-pat
options:
  token:
    type: string
    required: true
    description: "GitHub PAT env var reference (e.g. ${GITHUB_PAT})"

contributes:
  runtime:
    extra_builds:
      # Agent container needs dummy tokens so git/gh CLI attempt auth
      # (the gateway intercepts and injects real credentials)
      - "ENV GH_TOKEN=dummy GITHUB_TOKEN=dummy"
  gateway:
    egress:
      - hosts: ["api.github.com", "github.com"]
        middlewares:
          - script: "./src/github-auth.ts"
```

- [ ] **Step 2: Update mcp-oauth/plugin.yaml**

```yaml
name: mcp-oauth
options:
  providers:
    type: object
    required: true
    description: "Map of provider name to MCP config (each needs at least mcp_url)"

  callback_url:
    type: string
    required: false
    description: "Public callback URL (derived from request if not set)"

contributes:
  gateway:
    egress:
{{- range $name, $cfg := .plugin.options.providers }}
      - hosts: ["{{ index $cfg "mcp_url" }}"]
        middlewares:
          - script: "./src/oauth.ts"
{{- end }}
    namespaced_volumes:
      - "oauth-tokens:/data/plugins/mcp-oauth"
    routes:
      - path: "/callback"
        handler: "./src/callback.ts"
      - path: "/login"
        handler: "./src/login.ts"
      - path: "/status"
        handler: "./src/status.ts"
      - path: "/disconnect"
        handler: "./src/disconnect.ts"
```

The `hosts` values will be full URLs from the template (e.g. `https://mcp.notion.com/mcp`). The `normalizeEgressHosts` function from Task 5 handles extracting the hostname.

- [ ] **Step 3: Run full test suite**

Run: `flox activate -- go test ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add core/plugins/github-pat/plugin.yaml core/plugins/mcp-oauth/plugin.yaml
git commit -m "refactor: migrate built-in plugins to gateway.egress format"
```

---

### Task 7: Add migrate command

**Files:**
- Create: `cmd/agent-sandbox-core/migrate.go`
- Create: `internal/migrate/plugin_gateway.go`
- Create: `internal/migrate/plugin_gateway_test.go`

- [ ] **Step 1: Write the migration detection and transformation logic**

Create `internal/migrate/plugin_gateway.go`:

```go
package migrate

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// PluginMigration describes a proposed migration for a plugin file.
type PluginMigration struct {
	Path    string
	Before  string
	After   string
}

// DetectLegacyGateway checks if a plugin.yaml uses the old gateway format.
// Returns true if contributes.gateway.services or top-level contributes.gateway.middlewares exist.
func DetectLegacyGateway(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false, err
	}
	contribs, ok := raw["contributes"].(map[string]any)
	if !ok {
		return false, nil
	}
	gw, ok := contribs["gateway"].(map[string]any)
	if !ok {
		return false, nil
	}
	_, hasServices := gw["services"]
	_, hasMiddlewares := gw["middlewares"]
	_, hasEgress := gw["egress"]
	// Legacy if it has services or middlewares but no egress
	return (hasServices || hasMiddlewares) && !hasEgress, nil
}
```

- [ ] **Step 2: Write the transformation function**

```go
// TransformGateway converts old services+middlewares format to egress format.
// Returns the transformed YAML content.
func TransformGateway(path string) (*PluginMigration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Parse into node tree for structure-aware rewrite
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// ... (node manipulation to rewrite gateway section)
	// Extract services URLs → hosts
	// Match middlewares to services by domain overlap
	// Produce egress rules with attached middlewares

	transformed, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}

	return &PluginMigration{
		Path:   path,
		Before: string(data),
		After:  string(transformed),
	}, nil
}
```

- [ ] **Step 3: Write tests for detection and transformation**

```go
func TestDetectLegacyGateway_Old(t *testing.T) {
	path := writeTempPlugin(t, `
name: test
contributes:
  gateway:
    services:
      - url: "https://api.example.com"
    middlewares:
      - script: "./src/auth.ts"
        domains: ["api.example.com"]
`)
	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.True(t, legacy)
}

func TestDetectLegacyGateway_New(t *testing.T) {
	path := writeTempPlugin(t, `
name: test
contributes:
  gateway:
    egress:
      - hosts: ["api.example.com"]
        middlewares:
          - script: "./src/auth.ts"
`)
	legacy, err := DetectLegacyGateway(path)
	require.NoError(t, err)
	assert.False(t, legacy)
}
```

- [ ] **Step 4: Run tests**

Run: `flox activate -- go test ./internal/migrate/ -v`
Expected: PASS

- [ ] **Step 5: Write the cobra command**

Create `cmd/agent-sandbox-core/migrate.go`:

```go
func migrateCmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Migrate plugins from legacy gateway format to egress format",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir := resolveDir(*dir)
			// Scan for legacy plugins
			// Display diffs
			// Prompt for confirmation
			// Rewrite files
			return nil
		},
	}
}
```

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-sandbox-core/migrate.go internal/migrate/
git commit -m "feat: add migrate command for legacy plugin gateway format"
```

---

### Task 8: Generate rejects old format with helpful error

**Files:**
- Modify: `internal/generate/v1/generator.go`
- Test: `internal/generate/v1/generator_test.go`

- [ ] **Step 1: Add validation in generateAgent after plugin resolution**

After plugins are resolved and rendered, check for legacy format:

```go
// Reject legacy gateway format
for _, rp := range resolved {
	if hasLegacyGatewayContrib(rp.rendered) {
		return nil, fmt.Errorf(
			"plugin %q uses deprecated contributes.gateway.services format. "+
				"Run `agent-sandbox migrate` to convert to the egress format",
			rp.def.Name,
		)
	}
}
```

Add helper:

```go
func hasLegacyGatewayContrib(c *plugin.Contributions) bool {
	// Check if the raw YAML had services or top-level middlewares
	// This check happens after rendering, so we need a different approach:
	// If GatewayContrib has the old fields, it's legacy.
	// Since we removed those fields, this is now a parse-time check.
	return false
}
```

Actually — since we removed the old struct fields in Task 2, any plugin using the old format will fail to unmarshal into `GatewayContrib` (fields won't map). We need to detect this at YAML parse time before struct unmarshaling.

- [ ] **Step 2: Add pre-validation in plugin resolver**

In `internal/plugin/render.go`, after template rendering but before unmarshal:

```go
// Check for legacy gateway format before unmarshaling
if err := rejectLegacyGateway(rendered); err != nil {
	return nil, fmt.Errorf("plugin %s: %w", p.Name, err)
}
```

```go
func rejectLegacyGateway(yamlContent string) error {
	var raw struct {
		Gateway struct {
			Services    any `yaml:"services"`
			Middlewares any `yaml:"middlewares"`
		} `yaml:"gateway"`
	}
	if err := yaml.Unmarshal([]byte(yamlContent), &raw); err != nil {
		return nil
	}
	if raw.Gateway.Services != nil || raw.Gateway.Middlewares != nil {
		return fmt.Errorf(
			"uses deprecated contributes.gateway.services/middlewares format. " +
				"Run `agent-sandbox migrate` to convert to the egress format",
		)
	}
	return nil
}
```

- [ ] **Step 3: Write test**

```go
func TestRejectLegacyGateway(t *testing.T) {
	legacy := `
gateway:
  services:
    - url: "https://example.com"
`
	err := rejectLegacyGateway(legacy)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deprecated")
}
```

- [ ] **Step 4: Run tests**

Run: `flox activate -- go test ./internal/plugin/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/render.go internal/plugin/render_test.go
git commit -m "feat: reject legacy gateway format with migration hint"
```

---

### Task 9: Update gateway runtime plugin loading

**Files:**
- Modify: `core/gateway/internal/pluginloader/config.go`

- [ ] **Step 1: Verify no runtime changes needed**

The gateway runtime already loads `plugins.yaml` which has `middlewares[].domains`. Since `writePluginsYAML` (Task 4) still produces the same `plugins.yaml` format (script + domains), the gateway binary doesn't need changes. The domain derivation happens at generate-time.

Verify by inspecting the generated `plugins.yaml` format matches what `pluginloader` expects:
- `pluginsYAMLMiddleware` still has `Script` and `Domains` fields ✓
- Gateway's `MiddlewareEntry` still has `Script` and `Domains` fields ✓

No code changes needed. The refactor is transparent to the gateway runtime.

- [ ] **Step 2: Run gateway tests to confirm**

Run: `flox activate -- go test ./core/gateway/... -v`
Expected: PASS

- [ ] **Step 3: Commit (if any fixups needed)**

---

### Task 10: Update collectGatewayEnvVars

**Files:**
- Modify: `internal/generate/v1/compose.go:479-521`

- [ ] **Step 1: Remove the plugin services loop**

Replace lines 500-508:

```go
// From plugin contributions (egress rule headers)
if contribs != nil {
	for _, rule := range contribs.Gateway.Egress {
		for _, value := range rule.Headers {
			if ev := envvar.Extract(value); ev != "" {
				seen[ev] = true
			}
		}
	}
}
```

- [ ] **Step 2: Run tests**

Run: `flox activate -- go test ./internal/generate/v1/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/generate/v1/compose.go
git commit -m "refactor: collectGatewayEnvVars uses plugin egress rules"
```

---

### Task 11: Update documentation

**Files:**
- Modify: `docs/plugins.md`
- Modify: `docs/reference/gateway-egress.md`
- Modify: `docs/internals/build-pipeline.md`
- Modify: `docs/internals/plugin-system.md`

- [ ] **Step 1: Update docs/plugins.md — plugin schema section**

Replace the `contributes.gateway` example:

```yaml
contributes:
  gateway:
    egress:                            # egress rules (same format as user config)
      - hosts: ["api.example.com"]
        middlewares:                    # intercept proxied requests
          - script: "./src/auth.ts"
    namespaced_volumes:
      - "my-data:{{ .plugin.options.data_dir }}"
    routes:
      - path: "/callback"
        handler: "./src/callback.ts"
```

- [ ] **Step 2: Update docs/reference/gateway-egress.md — add middlewares field**

Add `middlewares` to the field reference table and add an example showing middleware on a rule.

- [ ] **Step 3: Update docs/internals/plugin-system.md**

Remove references to `contributes.gateway.services` and `contributes.gateway.middlewares` as separate concepts.

- [ ] **Step 4: Update docs/internals/build-pipeline.md**

Step 8 (Generate gateway config) — update description to reflect that MITM domains come from egress rules with headers, deny_paths, or middlewares.

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "docs: update for unified plugin gateway egress format"
```

---

### Task 12: Integration test — end-to-end generate

**Files:**
- Create: `internal/generate/v1/integration_test.go` (or add to existing)

- [ ] **Step 1: Write integration test**

```go
func TestGenerate_PluginMiddlewareEgress_EndToEnd(t *testing.T) {
	// Set up a temp project with fleet.yaml + agent.yaml + plugin with new format
	// Run generator
	// Verify:
	// 1. config.yaml has the middleware domain in mitm_domains
	// 2. plugins.yaml has the middleware with correct domains
	// 3. No errors during generation
}
```

- [ ] **Step 2: Write integration test for rejection of old format**

```go
func TestGenerate_RejectsLegacyPlugin(t *testing.T) {
	// Set up a temp project with a plugin using old services+middlewares format
	// Run generator
	// Verify: error contains "deprecated" and "agent-sandbox migrate"
}
```

- [ ] **Step 3: Run all tests**

Run: `flox activate -- go test ./...`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/generate/v1/integration_test.go
git commit -m "test: add integration tests for unified gateway format"
```

---

### Task 13: Update local plugins (my-agent-team-v3)

**Files:**
- Modify: `/Users/corey/Projects/my-agent-team-v3/plugins/telegram-v2/plugin.yaml`

- [ ] **Step 1: Migrate telegram-v2 plugin**

```yaml
name: telegram-v2
requires:
  - "@builtin/agent-manager-acp"

assets:
  - path: telegram-adapter/
    exclude: [node_modules, dist]

options:
  bot_token:
    type: string
    required: true
    description: "Telegram bot token (baked into gateway middleware at generate-time)"
  access_control:
    type: object
    required: false
    description: "Access control settings (allowed_users, require_mention, groups)"

contributes:
  runtime:
    extra_builds:
      - "COPY {{ asset \"telegram-adapter\" }}/ /opt/telegram-adapter-src/"
      - "RUN cd /opt/telegram-adapter-src && npm install && npm run build && npm prune --omit=dev && mkdir -p /opt/telegram-adapter && mv dist /opt/telegram-adapter/dist && mv node_modules /opt/telegram-adapter/node_modules && mv package.json /opt/telegram-adapter/ && rm -rf /opt/telegram-adapter-src"
      - "ENV TELEGRAM_BOT_TOKEN=dummy"
      - "RUN echo '{{ toJSON .plugin.options.access_control }}' > /opt/telegram-adapter/access-control.json"
  gateway:
    egress:
      - hosts: ["api.telegram.org"]
        middlewares:
          - script: "./src/telegram-token-rewrite.ts"
```

- [ ] **Step 2: Regenerate and verify**

```bash
flox activate -- agent-sandbox --dev -C ~/Projects/my-agent-team-v3 generate
```

Verify `api.telegram.org` is in `.build/dorey-001/gateway/config.yaml` under `mitm_domains`.

- [ ] **Step 3: Commit in my-agent-team-v3**

```bash
cd ~/Projects/my-agent-team-v3
git add plugins/telegram-v2/plugin.yaml
git commit -m "refactor: migrate telegram-v2 to gateway.egress format"
```
