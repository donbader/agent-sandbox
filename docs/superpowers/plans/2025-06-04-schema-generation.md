# Schema Generation + Old Config Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove obsolete `AgentConfig`, unify on `V1Config` (renamed to `Config`), and generate `.build/schema.json` from struct tags during `generate`.

**Architecture:** Single config struct with `jsonschema` struct tags → `invopop/jsonschema` reflects at generate-time → writes `.build/schema.json`. Init command updated to emit the correct shape.

**Tech Stack:** Go 1.24+, `github.com/invopop/jsonschema`, cobra CLI

---

### Task 1: Add `invopop/jsonschema` dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
flox activate -- go get github.com/invopop/jsonschema@latest
```

- [ ] **Step 2: Tidy**

```bash
flox activate -- go mod tidy
```

- [ ] **Step 3: Verify it resolved**

```bash
flox activate -- grep invopop go.mod
```

Expected: line like `github.com/invopop/jsonschema vX.Y.Z`

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add invopop/jsonschema dependency"
```

---

### Task 2: Remove `AgentConfig`, `Load()`, and related code

**Files:**
- Modify: `internal/config/config.go` (delete lines 55-94, keep FeatureEntry + Fleet code for now)
- Modify: `internal/config/config_test.go` (delete entire file or gut it)
- Modify: `cmd/agent-sandbox/main.go` (init command references)

- [ ] **Step 1: Delete `AgentConfig` struct and `Load()` from config.go**

Remove lines 55-94 from `internal/config/config.go`:
- `AgentConfig` struct (lines 55-62)
- `GatewayEnabled()` method (lines 66-71)
- `Load(dir)` function (lines 74-94)

Keep everything else (FeatureEntry, FleetConfig, etc. — those may still be used elsewhere or can be cleaned up later).

- [ ] **Step 2: Delete `config_test.go`**

Delete `internal/config/config_test.go` entirely — all tests reference the removed `Load()` and `AgentConfig`.

- [ ] **Step 3: Verify the build compiles**

```bash
flox activate -- go build ./...
```

If there are compile errors from other files referencing `AgentConfig` or `config.Load()`, fix them. Check `cmd/agent-sandbox/main.go` init command (lines 98-195) — it likely calls `config.Load()`.

- [ ] **Step 4: Run remaining tests**

```bash
flox activate -- go test ./...
```

Expected: all pass (v1_test.go still works, config_test.go is gone).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: remove obsolete AgentConfig and Load()"
```

---

### Task 3: Rename `V1Config` → `Config` and `LoadV1` → `Load`

**Files:**
- Modify: `internal/config/v1.go` (rename types and function)
- Modify: `internal/config/v1_test.go` (update references)
- Modify: `cmd/agent-sandbox/cmd_generate_v1.go:25` (calls `config.LoadV1`)
- Modify: `internal/generate/v1/generator.go:44` (calls `config.LoadV1`)

- [ ] **Step 1: Rename in v1.go**

In `internal/config/v1.go`:
- Rename `V1Config` → `Config` (line 13)
- Rename `LoadV1` → `Load` (line 56)
- Update the return type of `Load` from `*V1Config` to `*Config`

- [ ] **Step 2: Update all callers**

Search for `config.LoadV1` and `config.V1Config` across the codebase and replace:
- `config.LoadV1` → `config.Load`
- `config.V1Config` → `config.Config`
- `*config.V1Config` → `*config.Config`

Known locations:
- `cmd/agent-sandbox/cmd_generate_v1.go:25`
- `internal/generate/v1/generator.go:44`

- [ ] **Step 3: Update test file**

In `internal/config/v1_test.go`:
- Rename all `LoadV1` references to `Load`
- Rename test functions: `TestLoadV1_*` → `TestLoad_*`

- [ ] **Step 4: Optionally rename the file**

Rename `internal/config/v1.go` → `internal/config/config.go` (since we deleted the old one).
Rename `internal/config/v1_test.go` → `internal/config/config_test.go`.

```bash
git mv internal/config/v1.go internal/config/config.go
git mv internal/config/v1_test.go internal/config/config_test.go
```

- [ ] **Step 5: Verify build and tests**

```bash
flox activate -- go build ./... && flox activate -- go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename V1Config to Config, LoadV1 to Load"
```

---

### Task 4: Add `jsonschema` struct tags to Config

**Files:**
- Modify: `internal/config/config.go` (the renamed file from Task 3)

- [ ] **Step 1: Add jsonschema tags to all config structs**

Update `internal/config/config.go` structs with `jsonschema` tags:

```go
type Config struct {
	Name          string          `yaml:"name" jsonschema:"required,title=name,description=Agent instance name"`
	LogLevel      string          `yaml:"log_level" jsonschema:"title=log_level,description=Logging verbosity,enum=info,enum=debug"`
	CoreVersion   string          `yaml:"core_version" jsonschema:"title=core_version,description=Core version to use for generation"`
	Runtime       RuntimeConfig   `yaml:"runtime" jsonschema:"required,title=runtime,description=Agent container configuration"`
	Gateway       GatewayConfig   `yaml:"gateway" jsonschema:"title=gateway,description=Transparent egress proxy configuration"`
	Installations []Installation  `yaml:"installations" jsonschema:"title=installations,description=Plugins to install"`
}

type RuntimeConfig struct {
	Image       string   `yaml:"image" jsonschema:"required,title=image,description=Base image (@builtin/codex or any Docker image)"`
	ExtraBuilds []string `yaml:"extra_builds" jsonschema:"title=extra_builds,description=Additional Dockerfile instructions layered after the base"`
	Entrypoint  []string `yaml:"entrypoint" jsonschema:"title=entrypoint,description=Container CMD override"`
	Volumes     []string `yaml:"volumes" jsonschema:"title=volumes,description=Named or bind mount volumes"`
}

type GatewayConfig struct {
	Services []GatewayServiceEntry `yaml:"services" jsonschema:"title=services,description=External services proxied through the gateway"`
}

type GatewayServiceEntry struct {
	URL         string            `yaml:"url" jsonschema:"required,title=url,description=Service endpoint: HTTPS URL or plain host:port for sidecars"`
	Network     string            `yaml:"network" jsonschema:"title=network,description=Compose network to attach (optional)"`
	Headers     map[string]string `yaml:"headers" jsonschema:"title=headers,description=Headers injected by gateway on every proxied request"`
	Middlewares []MiddlewareEntry `yaml:"middlewares" jsonschema:"title=middlewares,description=Custom middleware chain"`
}

type MiddlewareEntry struct {
	Custom string `yaml:"custom" jsonschema:"required,title=custom,description=Relative path to custom middleware .go file"`
}

type Installation struct {
	Plugin  string         `yaml:"plugin" jsonschema:"required,title=plugin,description=Plugin name (bundled or local)"`
	Source  string         `yaml:"source" jsonschema:"title=source,description=Plugin source (local path or remote git URL)"`
	Options map[string]any `yaml:"options" jsonschema:"title=options,description=Plugin-specific configuration options"`
}
```

- [ ] **Step 2: Verify build**

```bash
flox activate -- go build ./...
```

Expected: compiles cleanly. The jsonschema tags are just metadata until reflected.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add jsonschema struct tags to Config types"
```

---

### Task 5: Add schema generation to the generate pipeline

**Files:**
- Modify: `internal/generate/v1/generator.go` (add schema step to `Run()`)
- Create: `internal/generate/v1/schema.go` (schema generation function)
- Create: `internal/generate/v1/schema_test.go` (test)

- [ ] **Step 1: Write the failing test**

Create `internal/generate/v1/schema_test.go`:

```go
package v1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSchema(t *testing.T) {
	outDir := t.TempDir()

	err := generateSchema(outDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outDir, "schema.json"))
	require.NoError(t, err)

	var schema map[string]any
	err = json.Unmarshal(data, &schema)
	require.NoError(t, err, "schema.json should be valid JSON")

	// Verify it's a JSON Schema
	assert.Contains(t, schema, "$schema")
	assert.Contains(t, schema, "properties")

	// Verify key properties exist
	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "runtime")
	assert.Contains(t, props, "gateway")
	assert.Contains(t, props, "installations")

	// Verify required fields
	required := schema["required"].([]any)
	assert.Contains(t, required, "name")
	assert.Contains(t, required, "runtime")

	// Verify nested runtime properties
	runtimeProps := props["runtime"].(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, runtimeProps, "image")
	assert.Contains(t, runtimeProps, "extra_builds")
	assert.Contains(t, runtimeProps, "entrypoint")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
flox activate -- go test ./internal/generate/v1/ -run TestGenerateSchema -v
```

Expected: FAIL — `generateSchema` undefined.

- [ ] **Step 3: Implement schema generation**

Create `internal/generate/v1/schema.go`:

```go
package v1

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/invopop/jsonschema"
)

func generateSchema(outDir string) error {
	reflector := &jsonschema.Reflector{
		DoNotReference: true,
	}
	schema := reflector.Reflect(&config.Config{})
	schema.Title = "agent-sandbox configuration"
	schema.Description = "Configuration schema for agent-sandbox agent.yaml"

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	return os.WriteFile(filepath.Join(outDir, "schema.json"), data, 0644)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
flox activate -- go test ./internal/generate/v1/ -run TestGenerateSchema -v
```

Expected: PASS.

- [ ] **Step 5: Wire into the Run() pipeline**

In `internal/generate/v1/generator.go`, add the schema generation step at the end of `Run()` (after line ~131, before the final return):

```go
	// Step 8: Generate JSON Schema
	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}
```

Where `buildDir` is the `.build/` directory path (should be `filepath.Join(g.projectDir, ".build")`).

- [ ] **Step 6: Run full test suite**

```bash
flox activate -- go test ./...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/generate/v1/schema.go internal/generate/v1/schema_test.go internal/generate/v1/generator.go
git commit -m "feat: generate .build/schema.json from Config struct tags"
```

---

### Task 6: Fix the `init` command to emit V1-shaped yaml

**Files:**
- Modify: `cmd/agent-sandbox/main.go:98-195` (init command)

- [ ] **Step 1: Read the current init command**

Read `cmd/agent-sandbox/main.go` lines 98-195 to understand the current scaffolding logic.

- [ ] **Step 2: Update the init template output**

Replace the yaml generation in the init command to produce:

```yaml
# yaml-language-server: $schema=.build/schema.json
name: <user-input>
core_version: v1.0.0
runtime:
  image: "@builtin/<selected-runtime>"
  entrypoint: ["sleep", "infinity"]
gateway:
  services: []
installations: []
```

The runtime selection (codex, claude-code, pi) should map to `@builtin/codex`, `@builtin/claude-code`, `@builtin/pi` respectively.

Remove any feature/plugin scaffolding that uses the old `AgentConfig` format (features array, flat runtime string).

- [ ] **Step 3: Remove references to old config types**

If the init command imports or references `config.AgentConfig`, `config.Load()`, or `config.FeatureEntry`, remove those references. The init command should write raw yaml (template string), not marshal a struct.

- [ ] **Step 4: Verify init produces valid config**

```bash
flox activate -- go build ./cmd/agent-sandbox/
```

Then manually test (or write a test):
```bash
mkdir /tmp/test-init && cd /tmp/test-init
/path/to/agent-sandbox init
cat agent.yaml
```

The output should be parseable by `config.Load()`.

- [ ] **Step 5: Run full test suite**

```bash
flox activate -- go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/agent-sandbox/main.go
git commit -m "fix: init command produces V1-shaped agent.yaml"
```

---

### Task 7: Update the schema comment helper

**Files:**
- Modify: `cmd/agent-sandbox/main.go:45-66` (`ensureSchemaComment` function)

- [ ] **Step 1: Verify `ensureSchemaComment` still works**

Read the function at lines 45-66. It should already insert `# yaml-language-server: $schema=.build/schema.json`. If it references anything from the old config, update it.

- [ ] **Step 2: Check for schema_comment_test.go**

If there's a test file for this function, verify it still passes:

```bash
flox activate -- go test ./cmd/agent-sandbox/ -run Schema -v
```

- [ ] **Step 3: Commit if changes were needed**

```bash
git add -A
git commit -m "fix: update schema comment helper for V1 config"
```

---

### Task 8: Clean up dead code (FleetConfig, FeatureEntry)

**Files:**
- Modify: `internal/config/config.go` (the original file, if FeatureEntry/FleetConfig are orphaned)

- [ ] **Step 1: Check if FleetConfig/FeatureEntry are still referenced**

```bash
flox activate -- grep -r "FleetConfig\|FeatureEntry\|LoadFleet\|MergeSharedFeatures" --include="*.go" .
```

- [ ] **Step 2: If unreferenced, remove them**

If nothing references `FleetConfig`, `FeatureEntry`, `LoadFleet`, `HasFleetConfig`, or `MergeSharedFeatures`, delete them. If they're still used by the fleet/compose workflow, leave them.

- [ ] **Step 3: Verify build and tests**

```bash
flox activate -- go build ./... && flox activate -- go test ./...
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "refactor: remove unused FleetConfig and FeatureEntry types"
```

---

### Task 9: End-to-end verification

**Files:**
- Test: `examples/local-coding/agent.yaml`

- [ ] **Step 1: Run generate on the example**

```bash
flox activate -- go run ./cmd/agent-sandbox/ generate -C examples/local-coding/
```

Expected: succeeds, produces `.build/` directory.

- [ ] **Step 2: Verify schema.json was created**

```bash
cat examples/local-coding/.build/schema.json | python3 -m json.tool | head -30
```

Expected: valid JSON Schema with `properties.name`, `properties.runtime`, `properties.gateway`.

- [ ] **Step 3: Verify the schema validates the example config**

Quick sanity check — the schema's required fields (`name`, `runtime`) should match what's in the example yaml.

- [ ] **Step 4: Run full test suite one final time**

```bash
flox activate -- go build ./... && flox activate -- go test ./...
```

Expected: all pass, no lint errors.

- [ ] **Step 5: Final commit if any .build artifacts need gitignoring**

Check `.gitignore` includes `.build/` (it likely already does). If not:

```bash
echo ".build/" >> .gitignore
git add .gitignore
git commit -m "chore: ensure .build/ is gitignored"
```
