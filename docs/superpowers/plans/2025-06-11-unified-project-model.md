# Unified Project Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate fleet/single-agent branching by making every project a fleet (fleet.yaml + agent subdirectories).

**Architecture:** Unified `Project` type wraps all agents. Single loader (`LoadProject`), single generator (`RunProject`), single compose builder (`BuildProjectCompose`). Commands use `--agent` flag to target specific agents.

**Tech Stack:** Go 1.24+, cobra, yaml.v3, testify

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/config/project.go` | `Project`, `Agent` types, `LoadProject`, `ResolveAgent` |
| Create | `internal/config/project_test.go` | Tests for `LoadProject`, `ResolveAgent` |
| Modify | `internal/generate/v1/compose.go` | Add `BuildProjectCompose`, keep `buildAgentPair` |
| Modify | `internal/generate/v1/compose_test.go` | Replace `BuildCompose`/`BuildFleetCompose` tests with `BuildProjectCompose` tests |
| Modify | `internal/generate/v1/generator.go` | Add `RunProject`, remove `Run`/`RunWithConfig`/`RunFleet` |
| Modify | `internal/generate/v1/generator_test.go` | Update to use `RunProject` |
| Modify | `cmd/agent-sandbox-core/main.go` | Update `gateway-url` with `--agent`, update `init` to fleet-only |
| Modify | `cmd/agent-sandbox-core/generate.go` | Use `LoadProject` + `RunProject` |
| Modify | `cmd/agent-sandbox-core/audit.go` | Use `LoadProject` |
| Modify | `examples/local-coding/` | Restructure to fleet format |
| Modify | `examples/telegram/` | Restructure to fleet format |
| Remove | Old dead code after all tests pass |

---

### Task 1: Add `Project` and `Agent` types with `LoadProject`

**Files:**
- Create: `internal/config/project.go`
- Test: `internal/config/project_test.go`

- [ ] **Step 1: Write failing test for LoadProject with single agent**

Create `internal/config/project_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadProject_SingleAgent(t *testing.T) {
	dir := t.TempDir()

	// fleet.yaml with one agent
	fleetYAML := `agents:
  - my-agent
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

	// my-agent/agent.yaml
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "my-agent"), 0755))
	agentYAML := `name: my-agent
core_version: latest
runtime:
  image: "@builtin/codex"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "my-agent", "agent.yaml"), []byte(agentYAML), 0644))

	project, err := LoadProject(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, project.Dir)
	require.Len(t, project.Agents, 1)
	assert.Equal(t, "my-agent", project.Agents[0].Name)
	assert.Equal(t, filepath.Join(dir, "my-agent"), project.Agents[0].Dir)
	assert.Equal(t, "@builtin/codex", project.Agents[0].Config.Runtime.Image)
}

func TestLoadProject_MultipleAgents(t *testing.T) {
	dir := t.TempDir()

	fleetYAML := `agents:
  - coder
  - reviewer
shared:
  installations:
    - plugin: "@builtin/github-pat"
      options:
        token: "${GITHUB_PAT}"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

	for _, name := range []string{"coder", "reviewer"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0755))
		yaml := `name: ` + name + `
core_version: latest
runtime:
  image: "@builtin/codex"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "agent.yaml"), []byte(yaml), 0644))
	}

	project, err := LoadProject(dir)
	require.NoError(t, err)
	require.Len(t, project.Agents, 2)
	assert.Equal(t, "coder", project.Agents[0].Name)
	assert.Equal(t, "reviewer", project.Agents[1].Name)
	// Shared installations merged
	require.Len(t, project.Agents[0].Config.Installations, 1)
	assert.Equal(t, "@builtin/github-pat", project.Agents[0].Config.Installations[0].Plugin)
}

func TestLoadProject_NoFleetYAML(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadProject(dir)
	assert.ErrorContains(t, err, "fleet.yaml")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `flox activate -- go test ./internal/config/ -run TestLoadProject -v`
Expected: FAIL — `LoadProject` not defined

- [ ] **Step 3: Write implementation**

Create `internal/config/project.go`:

```go
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Project is the unified representation of any agent-sandbox project.
// Loaded from fleet.yaml + agent subdirectories.
type Project struct {
	Dir    string  // absolute path to project root
	Agents []Agent // always len >= 1
}

// Agent pairs a resolved config with its source directory.
type Agent struct {
	Name   string  // from Config.Name
	Dir    string  // absolute path to agent's directory
	Config *Config // fully resolved (shared merged, defaults applied)
}

// LoadProject loads fleet.yaml and all referenced agent configs from dir.
func LoadProject(dir string) (*Project, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve project dir: %w", err)
	}

	fleet, err := LoadFleet(absDir)
	if err != nil {
		return nil, err
	}

	var agents []Agent
	for _, agentName := range fleet.Agents {
		agentDir := filepath.Join(absDir, agentName)
		cfg, err := Load(agentDir)
		if err != nil {
			return nil, fmt.Errorf("loading agent %q: %w", agentName, err)
		}

		cfg.Installations = MergeInstallations(fleet.Shared.Installations, cfg.Installations)
		cfg.Gateway.Services = MergeGatewayServices(fleet.Shared.Gateway.Services, cfg.Gateway.Services)

		agents = append(agents, Agent{
			Name:   cfg.Name,
			Dir:    agentDir,
			Config: cfg,
		})
	}

	return &Project{Dir: absDir, Agents: agents}, nil
}

// ResolveAgent returns the targeted agent: uses explicit name if provided,
// otherwise returns the single agent or errors with available names.
func (p *Project) ResolveAgent(name string) (*Agent, error) {
	if name != "" {
		return p.AgentByName(name)
	}
	if len(p.Agents) == 1 {
		return &p.Agents[0], nil
	}
	names := make([]string, len(p.Agents))
	for i, a := range p.Agents {
		names[i] = a.Name
	}
	return nil, fmt.Errorf("multiple agents in project, use --agent to specify one of: %s",
		strings.Join(names, ", "))
}

// AgentByName returns the agent with the given name, or an error listing available agents.
func (p *Project) AgentByName(name string) (*Agent, error) {
	for i := range p.Agents {
		if p.Agents[i].Name == name {
			return &p.Agents[i], nil
		}
	}
	names := make([]string, len(p.Agents))
	for i, a := range p.Agents {
		names[i] = a.Name
	}
	return nil, fmt.Errorf("agent %q not found, available agents: %s",
		name, strings.Join(names, ", "))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `flox activate -- go test ./internal/config/ -run TestLoadProject -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/project.go internal/config/project_test.go
git commit -m "feat(config): add Project/Agent types and LoadProject"
```

### Task 2: Add `ResolveAgent` tests

**Files:**
- Modify: `internal/config/project_test.go`

- [ ] **Step 1: Write tests for ResolveAgent**

Append to `internal/config/project_test.go`:

```go
func TestResolveAgent_SingleAgent(t *testing.T) {
	project := &Project{
		Dir: "/tmp/test",
		Agents: []Agent{
			{Name: "solo", Dir: "/tmp/test/solo", Config: &Config{Name: "solo"}},
		},
	}

	// Empty name resolves to the only agent
	agent, err := project.ResolveAgent("")
	require.NoError(t, err)
	assert.Equal(t, "solo", agent.Name)

	// Explicit name also works
	agent, err = project.ResolveAgent("solo")
	require.NoError(t, err)
	assert.Equal(t, "solo", agent.Name)

	// Wrong name errors
	_, err = project.ResolveAgent("nonexistent")
	assert.ErrorContains(t, err, "not found")
	assert.ErrorContains(t, err, "solo")
}

func TestResolveAgent_MultipleAgents(t *testing.T) {
	project := &Project{
		Dir: "/tmp/test",
		Agents: []Agent{
			{Name: "coder", Dir: "/tmp/test/coder", Config: &Config{Name: "coder"}},
			{Name: "reviewer", Dir: "/tmp/test/reviewer", Config: &Config{Name: "reviewer"}},
		},
	}

	// Empty name errors with list
	_, err := project.ResolveAgent("")
	assert.ErrorContains(t, err, "multiple agents")
	assert.ErrorContains(t, err, "coder")
	assert.ErrorContains(t, err, "reviewer")

	// Explicit name works
	agent, err := project.ResolveAgent("coder")
	require.NoError(t, err)
	assert.Equal(t, "coder", agent.Name)

	agent, err = project.ResolveAgent("reviewer")
	require.NoError(t, err)
	assert.Equal(t, "reviewer", agent.Name)
}
```

- [ ] **Step 2: Run tests**

Run: `flox activate -- go test ./internal/config/ -run TestResolveAgent -v`
Expected: PASS (implementation already in project.go from Task 1)

- [ ] **Step 3: Commit**

```bash
git add internal/config/project_test.go
git commit -m "test(config): add ResolveAgent tests"
```

### Task 3: Add `BuildProjectCompose` (unified compose builder)

**Files:**
- Modify: `internal/generate/v1/compose.go`
- Modify: `internal/generate/v1/compose_test.go`

- [ ] **Step 1: Write failing test for BuildProjectCompose**

Add to `internal/generate/v1/compose_test.go`:

```go
func TestBuildProjectCompose_SingleAgent(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "my-agent",
				Runtime: config.RuntimeConfig{
					Image:   "@builtin/codex",
					Volumes: []string{"data:/opt/data"},
				},
			},
			Contribs: nil,
			BuildDir: "/project/.build/my-agent",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	// Agent and gateway services present
	assert.Contains(t, output, "my-agent:")
	assert.Contains(t, output, "my-agent-gateway:")

	// Gateway port always exposed
	var composed struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	err = yaml.Unmarshal([]byte(output), &composed)
	require.NoError(t, err)
	assert.Contains(t, composed.Services["my-agent-gateway"].Ports, "8080")

	// Network aliases use agent name
	assert.Contains(t, output, "my-agent-gateway")

	// Build paths are nested
	assert.Contains(t, output, ".build/my-agent/Dockerfile")
}

func TestBuildProjectCompose_MultipleAgents(t *testing.T) {
	agents := []ComposeAgentEntry{
		{
			Config: &config.Config{
				Name: "coder",
				Runtime: config.RuntimeConfig{Image: "@builtin/codex"},
			},
			Contribs: nil,
			BuildDir: "/project/.build/coder",
		},
		{
			Config: &config.Config{
				Name: "reviewer",
				Runtime: config.RuntimeConfig{Image: "@builtin/codex"},
			},
			Contribs: nil,
			BuildDir: "/project/.build/reviewer",
		},
	}

	output, err := BuildProjectCompose(agents, "/project")
	require.NoError(t, err)

	assert.Contains(t, output, "coder:")
	assert.Contains(t, output, "coder-gateway:")
	assert.Contains(t, output, "reviewer:")
	assert.Contains(t, output, "reviewer-gateway:")

	// Both gateways expose port
	var composed struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	err = yaml.Unmarshal([]byte(output), &composed)
	require.NoError(t, err)
	assert.Contains(t, composed.Services["coder-gateway"].Ports, "8080")
	assert.Contains(t, composed.Services["reviewer-gateway"].Ports, "8080")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `flox activate -- go test ./internal/generate/v1/ -run TestBuildProjectCompose -v`
Expected: FAIL — `BuildProjectCompose` not defined

- [ ] **Step 3: Implement BuildProjectCompose**

First, modify `buildAgentPair` in `internal/generate/v1/compose.go` to expose gateway port unconditionally when `exposeGateway` is true. Change line 146:

```go
// OLD:
// Expose gateway HTTP port when plugin routes are registered (e.g. OAuth callbacks)
if p.exposeGateway && contribs != nil && len(contribs.Gateway.Routes) > 0 {
    gatewaySvc["ports"] = []string{"8080"}
}

// NEW:
// Expose gateway HTTP port for port discovery via `docker compose port`.
if p.exposeGateway {
    gatewaySvc["ports"] = []string{"8080"}
}
```

Then add `BuildProjectCompose` to `internal/generate/v1/compose.go`:

```go
// BuildProjectCompose generates a unified docker-compose.yml for any project (1 or N agents).
// Gateway port is always exposed for port discovery via `docker compose port`.
func BuildProjectCompose(agents []ComposeAgentEntry, projectDir string) (string, error) {
	compose := composeFile{
		Services: map[string]any{},
		Volumes:  map[string]any{},
		Networks: map[string]any{"sandbox": map[string]any{"driver": "bridge"}},
	}

	for _, agent := range agents {
		cfg := agent.Config
		agentName := cfg.Name
		gatewayName := cfg.Name + "-gateway"
		certsVolume := agentName + "-certs"

		relBuildDir, err := filepath.Rel(filepath.Join(projectDir, ".build"), agent.BuildDir)
		if err != nil {
			relBuildDir = agent.BuildDir
		}

		composeDir := filepath.Join(projectDir, ".build")

		pair := buildAgentPair(agentPairParams{
			cfg:          cfg,
			contribs:     agent.Contribs,
			agentName:    agentName,
			gatewayName:  gatewayName,
			agentAlias:   agentName,
			gatewayAlias: gatewayName,
			certsVolume:  certsVolume,
			agentBuild: map[string]any{
				"context":    "..",
				"dockerfile": filepath.Join(".build", relBuildDir, "Dockerfile"),
			},
			gatewayBuild: map[string]any{
				"context":    fmt.Sprintf("./%s/gateway", relBuildDir),
				"dockerfile": "Dockerfile",
			},
			gatewayVolumes: []string{
				certsVolume + ":/shared/certs",
				fmt.Sprintf("./%s/config.yaml:/etc/gateway/config.yaml:ro", relBuildDir),
			},
			sidecarPrefix: agentName,
			buildDir:      composeDir,
			exposeGateway: true,
		})

		maps.Copy(compose.Services, pair.services)
		maps.Copy(compose.Volumes, pair.volumes)
	}

	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("marshal compose: %w", err)
	}
	return string(data), nil
}
```

- [ ] **Step 4: Run tests**

Run: `flox activate -- go test ./internal/generate/v1/ -run TestBuildProjectCompose -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/compose.go internal/generate/v1/compose_test.go
git commit -m "feat(generate): add BuildProjectCompose unified compose builder"
```

### Task 4: Add `RunProject` to generator

**Files:**
- Modify: `internal/generate/v1/generator.go`
- Modify: `internal/generate/v1/generator_test.go`

- [ ] **Step 1: Write failing test for RunProject**

Add to `internal/generate/v1/generator_test.go`:

```go
func TestGenerator_RunProject(t *testing.T) {
	projectDir := t.TempDir()

	// Create two agent directories
	for _, name := range []string{"alpha", "beta"} {
		agentDir := filepath.Join(projectDir, name)
		require.NoError(t, os.MkdirAll(agentDir, 0755))
		agentYAML := fmt.Sprintf(`
name: %s
core_version: latest
runtime:
  image: "@builtin/codex"
gateway:
  services:
    - url: https://api.example.com
      headers:
        Authorization: Bearer ${TOKEN}
`, name)
		require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))
	}

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "alpha", Dir: filepath.Join(projectDir, "alpha"), Config: mustParseConfig(t, filepath.Join(projectDir, "alpha", "agent.yaml"))},
			{Name: "beta", Dir: filepath.Join(projectDir, "beta"), Config: mustParseConfig(t, filepath.Join(projectDir, "beta", "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	g.SetPresets(testPresets)
	require.NoError(t, g.RunProject(project))

	buildDir := filepath.Join(projectDir, ".build")

	// Verify nested structure
	for _, name := range []string{"alpha", "beta"} {
		assert.FileExists(t, filepath.Join(buildDir, name, "Dockerfile"))
		assert.FileExists(t, filepath.Join(buildDir, name, "entrypoint.sh"))
		assert.FileExists(t, filepath.Join(buildDir, name, "config.yaml"))
		assert.FileExists(t, filepath.Join(buildDir, name, "gateway", "config.yaml"))
	}

	// Verify unified compose
	assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yml"))
	composeData, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(composeData), "alpha:")
	assert.Contains(t, string(composeData), "alpha-gateway:")
	assert.Contains(t, string(composeData), "beta:")
	assert.Contains(t, string(composeData), "beta-gateway:")
}

func TestGenerator_RunProject_SingleAgent(t *testing.T) {
	projectDir := t.TempDir()

	agentDir := filepath.Join(projectDir, "solo")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	agentYAML := `
name: solo
core_version: latest
runtime:
  image: "@builtin/codex"
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "solo", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	g.SetPresets(testPresets)
	require.NoError(t, g.RunProject(project))

	buildDir := filepath.Join(projectDir, ".build")

	// Still nested even for single agent
	assert.FileExists(t, filepath.Join(buildDir, "solo", "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "solo", "gateway", "config.yaml"))
	assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yml"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `flox activate -- go test ./internal/generate/v1/ -run TestGenerator_RunProject -v`
Expected: FAIL — `RunProject` not defined

- [ ] **Step 3: Implement RunProject**

Add to `internal/generate/v1/generator.go`:

```go
// RunProject executes the full generation pipeline for any project.
func (g *Generator) RunProject(project *config.Project) error {
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	var entries []ComposeAgentEntry
	for _, agent := range project.Agents {
		agentBuildDir := filepath.Join(buildDir, agent.Name)
		if err := os.MkdirAll(agentBuildDir, 0755); err != nil {
			return fmt.Errorf("create build dir for %s: %w", agent.Name, err)
		}

		result, err := g.generateAgent(agent.Config, agent.Dir, agentBuildDir)
		if err != nil {
			return fmt.Errorf("generate %s: %w", agent.Name, err)
		}

		if err := g.writeGatewayBuild(agentBuildDir, result.Config, result.Contribs, result.Resolved); err != nil {
			return fmt.Errorf("write gateway build for %s: %w", agent.Name, err)
		}

		entries = append(entries, ComposeAgentEntry{
			Config:   result.Config,
			Contribs: result.Contribs,
			BuildDir: agentBuildDir,
		})
	}

	compose, err := BuildProjectCompose(entries, g.projectDir)
	if err != nil {
		return fmt.Errorf("build compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}
	if err := g.copyGatewayTypes(buildDir); err != nil {
		return fmt.Errorf("copy gateway types: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `flox activate -- go test ./internal/generate/v1/ -run TestGenerator_RunProject -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/generate/v1/generator.go internal/generate/v1/generator_test.go
git commit -m "feat(generate): add RunProject unified generation pipeline"
```

### Task 5: Update `generate` command to use `LoadProject` + `RunProject`

**Files:**
- Modify: `cmd/agent-sandbox-core/generate.go`

- [ ] **Step 1: Rewrite generate.go**

Replace the entire `generateCmd` RunE and remove `generateSingleAgent`/`generateFleet`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/dotenv"
	v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
	"github.com/spf13/cobra"
)

func generateCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate build artifacts from fleet.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			coreDir := coreRoot
			if coreDir == "." {
				fmt.Fprintf(os.Stderr, "Warning: could not detect core root from binary location.\n")
			}

			dotenv.Load(filepath.Join(projectDir, ".env"))

			project, err := config.LoadProject(projectDir)
			if err != nil {
				return err
			}

			g := v1.NewGeneratorWithCore(projectDir, coreDir)
			if err := g.RunProject(project); err != nil {
				return err
			}

			_ = ensureSchemaComment(filepath.Join(projectDir, "fleet.yaml"), ".build/fleet-schema.json")
			for _, agent := range project.Agents {
				agentYAML := filepath.Join(agent.Dir, "agent.yaml")
				relSchema, relErr := filepath.Rel(agent.Dir, filepath.Join(projectDir, ".build", "schema.json"))
				if relErr != nil {
					relSchema = ".build/schema.json"
				}
				_ = ensureSchemaComment(agentYAML, relSchema)
			}

			fmt.Fprintf(os.Stderr, "Generated .build/ for %d agent(s) in %s\n", len(project.Agents), projectDir)
			return nil
		},
	}
	return cmd
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `flox activate -- go build ./cmd/agent-sandbox-core/`
Expected: Compiles without errors

- [ ] **Step 3: Run all tests**

Run: `flox activate -- go test ./...`
Expected: PASS (some old tests may need updating — that's Task 8)

- [ ] **Step 4: Commit**

```bash
git add cmd/agent-sandbox-core/generate.go
git commit -m "refactor(cmd): generate uses LoadProject + RunProject"
```

### Task 6: Update `gateway-url` command with `--agent` flag

**Files:**
- Modify: `cmd/agent-sandbox-core/main.go` (lines 122-174)

- [ ] **Step 1: Rewrite gatewayURLCmd**

First, update `runtimeBinary` and `loadConfigSafe` to work with the new project model. Since all agents in a project share the same container runtime (you can't mix docker and podman in one compose file), use the first agent's config:

```go
// runtimeBinary determines the container runtime CLI to use.
// Priority: AGENT_SANDBOX_RUNTIME env var > first agent's runtime_engine > "docker"
func runtimeBinary(dir string) string {
	if rt := os.Getenv("AGENT_SANDBOX_RUNTIME"); rt != "" {
		return rt
	}
	project, err := config.LoadProject(dir)
	if err == nil && len(project.Agents) > 0 && project.Agents[0].Config.RuntimeEngine != "" {
		return project.Agents[0].Config.RuntimeEngineBinary()
	}
	return "docker"
}
```

Remove the `loadConfigSafe` function (no longer needed).

Then replace the `gatewayURLCmd` function:

```go
func gatewayURLCmd(dir *string) *cobra.Command {
	var agentName string
	cmd := &cobra.Command{
		Use:   "gateway-url",
		Short: "Print the gateway's public URL (resolves dynamic port)",
		RunE: func(cmd *cobra.Command, args []string) error {
			composePath := filepath.Join(*dir, ".build", "docker-compose.yml")
			if _, err := os.Stat(composePath); os.IsNotExist(err) {
				return fmt.Errorf("%s not found — run 'agent-sandbox generate' first", composePath)
			}

			absDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve project dir: %w", err)
			}
			projectName := filepath.Base(absDir)

			project, err := config.LoadProject(*dir)
			if err != nil {
				return fmt.Errorf("load project: %w", err)
			}

			agent, err := project.ResolveAgent(agentName)
			if err != nil {
				return err
			}

			gatewayService := agent.Name + "-gateway"

			runtime := runtimeBinary(*dir)
			c := exec.Command(runtime, "compose",
				"-f", composePath,
				"--project-name", projectName,
				"port", gatewayService, "8080",
			)
			out, err := c.Output()
			if err != nil {
				return fmt.Errorf("gateway not running or port not exposed — is 'agent-sandbox compose up' running?")
			}

			hostPort := strings.TrimSpace(string(out))
			if hostPort == "" {
				return fmt.Errorf("could not resolve gateway port")
			}

			hostPort = strings.Replace(hostPort, "0.0.0.0:", "localhost:", 1)
			if strings.HasPrefix(hostPort, ":::") {
				hostPort = "localhost:" + strings.TrimPrefix(hostPort, ":::")
			}

			fmt.Printf("http://%s\n", hostPort)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "Agent name (required for multi-agent projects)")
	return cmd
}
```

- [ ] **Step 2: Verify compilation**

Run: `flox activate -- go build ./cmd/agent-sandbox-core/`
Expected: Compiles without errors

- [ ] **Step 3: Commit**

```bash
git add cmd/agent-sandbox-core/main.go
git commit -m "feat(cmd): gateway-url supports --agent flag via LoadProject"
```

### Task 7: Update `audit` command to use `LoadProject`

**Files:**
- Modify: `cmd/agent-sandbox-core/audit.go`

- [ ] **Step 1: Rewrite `runAudit` to use LoadProject**

Replace the fleet/single branching in `runAudit` (lines 43-68) with unified logic:

```go
func runAudit(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}

	project, err := config.LoadProject(absDir)
	if err != nil {
		return err
	}

	var allResults []auditResult
	for _, agent := range project.Agents {
		results := auditAgent(agent.Config)
		if len(project.Agents) > 1 {
			fmt.Fprintf(os.Stderr, "\n=== Agent: %s ===\n", agent.Name)
		}
		printResults(results)
		allResults = append(allResults, results...)
	}

	if len(project.Agents) > 1 {
		fmt.Fprintf(os.Stderr, "\n=== Summary ===\n")
		printSummary(allResults)
	}

	for _, r := range allResults {
		if r.severity == "error" {
			return fmt.Errorf("audit failed with errors")
		}
	}
	return nil
}
```

- [ ] **Step 2: Remove old `config.Load` / `config.LoadFleet` calls from audit.go**

Remove the try-single-then-fleet pattern. The function now only uses `config.LoadProject`.

- [ ] **Step 3: Verify compilation**

Run: `flox activate -- go build ./cmd/agent-sandbox-core/`
Expected: Compiles without errors

- [ ] **Step 4: Commit**

```bash
git add cmd/agent-sandbox-core/audit.go
git commit -m "refactor(cmd): audit uses LoadProject"
```

### Task 8: Update `init` command to always generate fleet structure

**Files:**
- Modify: `cmd/agent-sandbox-core/main.go` (lines 195-316)

- [ ] **Step 1: Rewrite initCmd to remove single-agent path**

Replace `initCmd`, `initSingleAgent`, and `initFleet` with a unified `initCmd` that always produces fleet structure:

```go
func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new agent-sandbox project (interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat("fleet.yaml"); err == nil {
				return fmt.Errorf("fleet.yaml already exists in this directory")
			}

			reader := bufio.NewReader(os.Stdin)

			agentCountStr := prompt(reader, "How many agents? [1]: ")
			agentCount := 1
			if agentCountStr != "" {
				if _, err := fmt.Sscanf(agentCountStr, "%d", &agentCount); err != nil || agentCount < 1 {
					return fmt.Errorf("invalid agent count: %q (must be a positive integer)", agentCountStr)
				}
			}

			rt := selectRuntime(reader)

			// Determine agent names
			var agentNames []string
			if agentCount == 1 {
				dirName := filepath.Base(mustCwd())
				name := prompt(reader, fmt.Sprintf("Agent name [%s]: ", dirName))
				if name == "" {
					name = dirName
				}
				agentNames = []string{name}
			} else {
				for i := 1; i <= agentCount; i++ {
					agentNames = append(agentNames, fmt.Sprintf("agent-%03d", i))
				}
			}

			// Write fleet.yaml
			var fleet strings.Builder
			fleet.WriteString("# yaml-language-server: $schema=.build/fleet-schema.json\n")
			fleet.WriteString("agents:\n")
			for _, name := range agentNames {
				fmt.Fprintf(&fleet, "  - %s\n", name)
			}
			fleet.WriteString("\nshared:\n")
			fleet.WriteString("  gateway:\n")
			fleet.WriteString("    services: []\n")
			fleet.WriteString("  installations: []\n")

			if err := os.WriteFile("fleet.yaml", []byte(fleet.String()), 0644); err != nil {
				return fmt.Errorf("writing fleet.yaml: %w", err)
			}

			// Write per-agent directories
			for _, name := range agentNames {
				if err := os.MkdirAll(name, 0755); err != nil {
					return fmt.Errorf("creating %s/: %w", name, err)
				}

				var agent strings.Builder
				agent.WriteString("# yaml-language-server: $schema=../.build/schema.json\n")
				fmt.Fprintf(&agent, "name: %s\n", name)
				fmt.Fprintf(&agent, "core_version: %s\n", coreVersionForInit())
				agent.WriteString("runtime:\n")
				fmt.Fprintf(&agent, "  image: \"@builtin/%s\"\n", rt)
				agent.WriteString("  entrypoint: [\"sleep\", \"infinity\"]\n")
				agent.WriteString("gateway:\n")
				agent.WriteString("  services: []\n")
				agent.WriteString("installations: []\n")

				agentPath := filepath.Join(name, "agent.yaml")
				if err := os.WriteFile(agentPath, []byte(agent.String()), 0644); err != nil {
					return fmt.Errorf("writing %s: %w", agentPath, err)
				}
			}

			// Write .env.example
			if err := os.WriteFile(".env.example", []byte("# Shared secrets\n"), 0644); err != nil {
				return fmt.Errorf("writing .env.example: %w", err)
			}

			fmt.Printf("\nCreated fleet.yaml with %d agent(s)\n", agentCount)
			for _, name := range agentNames {
				fmt.Printf("  %s/agent.yaml\n", name)
			}
			fmt.Println("\nNext steps:")
			fmt.Println("  1. Add gateway services and plugins")
			fmt.Println("  2. Create .env with your secrets")
			fmt.Println("  3. agent-sandbox generate")
			fmt.Println("  4. agent-sandbox compose up --build -d")
			return nil
		},
	}
}
```

- [ ] **Step 2: Remove `initSingleAgent` and old `initFleet` functions**

Delete the `initSingleAgent` function (lines 227-258) and the old `initFleet` function (lines 260-316). Also remove the `agent.yaml` existence check from the guard (only check `fleet.yaml`).

- [ ] **Step 3: Verify compilation**

Run: `flox activate -- go build ./cmd/agent-sandbox-core/`
Expected: Compiles without errors

- [ ] **Step 4: Commit**

```bash
git add cmd/agent-sandbox-core/main.go
git commit -m "refactor(cmd): init always generates fleet structure"
```

### Task 9: Migrate examples to fleet structure

**Files:**
- Modify: `examples/local-coding/` → restructure
- Modify: `examples/telegram/` → restructure

- [ ] **Step 1: Restructure `examples/local-coding/`**

Create fleet structure:

```
examples/local-coding/
  fleet.yaml
  coder/
    agent.yaml       ← moved from examples/local-coding/agent.yaml
```

Create `examples/local-coding/fleet.yaml`:

```yaml
# yaml-language-server: $schema=.build/fleet-schema.json
agents:
  - coder

shared:
  gateway:
    services: []
  installations: []
```

Move `examples/local-coding/agent.yaml` to `examples/local-coding/coder/agent.yaml`. Update the schema comment path:

```yaml
# yaml-language-server: $schema=../.build/schema.json
name: coder
core_version: latest
log_level: debug
runtime:
  image: "@builtin/claude-code"
  entrypoint: ["sleep", "infinity"]
gateway:
  services:
    - url: https://agent-gateway.stx-ai.net
      headers:
        Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}
installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "./home"
      volume: true
  - plugin: "@builtin/mcp-oauth"
    options:
      providers:
        notion:
          mcp_url: https://mcp.notion.com/mcp
```

- [ ] **Step 2: Restructure `examples/telegram/`**

Create fleet structure:

```
examples/telegram/
  fleet.yaml
  telegram-agent/
    agent.yaml       ← moved from examples/telegram/agent.yaml
    plugins/         ← moved from examples/telegram/plugins/
```

Create `examples/telegram/fleet.yaml`:

```yaml
# yaml-language-server: $schema=.build/fleet-schema.json
agents:
  - telegram-agent

shared:
  gateway:
    services: []
  installations: []
```

Move `examples/telegram/agent.yaml` to `examples/telegram/telegram-agent/agent.yaml`. Update schema comment and local plugin path:

```yaml
# yaml-language-server: $schema=../.build/schema.json
name: telegram-agent
core_version: latest
log_level: debug

runtime:
  image: "@builtin/codex"
  extra_builds:
    - "ENV OPENAI_API_KEY=gateway-managed"
  entrypoint: ["node", "/opt/agent-manager/dist/index.js"]

gateway:
  services:
    - url: https://agent-gateway.stx-ai.net
      headers:
        Authorization: Bearer ${STX_LLM_GATEWAY_API_KEY}

installations:
  - plugin: "@builtin/home-override"
    options:
      home_directory: "./home"
      volume: true

  - plugin: "@builtin/agent-manager-acp"
    options:
      acp_command: ["codex-acp"]
      acp_install: "npm install -g @zed-industries/codex-acp@0.15.0"

  - plugin: ./plugins/telegram
    options:
      bot_token: "${TELEGRAM_BOT_TOKEN}"
      allowed_users:
        - "@${TELEGRAM_USERNAME}"
```

- [ ] **Step 3: Verify examples generate correctly**

Run: `flox activate -- go run ./cmd/agent-sandbox-core/ -C examples/local-coding generate`
Run: `flox activate -- go run ./cmd/agent-sandbox-core/ -C examples/multi-agent generate`
Expected: Both generate `.build/` with nested structure

- [ ] **Step 4: Remove old `.build/` directories from examples if tracked**

```bash
rm -rf examples/local-coding/.build examples/telegram/.build examples/multi-agent/.build
```

- [ ] **Step 5: Commit**

```bash
git add examples/
git commit -m "refactor(examples): migrate all examples to fleet structure"
```

### Task 10: Update existing tests and remove dead code

**Files:**
- Modify: `internal/generate/v1/generator_test.go`
- Modify: `internal/generate/v1/compose_test.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/generate/v1/generator.go`
- Modify: `internal/generate/v1/compose.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Update generator_test.go — replace old tests with RunProject equivalents**

Remove `TestGenerator_Contracts_Fleet` (uses `RunFleet`) and `TestGenerator_Contracts_SingleAgent` (uses `RunWithConfig`). These are replaced by `TestGenerator_RunProject` and `TestGenerator_RunProject_SingleAgent` from Task 4.

Also update any other tests that call `Run()` or `RunWithConfig()` to use `RunProject()` with a `config.Project` wrapper.

For tests that used the single-agent `Run()` pattern:

```go
// OLD:
g := NewGenerator(projectDir, nil)
require.NoError(t, g.Run())

// NEW:
project, err := config.LoadProject(projectDir)
require.NoError(t, err)
g := NewGenerator(projectDir, nil)
require.NoError(t, g.RunProject(project))
```

For tests that set up a single `agent.yaml` at project root, restructure the test setup to use fleet format (fleet.yaml + agent subdir).

- [ ] **Step 2: Update compose_test.go — replace BuildCompose/BuildFleetCompose tests**

Tests calling `BuildCompose` should be rewritten to use `BuildProjectCompose` with a single `ComposeAgentEntry`. Tests calling `BuildFleetCompose` should switch to `BuildProjectCompose`. The test logic stays the same — only the function call changes:

```go
// OLD:
output, err := BuildCompose(cfg, contribs, "/project")

// NEW (single agent):
agents := []ComposeAgentEntry{{
    Config:   cfg,
    Contribs: contribs,
    BuildDir: "/project/.build/" + cfg.Name,
}}
output, err := BuildProjectCompose(agents, "/project")
```

```go
// OLD:
output, err := BuildFleetCompose(agents, "/project")

// NEW:
output, err := BuildProjectCompose(agents, "/project")
```

Update assertions: the old `BuildCompose` used `"certs"` as volume name and `".build/Dockerfile"` as dockerfile path. The new unified builder uses `"<name>-certs"` and `.build/<name>/Dockerfile`. Update assertions accordingly.

- [ ] **Step 3: Update config_test.go — TestLoadFleetAgents still valid**

`TestLoadFleetAgents` and related tests remain valid since `LoadFleetAgents` is still used internally. No changes needed to these tests, but add a note that `LoadFleetAgents` will be removed once `LoadProject` fully replaces it.

Actually — since `LoadProject` replaces `LoadFleetAgents`, update `TestLoadFleetAgents` to test `LoadProject` instead:

```go
// OLD:
fleet, agents, err := LoadFleetAgents(dir)
require.NoError(t, err)
assert.Len(t, fleet.Agents, 2)
require.Len(t, agents, 2)
assert.Equal(t, "coder", agents[0].Config.Name)

// NEW:
project, err := LoadProject(dir)
require.NoError(t, err)
require.Len(t, project.Agents, 2)
assert.Equal(t, "coder", project.Agents[0].Name)
assert.Equal(t, "coder", project.Agents[0].Config.Name)
```

- [ ] **Step 4: Run full test suite**

Run: `flox activate -- go test ./...`
Expected: All tests PASS

- [ ] **Step 5: Remove dead code from generator.go**

Remove from `internal/generate/v1/generator.go`:
- `func (g *Generator) Run() error`
- `func (g *Generator) RunWithConfig(cfg *config.Config, agentDir string) error`
- `func (g *Generator) RunFleet(agents []config.FleetAgent) error`

- [ ] **Step 6: Remove dead code from compose.go**

Remove from `internal/generate/v1/compose.go`:
- `func BuildCompose(cfg *config.Config, contribs *plugin.Contributions, projectDir string) (string, error)`
- `func BuildFleetCompose(agents []ComposeAgentEntry, projectDir string) (string, error)`

- [ ] **Step 7: Remove dead code from config.go**

Remove from `internal/config/config.go`:
- `func LoadFleetAgents(dir string) (*FleetConfig, []FleetAgent, error)`
- `type FleetAgent struct` (replaced by `Agent` in project.go)

Keep `LoadFleet` (used by `LoadProject`) and `Load` (used per-agent by `LoadProject`).

- [ ] **Step 8: Run full test suite again**

Run: `flox activate -- go test ./...`
Expected: All tests PASS

- [ ] **Step 9: Run linter**

Run: `flox activate -- golangci-lint run ./...`
Expected: No errors (may have warnings about unused params — fix those)

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "refactor: remove dead code (BuildCompose, BuildFleetCompose, RunWithConfig, RunFleet, LoadFleetAgents)"
```
