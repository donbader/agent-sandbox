# V1 Architecture Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite agent-sandbox as a generic container orchestrator with CLI/core separation, declarative YAML plugins, and user-configurable sidecars.

**Architecture:** CLI reads `agent.yaml`, resolves plugins (bundled/local/remote), renders Go templates from plugin contributions, and generates `.build/` containing Dockerfile, docker-compose.yaml, gateway source with custom middleware, and entrypoint scripts. Core is independently versioned and fetched by CLI at generate-time.

**Tech Stack:** Go 1.24+, Cobra CLI, yaml.v3, Go text/template, Docker Compose

---

## Phase 1: Branch + Repo Restructure

### Task 1.1: Create v1 branch and core/ directory structure

**Files:**
- Create: `core/gateway/` (move from `gateway/`)
- Create: `core/presets/codex/runtime.yaml`
- Create: `core/presets/claude-code/runtime.yaml`
- Create: `core/presets/pi/runtime.yaml`
- Create: `core/sdk/gateway/middleware.go`
- Create: `core/templates/`
- Create: `core/plugins/` (bundled plugins)

- [ ] **Step 1: Create v1 branch**

```bash
git checkout -b v1
```

- [ ] **Step 2: Create core directory structure**

```bash
mkdir -p core/gateway core/presets core/sdk/gateway core/templates core/plugins
```

- [ ] **Step 3: Move gateway source to core/gateway/**

```bash
git mv gateway/cmd core/gateway/cmd
git mv gateway/internal core/gateway/internal
```

- [ ] **Step 4: Move runtime presets to core/presets/**

Copy existing runtime YAML from `internal/plugins/codex/runtime.yaml`, `internal/plugins/claude-code/runtime.yaml`, `internal/plugins/pi/runtime.yaml` into `core/presets/<name>/runtime.yaml`.

```bash
cp internal/plugins/codex/runtime.yaml core/presets/codex/runtime.yaml
cp internal/plugins/claude-code/runtime.yaml core/presets/claude-code/runtime.yaml
cp internal/plugins/pi/runtime.yaml core/presets/pi/runtime.yaml
```

- [ ] **Step 5: Create middleware SDK interface**

```go
// core/sdk/gateway/middleware.go
package gateway

import "net/http"

// MiddlewareContext provides request access and environment resolution for custom middleware.
type MiddlewareContext struct {
    Request *http.Request
    Env     func(string) string
}

// MiddlewareFunc is the signature for custom gateway middleware.
type MiddlewareFunc func(ctx *MiddlewareContext) error

var registry = map[string]MiddlewareFunc{}

// RegisterMiddleware registers a named middleware function.
func RegisterMiddleware(name string, fn MiddlewareFunc) {
    registry[name] = fn
}

// Get returns a registered middleware by name.
func Get(name string) (MiddlewareFunc, bool) {
    fn, ok := registry[name]
    return fn, ok
}

// All returns all registered middleware.
func All() map[string]MiddlewareFunc {
    return registry
}
```

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "refactor: create core/ directory structure for v1"
```

## Phase 2: Config Parser

### Task 2.1: Define v1 config types

**Files:**
- Create: `internal/config/v1.go`
- Create: `internal/config/v1_test.go`

- [ ] **Step 1: Write failing test for config parsing**

```go
// internal/config/v1_test.go
package config

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestLoadV1_BasicConfig(t *testing.T) {
    dir := t.TempDir()
    yaml := `
name: test-agent
log_level: debug
core_version: v1.0.0
runtime:
  image: "@builtin/codex"
  extra_builds:
    - "RUN apt-get install -y jq"
  entrypoint: ["codex-acp", "--listen", ":8080"]
  volumes:
    - "data:/opt/data"
gateway:
  services:
    - url: https://api.example.com
      headers:
        Authorization: Bearer ${TOKEN}
installations:
  - plugin: github-pat
    options:
      token: "${GITHUB_PAT}"
`
    require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))

    cfg, err := LoadV1(dir)
    require.NoError(t, err)

    assert.Equal(t, "test-agent", cfg.Name)
    assert.Equal(t, "debug", cfg.LogLevel)
    assert.Equal(t, "v1.0.0", cfg.CoreVersion)
    assert.Equal(t, "@builtin/codex", cfg.Runtime.Image)
    assert.Equal(t, []string{"codex-acp", "--listen", ":8080"}, cfg.Runtime.Entrypoint)
    assert.Len(t, cfg.Gateway.Services, 1)
    assert.Equal(t, "https://api.example.com", cfg.Gateway.Services[0].URL)
    assert.Len(t, cfg.Installations, 1)
    assert.Equal(t, "github-pat", cfg.Installations[0].Plugin)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadV1 -v`
Expected: FAIL — `LoadV1` not defined

- [ ] **Step 3: Implement v1 config types and loader**

```go
// internal/config/v1.go
package config

import (
    "fmt"
    "os"
    "path/filepath"

    "gopkg.in/yaml.v3"
)

// V1Config represents a v1 agent.yaml file.
type V1Config struct {
    Name          string          `yaml:"name"`
    LogLevel      string          `yaml:"log_level"`
    CoreVersion   string          `yaml:"core_version"`
    Runtime       RuntimeConfig   `yaml:"runtime"`
    Gateway       GatewayConfig   `yaml:"gateway"`
    Installations []Installation  `yaml:"installations"`
}

type RuntimeConfig struct {
    Image       string   `yaml:"image"`
    ExtraBuilds []string `yaml:"extra_builds"`
    Entrypoint  []string `yaml:"entrypoint"`
    Volumes     []string `yaml:"volumes"`
}

type GatewayConfig struct {
    Services []GatewayServiceEntry `yaml:"services"`
}

type GatewayServiceEntry struct {
    URL        string            `yaml:"url"`
    Network    string            `yaml:"network"`
    Headers    map[string]string `yaml:"headers"`
    Middlewares []MiddlewareEntry `yaml:"middlewares"`
}

type MiddlewareEntry struct {
    Custom string `yaml:"custom"`
}

type Installation struct {
    Plugin  string         `yaml:"plugin"`
    Source  string         `yaml:"source"`
    Options map[string]any `yaml:"options"`
}

// LoadV1 loads and parses a v1 agent.yaml from the given directory.
func LoadV1(dir string) (*V1Config, error) {
    path := filepath.Join(dir, "agent.yaml")
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read agent.yaml: %w", err)
    }

    var cfg V1Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("parse agent.yaml: %w", err)
    }

    if cfg.Name == "" {
        return nil, fmt.Errorf("agent.yaml: name is required")
    }
    if cfg.Runtime.Image == "" {
        return nil, fmt.Errorf("agent.yaml: runtime.image is required")
    }

    return &cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadV1 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/v1.go internal/config/v1_test.go
git commit -m "feat(config): add v1 config types and loader"
```

### Task 2.2: Config validation

**Files:**
- Modify: `internal/config/v1.go`
- Modify: `internal/config/v1_test.go`

- [ ] **Step 1: Write failing tests for validation**

```go
func TestLoadV1_MissingName(t *testing.T) {
    dir := t.TempDir()
    yaml := `runtime:
  image: "@builtin/codex"
`
    require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
    _, err := LoadV1(dir)
    assert.ErrorContains(t, err, "name is required")
}

func TestLoadV1_MissingRuntimeImage(t *testing.T) {
    dir := t.TempDir()
    yaml := `name: test
`
    require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
    _, err := LoadV1(dir)
    assert.ErrorContains(t, err, "runtime.image is required")
}

func TestLoadV1_DockerURLRequiresNetwork(t *testing.T) {
    dir := t.TempDir()
    yaml := `
name: test
runtime:
  image: "@builtin/codex"
gateway:
  services:
    - url: "docker://sidecar:8080"
`
    require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
    _, err := LoadV1(dir)
    assert.ErrorContains(t, err, "network is required for docker:// URLs")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestLoadV1 -v`
Expected: `TestLoadV1_DockerURLRequiresNetwork` FAILS (validation not implemented yet)

- [ ] **Step 3: Add validation for docker:// URLs**

Add to `LoadV1()` after unmarshal:

```go
for i, svc := range cfg.Gateway.Services {
    if strings.HasPrefix(svc.URL, "docker://") && svc.Network == "" {
        return nil, fmt.Errorf("agent.yaml: gateway.services[%d]: network is required for docker:// URLs", i)
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run TestLoadV1 -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/v1.go internal/config/v1_test.go
git commit -m "feat(config): add v1 config validation"
```

## Phase 3: Declarative Plugin System

### Task 3.1: Plugin YAML schema types

**Files:**
- Create: `internal/plugin/types.go`
- Create: `internal/plugin/types_test.go`

- [ ] **Step 1: Write failing test for plugin YAML parsing**

```go
// internal/plugin/types_test.go
package plugin

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestParsePluginYAML(t *testing.T) {
    raw := `
name: github-pat
options:
  token:
    type: string
    required: true
    description: "GitHub personal access token"
contributes:
  gateway:
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`
    p, err := ParsePluginYAML([]byte(raw))
    require.NoError(t, err)
    assert.Equal(t, "github-pat", p.Name)
    assert.Contains(t, p.Options, "token")
    assert.Equal(t, "string", p.Options["token"].Type)
    assert.True(t, p.Options["token"].Required)
    assert.Len(t, p.Contributes.Gateway.Services, 1)
    assert.Equal(t, "https://github.com", p.Contributes.Gateway.Services[0].URL)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/plugin/ -run TestParsePluginYAML -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement plugin types and parser**

```go
// internal/plugin/types.go
package plugin

import (
    "fmt"

    "gopkg.in/yaml.v3"
)

// PluginDef represents a parsed plugin.yaml file.
type PluginDef struct {
    Name        string                  `yaml:"name"`
    Options     map[string]OptionSchema `yaml:"options"`
    Contributes Contributions           `yaml:"contributes"`
}

type OptionSchema struct {
    Type        string                  `yaml:"type"`
    Required    bool                    `yaml:"required"`
    Default     any                     `yaml:"default"`
    Description string                  `yaml:"description"`
    Properties  map[string]OptionSchema `yaml:"properties"`
    Items       *OptionSchema           `yaml:"items"`
}

type Contributions struct {
    Runtime RuntimeContrib `yaml:"runtime"`
    Gateway GatewayContrib `yaml:"gateway"`
    Sidecar SidecarContrib `yaml:"sidecar"`
}

type RuntimeContrib struct {
    ExtraBuilds []string `yaml:"extra_builds"`
}

type GatewayContrib struct {
    Services []GatewayService `yaml:"services"`
    Volumes  []string         `yaml:"volumes"`
}

type GatewayService struct {
    URL         string            `yaml:"url"`
    Network     string            `yaml:"network"`
    Headers     map[string]string `yaml:"headers"`
    Middlewares []MiddlewareRef   `yaml:"middlewares"`
}

type MiddlewareRef struct {
    Custom string `yaml:"custom"`
}

type SidecarContrib struct {
    Services map[string]ComposeService `yaml:"services"`
}

// ComposeService follows docker-compose service spec (subset).
type ComposeService struct {
    Build       string            `yaml:"build"`
    Image       string            `yaml:"image"`
    Environment map[string]string `yaml:"environment"`
    Ports       []string          `yaml:"ports"`
    Volumes     []string          `yaml:"volumes"`
    DependsOn   any               `yaml:"depends_on"`
    Healthcheck *Healthcheck      `yaml:"healthcheck"`
    Networks    []string          `yaml:"networks"`
}

type Healthcheck struct {
    Test     []string `yaml:"test"`
    Interval string   `yaml:"interval"`
    Timeout  string   `yaml:"timeout"`
    Retries  int      `yaml:"retries"`
}

// ParsePluginYAML parses raw YAML bytes into a PluginDef.
func ParsePluginYAML(data []byte) (*PluginDef, error) {
    var p PluginDef
    if err := yaml.Unmarshal(data, &p); err != nil {
        return nil, fmt.Errorf("parse plugin.yaml: %w", err)
    }
    if p.Name == "" {
        return nil, fmt.Errorf("plugin.yaml: name is required")
    }
    return &p, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/plugin/ -run TestParsePluginYAML -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/types.go internal/plugin/types_test.go
git commit -m "feat(plugin): add plugin YAML types and parser"
```

### Task 3.2: Template rendering for plugin contributions

**Files:**
- Create: `internal/plugin/render.go`
- Create: `internal/plugin/render_test.go`

- [ ] **Step 1: Write failing test for template rendering**

```go
// internal/plugin/render_test.go
package plugin

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestRenderContributions(t *testing.T) {
    raw := `
name: github-pat
options:
  token:
    type: string
    required: true
contributes:
  runtime:
    extra_builds:
      - "RUN echo {{ .options.token }}"
  gateway:
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`
    p, err := ParsePluginYAML([]byte(raw))
    require.NoError(t, err)

    opts := map[string]any{"token": "ghp_abc123"}
    rendered, err := RenderContributions(p, opts)
    require.NoError(t, err)

    assert.Equal(t, "RUN echo ghp_abc123", rendered.Runtime.ExtraBuilds[0])
    assert.Equal(t, "Bearer ghp_abc123", rendered.Gateway.Services[0].Headers["Authorization"])
}

func TestRenderContributions_MissingRequired(t *testing.T) {
    raw := `
name: test
options:
  token:
    type: string
    required: true
contributes:
  runtime:
    extra_builds: []
`
    p, err := ParsePluginYAML([]byte(raw))
    require.NoError(t, err)

    opts := map[string]any{}
    _, err = RenderContributions(p, opts)
    assert.ErrorContains(t, err, "required option \"token\" not provided")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/plugin/ -run TestRender -v`
Expected: FAIL — `RenderContributions` not defined

- [ ] **Step 3: Implement template rendering**

```go
// internal/plugin/render.go
package plugin

import (
    "bytes"
    "fmt"
    "text/template"

    "gopkg.in/yaml.v3"
)

// RenderContributions resolves Go templates in a plugin's contributions using provided options.
func RenderContributions(p *PluginDef, opts map[string]any) (*Contributions, error) {
    if err := validateOptions(p.Options, opts); err != nil {
        return nil, err
    }

    // Apply defaults
    resolvedOpts := applyDefaults(p.Options, opts)

    // Re-marshal the contributes block to YAML, then template-render it
    contribYAML, err := yaml.Marshal(p.Contributes)
    if err != nil {
        return nil, fmt.Errorf("marshal contributes: %w", err)
    }

    tmpl, err := template.New("contrib").Parse(string(contribYAML))
    if err != nil {
        return nil, fmt.Errorf("parse contributes template: %w", err)
    }

    data := map[string]any{"options": resolvedOpts}
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return nil, fmt.Errorf("render contributes template: %w", err)
    }

    var rendered Contributions
    if err := yaml.Unmarshal(buf.Bytes(), &rendered); err != nil {
        return nil, fmt.Errorf("parse rendered contributes: %w", err)
    }

    return &rendered, nil
}

func validateOptions(schema map[string]OptionSchema, opts map[string]any) error {
    for name, s := range schema {
        if s.Required {
            if _, ok := opts[name]; !ok {
                return fmt.Errorf("required option %q not provided", name)
            }
        }
    }
    return nil
}

func applyDefaults(schema map[string]OptionSchema, opts map[string]any) map[string]any {
    resolved := make(map[string]any, len(opts))
    for k, v := range opts {
        resolved[k] = v
    }
    for name, s := range schema {
        if _, ok := resolved[name]; !ok && s.Default != nil {
            resolved[name] = s.Default
        }
    }
    return resolved
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/plugin/ -run TestRender -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/render.go internal/plugin/render_test.go
git commit -m "feat(plugin): add template rendering for contributions"
```

### Task 3.3: Plugin resolution (bundled, local, remote)

**Files:**
- Create: `internal/plugin/resolve.go`
- Create: `internal/plugin/resolve_test.go`

- [ ] **Step 1: Write failing test for local plugin resolution**

```go
// internal/plugin/resolve_test.go
package plugin

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestResolveLocal(t *testing.T) {
    dir := t.TempDir()
    pluginDir := filepath.Join(dir, "plugins", "my-plugin")
    require.NoError(t, os.MkdirAll(pluginDir, 0755))

    pluginYAML := `
name: my-plugin
options:
  greeting:
    type: string
    default: "hello"
contributes:
  runtime:
    extra_builds:
      - "RUN echo {{ .options.greeting }}"
`
    require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(pluginYAML), 0644))

    resolver := NewResolver(dir, nil)
    p, err := resolver.Resolve("my-plugin", "")
    require.NoError(t, err)
    assert.Equal(t, "my-plugin", p.Name)
}

func TestResolveBundled(t *testing.T) {
    dir := t.TempDir()
    // No local plugins dir — should fall back to bundled
    resolver := NewResolver(dir, testBundledFS())
    p, err := resolver.Resolve("github-pat", "")
    require.NoError(t, err)
    assert.Equal(t, "github-pat", p.Name)
}

func TestResolve_NotFound(t *testing.T) {
    dir := t.TempDir()
    resolver := NewResolver(dir, nil)
    _, err := resolver.Resolve("nonexistent", "")
    assert.ErrorContains(t, err, "plugin \"nonexistent\" not found")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/plugin/ -run TestResolve -v`
Expected: FAIL — `NewResolver` not defined

- [ ] **Step 3: Implement resolver**

```go
// internal/plugin/resolve.go
package plugin

import (
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
)

// Resolver locates and loads plugin definitions.
type Resolver struct {
    projectDir string
    bundledFS  fs.FS
}

// NewResolver creates a resolver that checks local plugins/ dir first, then bundled FS.
func NewResolver(projectDir string, bundledFS fs.FS) *Resolver {
    return &Resolver{projectDir: projectDir, bundledFS: bundledFS}
}

// Resolve finds and parses a plugin by name. If source is non-empty, it's a remote plugin (future).
func (r *Resolver) Resolve(name string, source string) (*PluginDef, error) {
    // 1. Check local plugins/<name>/plugin.yaml
    localPath := filepath.Join(r.projectDir, "plugins", name, "plugin.yaml")
    if data, err := os.ReadFile(localPath); err == nil {
        return ParsePluginYAML(data)
    }

    // 2. Check bundled FS
    if r.bundledFS != nil {
        bundledPath := filepath.Join(name, "plugin.yaml")
        if data, err := fs.ReadFile(r.bundledFS, bundledPath); err == nil {
            return ParsePluginYAML(data)
        }
    }

    // 3. Remote (future — source field)
    if source != "" {
        return nil, fmt.Errorf("remote plugin resolution not yet implemented: %s", source)
    }

    return nil, fmt.Errorf("plugin %q not found (checked: local plugins/%s/, bundled)", name, name)
}
```

- [ ] **Step 4: Create test helper for bundled FS**

Add to `resolve_test.go`:

```go
import "testing/fstest"

func testBundledFS() fs.FS {
    return fstest.MapFS{
        "github-pat/plugin.yaml": &fstest.MapFile{
            Data: []byte(`
name: github-pat
options:
  token:
    type: string
    required: true
contributes:
  gateway:
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`),
        },
    }
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/plugin/ -run TestResolve -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/plugin/resolve.go internal/plugin/resolve_test.go
git commit -m "feat(plugin): add plugin resolution (local + bundled)"
```

### Task 3.4: Contribution merging

**Files:**
- Create: `internal/plugin/merge.go`
- Create: `internal/plugin/merge_test.go`

- [ ] **Step 1: Write failing test for merging contributions**

```go
// internal/plugin/merge_test.go
package plugin

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestMergeContributions(t *testing.T) {
    a := &Contributions{
        Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN apt-get install -y git"}},
        Gateway: GatewayContrib{Services: []GatewayService{
            {URL: "https://github.com", Headers: map[string]string{"Authorization": "Bearer abc"}},
        }},
    }
    b := &Contributions{
        Runtime: RuntimeContrib{ExtraBuilds: []string{"RUN npm install -g codex-acp"}},
        Gateway: GatewayContrib{Services: []GatewayService{
            {URL: "https://api.telegram.org"},
        }},
        Sidecar: SidecarContrib{Services: map[string]ComposeService{
            "telegram": {Build: "./sidecar"},
        }},
    }

    merged := MergeContributions(a, b)

    assert.Len(t, merged.Runtime.ExtraBuilds, 2)
    assert.Len(t, merged.Gateway.Services, 2)
    assert.Len(t, merged.Sidecar.Services, 1)
    assert.Contains(t, merged.Sidecar.Services, "telegram")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/plugin/ -run TestMerge -v`
Expected: FAIL — `MergeContributions` not defined

- [ ] **Step 3: Implement merge logic**

```go
// internal/plugin/merge.go
package plugin

// MergeContributions combines multiple contribution sets in order.
func MergeContributions(contribs ...*Contributions) *Contributions {
    merged := &Contributions{
        Sidecar: SidecarContrib{Services: map[string]ComposeService{}},
    }

    for _, c := range contribs {
        if c == nil {
            continue
        }
        merged.Runtime.ExtraBuilds = append(merged.Runtime.ExtraBuilds, c.Runtime.ExtraBuilds...)
        merged.Gateway.Services = append(merged.Gateway.Services, c.Gateway.Services...)
        merged.Gateway.Volumes = append(merged.Gateway.Volumes, c.Gateway.Volumes...)
        for name, svc := range c.Sidecar.Services {
            merged.Sidecar.Services[name] = svc
        }
    }

    return merged
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/plugin/ -run TestMerge -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/merge.go internal/plugin/merge_test.go
git commit -m "feat(plugin): add contribution merging"
```

## Phase 4: Gateway Updates

### Task 4.1: Custom middleware compilation scaffolding

**Files:**
- Create: `core/gateway/middlewares/custom/stub.go`
- Modify: `core/gateway/cmd/gateway/main.go` (add blank import)

- [ ] **Step 1: Create the custom middleware stub package**

```go
// core/gateway/middlewares/custom/stub.go
package custom

// This package is a compilation target for user-provided custom middleware.
// At generate-time, the CLI copies custom .go files into this package directory.
// Each file uses init() to self-register via the gateway SDK.
//
// When no custom middleware exists, this package compiles to nothing.
```

- [ ] **Step 2: Add blank import in gateway main**

Add to `core/gateway/cmd/gateway/main.go`:

```go
import (
    // existing imports...
    _ "github.com/donbader/agent-sandbox/core/gateway/middlewares/custom"
)
```

- [ ] **Step 3: Commit**

```bash
git add core/gateway/middlewares/custom/stub.go core/gateway/cmd/gateway/main.go
git commit -m "feat(gateway): add custom middleware compilation package"
```

### Task 4.2: Gateway config generation from v1 contributions

**Files:**
- Create: `internal/generate/v1/gateway_config.go`
- Create: `internal/generate/v1/gateway_config_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/generate/v1/gateway_config_test.go
package v1

import (
    "testing"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestBuildGatewayConfig(t *testing.T) {
    cfg := &config.V1Config{
        Gateway: config.GatewayConfig{
            Services: []config.GatewayServiceEntry{
                {
                    URL:     "https://api.example.com",
                    Headers: map[string]string{"Authorization": "Bearer token123"},
                },
            },
        },
    }

    pluginContribs := &plugin.Contributions{
        Gateway: plugin.GatewayContrib{
            Services: []plugin.GatewayService{
                {
                    URL:     "https://github.com",
                    Headers: map[string]string{"Authorization": "Bearer ghp_abc"},
                },
            },
        },
    }

    gwCfg := BuildGatewayConfig(cfg, pluginContribs)

    require.Len(t, gwCfg.Services, 2)
    assert.Equal(t, "https://api.example.com", gwCfg.Services[0].URL)
    assert.Equal(t, "https://github.com", gwCfg.Services[1].URL)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestBuildGatewayConfig -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement gateway config builder**

```go
// internal/generate/v1/gateway_config.go
package v1

import (
    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
)

// GatewayConfigOutput is the merged gateway configuration for rendering.
type GatewayConfigOutput struct {
    Services    []GatewayServiceOutput
    Middlewares []string // paths to custom .go files to copy
}

type GatewayServiceOutput struct {
    URL     string
    Network string
    Headers map[string]string
}

// BuildGatewayConfig merges user gateway config with plugin contributions.
func BuildGatewayConfig(cfg *config.V1Config, contribs *plugin.Contributions) *GatewayConfigOutput {
    out := &GatewayConfigOutput{}

    // User-declared services
    for _, svc := range cfg.Gateway.Services {
        out.Services = append(out.Services, GatewayServiceOutput{
            URL:     svc.URL,
            Network: svc.Network,
            Headers: svc.Headers,
        })
        for _, mw := range svc.Middlewares {
            if mw.Custom != "" {
                out.Middlewares = append(out.Middlewares, mw.Custom)
            }
        }
    }

    // Plugin-contributed services
    if contribs != nil {
        for _, svc := range contribs.Gateway.Services {
            out.Services = append(out.Services, GatewayServiceOutput{
                URL:     svc.URL,
                Network: svc.Network,
                Headers: svc.Headers,
            })
            for _, mw := range svc.Middlewares {
                if mw.Custom != "" {
                    out.Middlewares = append(out.Middlewares, mw.Custom)
                }
            }
        }
    }

    return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/generate/v1/ -run TestBuildGatewayConfig -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/gateway_config.go internal/generate/v1/gateway_config_test.go
git commit -m "feat(generate): add v1 gateway config builder"
```

### Task 4.3: Copy custom middleware files during generation

**Files:**
- Create: `internal/generate/v1/middleware_copy.go`
- Create: `internal/generate/v1/middleware_copy_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/generate/v1/middleware_copy_test.go
package v1

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestCopyCustomMiddleware(t *testing.T) {
    projectDir := t.TempDir()
    outDir := t.TempDir()

    // Create a custom middleware file
    mwDir := filepath.Join(projectDir, "middlewares")
    require.NoError(t, os.MkdirAll(mwDir, 0755))
    mwContent := `package custom

import "github.com/donbader/agent-sandbox/core/sdk/gateway"

func init() {
    gateway.RegisterMiddleware("test", func(ctx *gateway.MiddlewareContext) error {
        return nil
    })
}
`
    require.NoError(t, os.WriteFile(filepath.Join(mwDir, "test.go"), []byte(mwContent), 0644))

    err := CopyCustomMiddleware(projectDir, outDir, []string{"./middlewares/test.go"})
    require.NoError(t, err)

    // Verify file was copied to the custom middleware package dir
    dest := filepath.Join(outDir, "gateway-src", "middlewares", "custom", "test.go")
    data, err := os.ReadFile(dest)
    require.NoError(t, err)
    assert.Contains(t, string(data), "RegisterMiddleware")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestCopyCustomMiddleware -v`
Expected: FAIL — `CopyCustomMiddleware` not defined

- [ ] **Step 3: Implement middleware copy**

```go
// internal/generate/v1/middleware_copy.go
package v1

import (
    "fmt"
    "os"
    "path/filepath"
)

// CopyCustomMiddleware copies custom middleware .go files into the gateway build context.
func CopyCustomMiddleware(projectDir, outDir string, middlewarePaths []string) error {
    if len(middlewarePaths) == 0 {
        return nil
    }

    destDir := filepath.Join(outDir, "gateway-src", "middlewares", "custom")
    if err := os.MkdirAll(destDir, 0755); err != nil {
        return fmt.Errorf("create middleware dest dir: %w", err)
    }

    for _, mwPath := range middlewarePaths {
        srcPath := filepath.Join(projectDir, mwPath)
        data, err := os.ReadFile(srcPath)
        if err != nil {
            return fmt.Errorf("read middleware %s: %w", mwPath, err)
        }

        destFile := filepath.Join(destDir, filepath.Base(mwPath))
        if err := os.WriteFile(destFile, data, 0644); err != nil {
            return fmt.Errorf("write middleware %s: %w", destFile, err)
        }
    }

    return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/generate/v1/ -run TestCopyCustomMiddleware -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/middleware_copy.go internal/generate/v1/middleware_copy_test.go
git commit -m "feat(generate): copy custom middleware into gateway build context"
```

## Phase 5: Compose/Dockerfile Generation

### Task 5.1: Dockerfile generation from v1 config + contributions

**Files:**
- Create: `internal/generate/v1/dockerfile.go`
- Create: `internal/generate/v1/dockerfile_test.go`
- Create: `core/templates/Dockerfile.agent.tmpl`

- [ ] **Step 1: Write failing test**

```go
// internal/generate/v1/dockerfile_test.go
package v1

import (
    "testing"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestBuildDockerfile(t *testing.T) {
    cfg := &config.V1Config{
        Runtime: config.RuntimeConfig{
            Image:       "node:24-slim",
            ExtraBuilds: []string{"RUN apt-get update && apt-get install -y git"},
            Entrypoint:  []string{"codex-acp", "--listen", ":8080"},
        },
    }

    contribs := &plugin.Contributions{
        Runtime: plugin.RuntimeContrib{
            ExtraBuilds: []string{"RUN npm install -g some-tool"},
        },
    }

    output, err := BuildDockerfile(cfg, contribs)
    require.NoError(t, err)

    assert.Contains(t, output, "FROM node:24-slim")
    assert.Contains(t, output, "RUN apt-get update && apt-get install -y git")
    assert.Contains(t, output, "RUN npm install -g some-tool")
    assert.Contains(t, output, `CMD ["codex-acp", "--listen", ":8080"]`)
}

func TestBuildDockerfile_BuiltinPreset(t *testing.T) {
    cfg := &config.V1Config{
        Runtime: config.RuntimeConfig{
            Image:      "@builtin/codex",
            Entrypoint: []string{"sleep", "infinity"},
        },
    }

    output, err := BuildDockerfile(cfg, nil)
    require.NoError(t, err)

    assert.Contains(t, output, "FROM node:24-slim")
    assert.Contains(t, output, "npm install -g @openai/codex")
    assert.Contains(t, output, `CMD ["sleep", "infinity"]`)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestBuildDockerfile -v`
Expected: FAIL — `BuildDockerfile` not defined

- [ ] **Step 3: Create Dockerfile template**

```
{{/* core/templates/Dockerfile.agent.tmpl */}}
FROM {{ .BaseImage }}

{{ range .PresetInstalls }}
RUN {{ . }}
{{ end }}

{{ range .UserExtraBuilds }}
{{ . }}
{{ end }}

{{ range .PluginExtraBuilds }}
{{ . }}
{{ end }}

{{ if .Entrypoint }}
CMD {{ .EntrypointJSON }}
{{ end }}
```

- [ ] **Step 4: Implement Dockerfile builder**

```go
// internal/generate/v1/dockerfile.go
package v1

import (
    "bytes"
    "encoding/json"
    "fmt"
    "strings"
    "text/template"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
)

var dockerfileTmpl = template.Must(template.New("Dockerfile").Parse(`FROM {{ .BaseImage }}
{{ range .PresetInstalls }}
RUN {{ . }}
{{ end }}
{{ range .UserExtraBuilds }}
{{ . }}
{{ end }}
{{ range .PluginExtraBuilds }}
{{ . }}
{{ end }}
{{ if .EntrypointJSON }}
CMD {{ .EntrypointJSON }}
{{ end }}
`))

// Presets maps @builtin/* to base image + install commands.
var Presets = map[string]struct {
    BaseImage string
    Installs  []string
}{
    "@builtin/codex": {
        BaseImage: "node:24-slim",
        Installs: []string{
            "apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*",
            "--mount=type=cache,target=/root/.npm npm install -g @openai/codex@0.136.0",
        },
    },
    "@builtin/claude-code": {
        BaseImage: "node:24-slim",
        Installs: []string{
            "apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*",
            "--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
        },
    },
    "@builtin/pi": {
        BaseImage: "node:24-slim",
        Installs: []string{
            "apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates && rm -rf /var/lib/apt/lists/*",
            "--mount=type=cache,target=/root/.npm npm install -g @anthropic-ai/claude-code",
        },
    },
}

type dockerfileData struct {
    BaseImage        string
    PresetInstalls   []string
    UserExtraBuilds  []string
    PluginExtraBuilds []string
    EntrypointJSON   string
}

// BuildDockerfile generates a Dockerfile string from config and plugin contributions.
func BuildDockerfile(cfg *config.V1Config, contribs *plugin.Contributions) (string, error) {
    data := dockerfileData{}

    // Resolve base image
    if preset, ok := Presets[cfg.Runtime.Image]; ok {
        data.BaseImage = preset.BaseImage
        data.PresetInstalls = preset.Installs
    } else {
        data.BaseImage = cfg.Runtime.Image
    }

    // User extra builds
    data.UserExtraBuilds = cfg.Runtime.ExtraBuilds

    // Plugin extra builds
    if contribs != nil {
        data.PluginExtraBuilds = contribs.Runtime.ExtraBuilds
    }

    // Entrypoint
    if len(cfg.Runtime.Entrypoint) > 0 {
        ep, err := json.Marshal(cfg.Runtime.Entrypoint)
        if err != nil {
            return "", fmt.Errorf("marshal entrypoint: %w", err)
        }
        data.EntrypointJSON = string(ep)
    }

    var buf bytes.Buffer
    if err := dockerfileTmpl.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("render Dockerfile: %w", err)
    }

    // Clean up excessive blank lines
    lines := strings.Split(buf.String(), "\n")
    var cleaned []string
    prevEmpty := false
    for _, line := range lines {
        empty := strings.TrimSpace(line) == ""
        if empty && prevEmpty {
            continue
        }
        cleaned = append(cleaned, line)
        prevEmpty = empty
    }

    return strings.Join(cleaned, "\n"), nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/generate/v1/ -run TestBuildDockerfile -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/generate/v1/dockerfile.go internal/generate/v1/dockerfile_test.go
git commit -m "feat(generate): v1 Dockerfile generation from config + contributions"
```

### Task 5.2: docker-compose.yaml generation

**Files:**
- Create: `internal/generate/v1/compose.go`
- Create: `internal/generate/v1/compose_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/generate/v1/compose_test.go
package v1

import (
    "testing"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestBuildCompose(t *testing.T) {
    cfg := &config.V1Config{
        Name: "test-agent",
        Runtime: config.RuntimeConfig{
            Volumes: []string{"data:/opt/data"},
        },
    }

    contribs := &plugin.Contributions{
        Sidecar: plugin.SidecarContrib{
            Services: map[string]plugin.ComposeService{
                "telegram": {
                    Build:       "./sidecar",
                    Environment: map[string]string{"AGENT_URL": "http://agent:8080"},
                },
            },
        },
    }

    output, err := BuildCompose(cfg, contribs)
    require.NoError(t, err)

    // Agent service present
    assert.Contains(t, output, "agent:")
    assert.Contains(t, output, "data:/opt/data")

    // Gateway service present
    assert.Contains(t, output, "gateway:")

    // Sidecar present
    assert.Contains(t, output, "telegram:")
    assert.Contains(t, output, "AGENT_URL")
}

func TestBuildCompose_NoSidecars(t *testing.T) {
    cfg := &config.V1Config{
        Name: "simple-agent",
        Runtime: config.RuntimeConfig{
            Image: "@builtin/codex",
        },
    }

    output, err := BuildCompose(cfg, nil)
    require.NoError(t, err)

    assert.Contains(t, output, "agent:")
    assert.Contains(t, output, "gateway:")
    assert.NotContains(t, output, "telegram:")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestBuildCompose -v`
Expected: FAIL — `BuildCompose` not defined

- [ ] **Step 3: Implement compose builder**

```go
// internal/generate/v1/compose.go
package v1

import (
    "fmt"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
    "gopkg.in/yaml.v3"
)

type composeFile struct {
    Services map[string]any `yaml:"services"`
    Volumes  map[string]any `yaml:"volumes,omitempty"`
    Networks map[string]any `yaml:"networks,omitempty"`
}

// BuildCompose generates a docker-compose.yaml string from config and plugin contributions.
func BuildCompose(cfg *config.V1Config, contribs *plugin.Contributions) (string, error) {
    compose := composeFile{
        Services: map[string]any{},
        Volumes:  map[string]any{},
        Networks: map[string]any{},
    }

    // Agent service
    agentSvc := map[string]any{
        "build": map[string]any{
            "context":    ".",
            "dockerfile": "Dockerfile",
        },
        "depends_on": []string{"gateway"},
        "networks":   []string{"sandbox"},
    }
    if len(cfg.Runtime.Volumes) > 0 {
        agentSvc["volumes"] = cfg.Runtime.Volumes
    }
    compose.Services["agent"] = agentSvc

    // Gateway service
    gatewaySvc := map[string]any{
        "build": map[string]any{
            "context":    "./gateway-src",
            "dockerfile": "Dockerfile",
        },
        "networks": []string{"sandbox"},
        "healthcheck": map[string]any{
            "test":     []string{"CMD", "wget", "--spider", "-q", "http://localhost:8080/health"},
            "interval": "5s",
            "timeout":  "3s",
            "retries":  3,
        },
    }
    compose.Services["gateway"] = gatewaySvc

    // Sidecar services from plugins
    if contribs != nil {
        for name, svc := range contribs.Sidecar.Services {
            sidecarSvc := map[string]any{
                "networks": []string{"sandbox"},
            }
            if svc.Build != "" {
                sidecarSvc["build"] = svc.Build
            }
            if svc.Image != "" {
                sidecarSvc["image"] = svc.Image
            }
            if len(svc.Environment) > 0 {
                sidecarSvc["environment"] = svc.Environment
            }
            if len(svc.Volumes) > 0 {
                sidecarSvc["volumes"] = svc.Volumes
            }
            if len(svc.Ports) > 0 {
                sidecarSvc["ports"] = svc.Ports
            }
            if svc.Healthcheck != nil {
                sidecarSvc["healthcheck"] = svc.Healthcheck
            }
            if svc.DependsOn != nil {
                sidecarSvc["depends_on"] = svc.DependsOn
            }
            compose.Services[name] = sidecarSvc
        }
    }

    // Sandbox network
    compose.Networks["sandbox"] = map[string]any{"driver": "bridge"}

    // Extract named volumes
    for _, v := range cfg.Runtime.Volumes {
        volName := extractVolumeName(v)
        if volName != "" {
            compose.Volumes[volName] = nil
        }
    }

    data, err := yaml.Marshal(compose)
    if err != nil {
        return "", fmt.Errorf("marshal compose: %w", err)
    }
    return string(data), nil
}

func extractVolumeName(volume string) string {
    // Named volumes have format "name:/path" (no leading . or /)
    for i, c := range volume {
        if c == ':' {
            name := volume[:i]
            if len(name) > 0 && name[0] != '.' && name[0] != '/' {
                return name
            }
            return ""
        }
    }
    return ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/generate/v1/ -run TestBuildCompose -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/compose.go internal/generate/v1/compose_test.go
git commit -m "feat(generate): v1 docker-compose generation"
```

### Task 5.3: V1 Generator orchestrator

**Files:**
- Create: `internal/generate/v1/generator.go`
- Create: `internal/generate/v1/generator_test.go`

- [ ] **Step 1: Write failing integration test**

```go
// internal/generate/v1/generator_test.go
package v1

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestGenerator_Run(t *testing.T) {
    projectDir := t.TempDir()

    // Write agent.yaml
    agentYAML := `
name: test-agent
log_level: debug
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
gateway:
  services:
    - url: https://api.example.com
      headers:
        Authorization: Bearer ${TOKEN}
`
    require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

    // Write a local plugin
    pluginDir := filepath.Join(projectDir, "plugins", "my-tool")
    require.NoError(t, os.MkdirAll(pluginDir, 0755))
    pluginYAML := `
name: my-tool
options:
  version:
    type: string
    default: "1.0.0"
contributes:
  runtime:
    extra_builds:
      - "RUN npm install -g my-tool@{{ .options.version }}"
`
    require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(pluginYAML), 0644))

    // Update agent.yaml to include the plugin
    agentYAML = `
name: test-agent
log_level: debug
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
gateway:
  services:
    - url: https://api.example.com
      headers:
        Authorization: Bearer ${TOKEN}
installations:
  - plugin: my-tool
    options:
      version: "2.0.0"
`
    require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

    g := NewGenerator(projectDir, nil)
    require.NoError(t, g.Run())

    // Verify outputs
    buildDir := filepath.Join(projectDir, ".build")
    assert.FileExists(t, filepath.Join(buildDir, "Dockerfile"))
    assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yaml"))

    // Check Dockerfile content
    df, err := os.ReadFile(filepath.Join(buildDir, "Dockerfile"))
    require.NoError(t, err)
    assert.Contains(t, string(df), "FROM node:24-slim")
    assert.Contains(t, string(df), "npm install -g my-tool@2.0.0")
    assert.Contains(t, string(df), `CMD ["sleep", "infinity"]`)

    // Check compose content
    comp, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yaml"))
    require.NoError(t, err)
    assert.Contains(t, string(comp), "agent:")
    assert.Contains(t, string(comp), "gateway:")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestGenerator_Run -v`
Expected: FAIL — `NewGenerator` not defined

- [ ] **Step 3: Implement Generator orchestrator**

```go
// internal/generate/v1/generator.go
package v1

import (
    "fmt"
    "io/fs"
    "os"
    "path/filepath"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/plugin"
)

// Generator orchestrates v1 build artifact generation.
type Generator struct {
    projectDir string
    bundledFS  fs.FS
}

// NewGenerator creates a v1 generator for the given project directory.
func NewGenerator(projectDir string, bundledFS fs.FS) *Generator {
    return &Generator{projectDir: projectDir, bundledFS: bundledFS}
}

// Run executes the full generation pipeline.
func (g *Generator) Run() error {
    // 1. Load config
    cfg, err := config.LoadV1(g.projectDir)
    if err != nil {
        return fmt.Errorf("load config: %w", err)
    }

    // 2. Resolve and render plugins
    resolver := plugin.NewResolver(g.projectDir, g.bundledFS)
    var allContribs []*plugin.Contributions

    for _, inst := range cfg.Installations {
        pluginDef, err := resolver.Resolve(inst.Plugin, inst.Source)
        if err != nil {
            return fmt.Errorf("resolve plugin %q: %w", inst.Plugin, err)
        }

        rendered, err := plugin.RenderContributions(pluginDef, inst.Options)
        if err != nil {
            return fmt.Errorf("render plugin %q: %w", inst.Plugin, err)
        }

        allContribs = append(allContribs, rendered)
    }

    merged := plugin.MergeContributions(allContribs...)

    // 3. Create output directory
    buildDir := filepath.Join(g.projectDir, ".build")
    if err := os.MkdirAll(buildDir, 0755); err != nil {
        return fmt.Errorf("create .build dir: %w", err)
    }

    // 4. Generate Dockerfile
    dockerfile, err := BuildDockerfile(cfg, merged)
    if err != nil {
        return fmt.Errorf("build dockerfile: %w", err)
    }
    if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
        return fmt.Errorf("write Dockerfile: %w", err)
    }

    // 5. Generate docker-compose.yaml
    compose, err := BuildCompose(cfg, merged)
    if err != nil {
        return fmt.Errorf("build compose: %w", err)
    }
    if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yaml"), []byte(compose), 0644); err != nil {
        return fmt.Errorf("write docker-compose.yaml: %w", err)
    }

    // 6. Build gateway config + copy middleware
    gwCfg := BuildGatewayConfig(cfg, merged)
    if len(gwCfg.Middlewares) > 0 {
        if err := CopyCustomMiddleware(g.projectDir, buildDir, gwCfg.Middlewares); err != nil {
            return fmt.Errorf("copy middleware: %w", err)
        }
    }

    return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/generate/v1/ -run TestGenerator_Run -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/generator.go internal/generate/v1/generator_test.go
git commit -m "feat(generate): v1 generator orchestrator"
```

## Phase 6: Core Versioning

### Task 6.1: Core version fetcher

**Files:**
- Create: `internal/core/fetcher.go`
- Create: `internal/core/fetcher_test.go`

- [ ] **Step 1: Write failing test for local cache check**

```go
// internal/core/fetcher_test.go
package core

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestCacheDir(t *testing.T) {
    dir := CacheDir("v1.0.0")
    assert.Contains(t, dir, "agent-sandbox")
    assert.Contains(t, dir, "v1.0.0")
}

func TestIsCached(t *testing.T) {
    cacheDir := t.TempDir()
    version := "v1.0.0"
    versionDir := filepath.Join(cacheDir, version)

    // Not cached yet
    assert.False(t, IsCachedAt(versionDir))

    // Create marker
    require.NoError(t, os.MkdirAll(versionDir, 0755))
    require.NoError(t, os.WriteFile(filepath.Join(versionDir, ".complete"), []byte(""), 0644))
    assert.True(t, IsCachedAt(versionDir))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestCache -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement cache logic**

```go
// internal/core/fetcher.go
package core

import (
    "fmt"
    "os"
    "path/filepath"
    "runtime"
)

// CacheDir returns the path where a specific core version is cached.
func CacheDir(version string) string {
    base := cacheBase()
    return filepath.Join(base, version)
}

// IsCachedAt checks if a core version is fully downloaded at the given path.
func IsCachedAt(versionDir string) bool {
    _, err := os.Stat(filepath.Join(versionDir, ".complete"))
    return err == nil
}

// Fetch downloads a core version if not already cached. Returns the path to the cached core.
func Fetch(version string) (string, error) {
    dir := CacheDir(version)
    if IsCachedAt(dir) {
        return dir, nil
    }

    // Download from GitHub releases
    if err := download(version, dir); err != nil {
        // Clean up partial download
        os.RemoveAll(dir)
        return "", fmt.Errorf("fetch core %s: %w", version, err)
    }

    // Mark complete
    if err := os.WriteFile(filepath.Join(dir, ".complete"), []byte(version), 0644); err != nil {
        return "", fmt.Errorf("mark complete: %w", err)
    }

    return dir, nil
}

func cacheBase() string {
    if dir := os.Getenv("AGENT_SANDBOX_CACHE"); dir != "" {
        return filepath.Join(dir, "core")
    }
    home, _ := os.UserHomeDir()
    switch runtime.GOOS {
    case "darwin":
        return filepath.Join(home, "Library", "Caches", "agent-sandbox", "core")
    default:
        if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
            return filepath.Join(xdg, "agent-sandbox", "core")
        }
        return filepath.Join(home, ".cache", "agent-sandbox", "core")
    }
}

func download(version, destDir string) error {
    // TODO: implement GitHub release tarball download
    // For now, return error indicating not implemented
    return fmt.Errorf("remote fetch not yet implemented for version %s (use local core)", version)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/core/ -run TestCache -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/fetcher.go internal/core/fetcher_test.go
git commit -m "feat(core): add core version caching logic"
```

### Task 6.2: Integrate core version into generator

**Files:**
- Modify: `internal/generate/v1/generator.go`
- Modify: `internal/generate/v1/generator_test.go`

- [ ] **Step 1: Write failing test for core version resolution**

```go
// Add to internal/generate/v1/generator_test.go
func TestGenerator_UsesLocalCore(t *testing.T) {
    projectDir := t.TempDir()
    coreDir := t.TempDir()

    // Create a preset in the core dir
    presetDir := filepath.Join(coreDir, "presets", "codex")
    require.NoError(t, os.MkdirAll(presetDir, 0755))
    require.NoError(t, os.WriteFile(filepath.Join(presetDir, "runtime.yaml"), []byte(`
base_image: node:24-slim
install:
  - apt-get update && apt-get install -y git
`), 0644))

    // Create bundled plugin in core
    pluginDir := filepath.Join(coreDir, "plugins", "github-pat")
    require.NoError(t, os.MkdirAll(pluginDir, 0755))
    require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
name: github-pat
options:
  token:
    type: string
    required: true
contributes:
  gateway:
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`), 0644))

    agentYAML := `
name: test-agent
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: github-pat
    options:
      token: "${GITHUB_PAT}"
`
    require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

    g := NewGeneratorWithCore(projectDir, coreDir)
    require.NoError(t, g.Run())

    buildDir := filepath.Join(projectDir, ".build")
    assert.FileExists(t, filepath.Join(buildDir, "Dockerfile"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/generate/v1/ -run TestGenerator_UsesLocalCore -v`
Expected: FAIL — `NewGeneratorWithCore` not defined

- [ ] **Step 3: Add core directory support to Generator**

Update `generator.go`:

```go
// NewGeneratorWithCore creates a v1 generator that reads bundled plugins from a specific core directory.
func NewGeneratorWithCore(projectDir, coreDir string) *Generator {
    var bundled fs.FS
    if coreDir != "" {
        pluginsDir := filepath.Join(coreDir, "plugins")
        bundled = os.DirFS(pluginsDir)
    }
    return &Generator{projectDir: projectDir, bundledFS: bundled, coreDir: coreDir}
}
```

Add `coreDir string` field to `Generator` struct.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/generate/v1/ -run TestGenerator_UsesLocalCore -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/generator.go internal/generate/v1/generator_test.go
git commit -m "feat(generate): integrate core directory into v1 generator"
```

### Task 6.3: CLI generate command for v1

**Files:**
- Create: `cmd/agent-sandbox/cmd_generate_v1.go`

- [ ] **Step 1: Implement v1 generate command**

```go
// cmd/agent-sandbox/cmd_generate_v1.go
package main

import (
    "fmt"
    "os"
    "path/filepath"

    "github.com/donbader/agent-sandbox/internal/config"
    "github.com/donbader/agent-sandbox/internal/core"
    v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
    "github.com/spf13/cobra"
)

var generateV1Cmd = &cobra.Command{
    Use:   "generate",
    Short: "Generate build artifacts from agent.yaml (v1)",
    RunE: func(cmd *cobra.Command, args []string) error {
        dir, _ := cmd.Flags().GetString("dir")
        if dir == "" {
            dir = "."
        }
        dir, err := filepath.Abs(dir)
        if err != nil {
            return fmt.Errorf("resolve dir: %w", err)
        }

        // Load config to get core_version
        cfg, err := config.LoadV1(dir)
        if err != nil {
            return err
        }

        // Resolve core directory
        var coreDir string
        if cfg.CoreVersion != "" {
            coreDir, err = core.Fetch(cfg.CoreVersion)
            if err != nil {
                return fmt.Errorf("fetch core %s: %w", cfg.CoreVersion, err)
            }
            fmt.Fprintf(os.Stderr, "Using core %s from %s\n", cfg.CoreVersion, coreDir)
        }

        g := v1.NewGeneratorWithCore(dir, coreDir)
        if err := g.Run(); err != nil {
            return err
        }

        fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", dir)
        return nil
    },
}

func init() {
    generateV1Cmd.Flags().StringP("dir", "C", ".", "Project directory")
}
```

- [ ] **Step 2: Wire into root command (replace or add alongside existing)**

This depends on how the v1 transition is handled. For now, replace the existing generate command on the v1 branch.

- [ ] **Step 3: Run manual test**

```bash
go build ./cmd/agent-sandbox/ && ./agent-sandbox generate -C examples/local-coding/
```

Expected: generates `.build/` with Dockerfile and docker-compose.yaml (will fail until example is updated to v1 format)

- [ ] **Step 4: Commit**

```bash
git add cmd/agent-sandbox/cmd_generate_v1.go
git commit -m "feat(cli): add v1 generate command"
```

## Phase 7: Example (telegram-acp)

### Task 7.1: Create telegram-acp example plugin structure

**Files:**
- Create: `examples/telegram-acp/agent.yaml`
- Create: `examples/telegram-acp/plugins/telegram-acp/plugin.yaml`
- Create: `examples/telegram-acp/plugins/telegram-acp/middlewares/telegram-token-rewrite.go`
- Create: `examples/telegram-acp/plugins/telegram-acp/sidecar/Dockerfile`
- Create: `examples/telegram-acp/plugins/telegram-acp/sidecar/package.json`
- Create: `examples/telegram-acp/plugins/telegram-acp/sidecar/src/index.ts`

- [ ] **Step 1: Create example directory structure**

```bash
mkdir -p examples/telegram-acp/plugins/telegram-acp/middlewares
mkdir -p examples/telegram-acp/plugins/telegram-acp/sidecar/src
```

- [ ] **Step 2: Write agent.yaml for the example**

```yaml
# examples/telegram-acp/agent.yaml
name: telegram-agent
log_level: debug

runtime:
  image: "@builtin/codex"
  extra_builds:
    - "RUN --mount=type=cache,target=/root/.npm npm install -g @zed-industries/codex-acp@0.15.0"
  entrypoint: ["codex-acp", "--listen", ":8080"]

gateway:
  services:
    - url: https://agent-gateway.stx-ai.net
      headers:
        Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}

installations:
  - plugin: telegram-acp
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      acp_package: "@zed-industries/codex-acp"
      allowed_users:
        - "@${TELEGRAM_USERNAME}"
```

- [ ] **Step 3: Write plugin.yaml**

```yaml
# examples/telegram-acp/plugins/telegram-acp/plugin.yaml
name: telegram-acp
options:
  bot_token:
    type: string
    required: true
    description: "Telegram bot token"
  acp_package:
    type: string
    required: false
    default: "@zed-industries/codex-acp"
    description: "ACP adapter npm package name"
  allowed_users:
    type: array
    required: false
    description: "Telegram usernames allowed to interact with the bot"
    items:
      type: string

contributes:
  gateway:
    services:
      - url: https://api.telegram.org
        middlewares:
          - custom: "./middlewares/telegram-token-rewrite.go"
  sidecar:
    services:
      telegram:
        build: ./plugins/telegram-acp/sidecar
        environment:
          AGENT_ACP_URL: "http://gateway:8080/agent"
          TELEGRAM_BOT_TOKEN: "{{ .options.bot_token }}"
        depends_on:
          gateway:
            condition: service_healthy
        healthcheck:
          test: ["CMD", "node", "-e", "fetch('http://localhost:3000/health').then(r => process.exit(r.ok ? 0 : 1))"]
          interval: 10s
          timeout: 5s
          retries: 3
```

- [ ] **Step 4: Write custom middleware for Telegram token rewriting**

```go
// examples/telegram-acp/plugins/telegram-acp/middlewares/telegram-token-rewrite.go
package custom

import (
    "strings"

    "github.com/donbader/agent-sandbox/core/sdk/gateway"
)

func init() {
    gateway.RegisterMiddleware("telegram-token-rewrite", func(ctx *gateway.MiddlewareContext) error {
        // Telegram Bot API uses the token in the URL path: /bot<token>/method
        // The agent uses a dummy token; the gateway rewrites it to the real one.
        realToken := ctx.Env("TELEGRAM_BOT_TOKEN")
        if realToken == "" {
            return nil
        }

        path := ctx.Request.URL.Path
        // Replace /bot<dummy>/... with /bot<real>/...
        if idx := strings.Index(path, "/bot"); idx != -1 {
            // Find end of token (next /)
            rest := path[idx+4:]
            if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
                method := rest[slashIdx:]
                ctx.Request.URL.Path = path[:idx] + "/bot" + realToken + method
            }
        }

        return nil
    })
}
```

- [ ] **Step 5: Write sidecar Dockerfile**

```dockerfile
# examples/telegram-acp/plugins/telegram-acp/sidecar/Dockerfile
FROM node:22-slim

WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --production
COPY src/ ./src/

EXPOSE 3000
CMD ["node", "src/index.js"]
```

- [ ] **Step 6: Write minimal sidecar entry point (placeholder for channel-manager port)**

```typescript
// examples/telegram-acp/plugins/telegram-acp/sidecar/src/index.ts
import http from "node:http";

// Minimal placeholder showing the sidecar pattern.
// In a real implementation, this would be the channel-manager code
// that connects to the agent via ACP and bridges to Telegram.

const AGENT_ACP_URL = process.env.AGENT_ACP_URL || "http://gateway:8080/agent";
const PORT = 3000;

const server = http.createServer((req, res) => {
  if (req.url === "/health") {
    res.writeHead(200);
    res.end("ok");
    return;
  }
  res.writeHead(404);
  res.end();
});

server.listen(PORT, () => {
  console.log(`Telegram ACP sidecar running on :${PORT}`);
  console.log(`Agent ACP URL: ${AGENT_ACP_URL}`);
});
```

- [ ] **Step 7: Write package.json**

```json
{
  "name": "telegram-acp-sidecar",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "start": "node src/index.js"
  },
  "dependencies": {}
}
```

- [ ] **Step 8: Commit**

```bash
git add examples/telegram-acp/
git commit -m "feat(examples): add telegram-acp example with plugin structure"
```

### Task 7.2: Verify end-to-end generation with example

**Files:**
- No new files — validation only

- [ ] **Step 1: Run v1 generate against the example**

```bash
go run ./cmd/agent-sandbox/ generate -C examples/telegram-acp/
```

Expected: `.build/` directory created with:
- `Dockerfile` containing codex base + codex-acp install + `CMD ["codex-acp", "--listen", ":8080"]`
- `docker-compose.yaml` with agent, gateway, and telegram sidecar services
- `gateway-src/middlewares/custom/telegram-token-rewrite.go`

- [ ] **Step 2: Verify Dockerfile content**

```bash
cat examples/telegram-acp/.build/Dockerfile
```

Expected output should contain:
```
FROM node:24-slim
RUN apt-get update...
RUN npm install -g @openai/codex...
RUN npm install -g @zed-industries/codex-acp@0.15.0
CMD ["codex-acp", "--listen", ":8080"]
```

- [ ] **Step 3: Verify docker-compose.yaml content**

```bash
cat examples/telegram-acp/.build/docker-compose.yaml
```

Expected: agent, gateway, and telegram services all present with correct networks.

- [ ] **Step 4: Verify middleware was copied**

```bash
cat examples/telegram-acp/.build/gateway-src/middlewares/custom/telegram-token-rewrite.go
```

Expected: Contains `RegisterMiddleware("telegram-token-rewrite", ...)`

- [ ] **Step 5: Commit .gitignore for .build/ if not already present**

```bash
echo ".build/" >> examples/telegram-acp/.gitignore
git add examples/telegram-acp/.gitignore
git commit -m "chore: gitignore .build/ in telegram-acp example"
```

### Task 7.3: Clean up old v0 code (on v1 branch)

**Files:**
- Remove: `channel-manager/` (entire directory)
- Remove: `internal/plugins/telegram/`
- Remove: `internal/plugins/custom-runtime/`
- Remove: `internal/plugins/register.go`
- Remove: `internal/resolve/` (old plugin resolution)

- [ ] **Step 1: Remove old channel-manager and telegram plugin**

```bash
git rm -r channel-manager/
git rm -r internal/plugins/telegram/
git rm -r internal/plugins/custom-runtime/
git rm internal/plugins/register.go
```

- [ ] **Step 2: Remove old resolve package (replaced by internal/plugin/resolve.go)**

```bash
git rm -r internal/resolve/
```

- [ ] **Step 3: Update any imports that referenced removed packages**

Check `cmd/agent-sandbox/` for imports of removed packages and update to use the new v1 generation path.

- [ ] **Step 4: Verify build still compiles**

```bash
go build ./...
```

Expected: Clean compilation with no errors.

- [ ] **Step 5: Verify tests pass**

```bash
go test ./...
```

Expected: All tests pass (old tests for removed packages are gone, new v1 tests pass).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: remove v0 channel-manager, telegram, and old plugin system"
```
