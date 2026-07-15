package v1

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGenerator_Run(t *testing.T) {
	projectDir := t.TempDir()

	// Write a local plugin inside the agent subdirectory
	agentDir := filepath.Join(projectDir, "test-agent")
	pluginDir := filepath.Join(agentDir, "plugins", "my-tool")
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
      - "RUN npm install -g my-tool@{{ .plugin.options.version }}"
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(pluginYAML), 0644))

	// Write agent.yaml in the agent subdirectory
	agentYAML := `
name: test-agent
core_version: latest
log_level: debug
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/my-tool
    options:
      version: "2.0.0"
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	g.SetPresets(testPresets)
	require.NoError(t, g.RunProject(project))

	// Verify outputs (nested layout)
	buildDir := filepath.Join(projectDir, ".build")
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yml"))

	// Check Dockerfile content
	df, err := os.ReadFile(filepath.Join(buildDir, "test-agent", "Dockerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(df), "FROM node:24-slim")
	assert.Contains(t, string(df), "npm install -g my-tool@2.0.0")
	assert.Contains(t, string(df), `CMD ["sleep","infinity"]`)

	// Check compose content
	comp, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(comp), "test-agent:")
	assert.Contains(t, string(comp), "test-agent-gateway:")
}

func TestGenerator_UsesLocalCore(t *testing.T) {
	projectDir := t.TempDir()
	coreDir := t.TempDir()

	// Create a bundled plugin in the core directory
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
    egress:
      - hosts: ["github.com"]
        headers:
          Authorization: "Bearer {{ .plugin.options.token }}"
`), 0644))

	// Create gateway source in core directory
	gatewayDir := filepath.Join(coreDir, "gateway", "cmd", "gateway")
	require.NoError(t, os.MkdirAll(gatewayDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gatewayDir, "main.go"), []byte("package main\n"), 0644))

	// Create go.mod in coreDir (simulates self-contained core distribution)
	require.NoError(t, os.WriteFile(filepath.Join(coreDir, "go.mod"), []byte("module github.com/donbader/agent-sandbox\n\ngo 1.26\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(coreDir, "scripts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(coreDir, "scripts", "gateway-route.sh"), []byte("#!/bin/sh\n"), 0755))

	// Agent lives in a subdirectory
	agentDir := filepath.Join(projectDir, "test-agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	agentYAML := `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "ghp_test123"
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGeneratorWithCore(projectDir, coreDir)
	require.NoError(t, g.RunProject(project))

	buildDir := filepath.Join(projectDir, ".build")
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yml"))
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "gateway", "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "gateway", "config.yaml"))
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "gateway", "plugins.yaml"))
}

func TestGenerator_Run_WithSidecar(t *testing.T) {
	projectDir := t.TempDir()

	// Plugin that contributes a sidecar (in agent subdirectory)
	agentDir := filepath.Join(projectDir, "test-agent")
	pluginDir := filepath.Join(agentDir, "plugins", "my-sidecar")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))
	pluginYAML := `
name: my-sidecar
options:
  port:
    type: string
    default: "3000"
contributes:
  sidecar:
    services:
      mysvc:
        image: "myimage:latest"
        environment:
          PORT: "{{ .plugin.options.port }}"
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(pluginYAML), 0644))

	agentYAML := `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/my-sidecar
    options:
      port: "8080"
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	require.NoError(t, g.RunProject(project))

	buildDir := filepath.Join(projectDir, ".build")
	comp, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(comp), "test-agent-mysvc:")
	assert.Contains(t, string(comp), "PORT")
	assert.Contains(t, string(comp), "8080")
}

func TestGenerator_Run_RequiresUnsatisfied(t *testing.T) {
	projectDir := t.TempDir()

	// Plugin that declares a requires dependency (in agent subdirectory)
	agentDir := filepath.Join(projectDir, "test-agent")
	pluginDir := filepath.Join(agentDir, "plugins", "my-channel")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
name: my-channel
requires:
  - "@builtin/agent-manager-acp"
contributes:
  runtime:
    extra_builds:
      - "RUN echo channel"
`), 0644))

	agentYAML := `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/my-channel
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	err := g.RunProject(project)
	require.Error(t, err)
	assert.ErrorContains(t, err, "requires \"@builtin/agent-manager-acp\"")
}

func TestGenerator_Run_RequiresSatisfied(t *testing.T) {
	projectDir := t.TempDir()

	// Agent subdirectory with plugins
	agentDir := filepath.Join(projectDir, "test-agent")

	// "dep" plugin (simulates agent-manager-acp)
	depDir := filepath.Join(agentDir, "plugins", "agent-manager-acp")
	require.NoError(t, os.MkdirAll(depDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(depDir, "plugin.yaml"), []byte(`
name: agent-manager-acp
contributes:
  runtime:
    extra_builds:
      - "RUN echo manager"
`), 0644))

	// Plugin that requires agent-manager-acp
	channelDir := filepath.Join(agentDir, "plugins", "my-channel")
	require.NoError(t, os.MkdirAll(channelDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "plugin.yaml"), []byte(`
name: my-channel
requires:
  - "./plugins/agent-manager-acp"
contributes:
  runtime:
    extra_builds:
      - "RUN echo channel"
`), 0644))

	agentYAML := `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/agent-manager-acp
  - plugin: ./plugins/my-channel
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	require.NoError(t, g.RunProject(project))
}

func TestGenerator_Run_BundledPluginAssets(t *testing.T) {
	projectDir := t.TempDir()
	coreDir := t.TempDir()

	// Create a bundled plugin with an asset directory
	pluginDir := filepath.Join(coreDir, "plugins", "my-bundled")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
name: my-bundled
assets:
  - my-src/
contributes:
  runtime:
    extra_builds:
      - "COPY {{ asset \"my-src\" }}/ /opt/my-src/"
      - "RUN echo built"
`), 0644))

	// Create the asset directory
	assetDir := filepath.Join(pluginDir, "my-src")
	require.NoError(t, os.MkdirAll(assetDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(assetDir, "main.ts"), []byte("console.log('hello')"), 0644))

	// Create gateway source
	gatewayDir := filepath.Join(coreDir, "gateway", "cmd", "gateway")
	require.NoError(t, os.MkdirAll(gatewayDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gatewayDir, "main.go"), []byte("package main\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(coreDir, "go.mod"), []byte("module test\n\ngo 1.26\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(coreDir, "scripts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(coreDir, "scripts", "gateway-route.sh"), []byte("#!/bin/sh\n"), 0755))

	// Agent lives in a subdirectory
	agentDir := filepath.Join(projectDir, "test-agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	agentYAML := `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: "@builtin/my-bundled"
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGeneratorWithCore(projectDir, coreDir)
	require.NoError(t, g.RunProject(project))

	// Verify asset was extracted to .build/test-agent/plugins/my-bundled/my-src/
	extractedFile := filepath.Join(projectDir, ".build", "test-agent", "plugins", "my-bundled", "my-src", "main.ts")
	assert.FileExists(t, extractedFile)

	// Verify Dockerfile references the extracted path
	df, err := os.ReadFile(filepath.Join(projectDir, ".build", "test-agent", "Dockerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(df), "COPY .build/test-agent/plugins/my-bundled/my-src/ /opt/my-src/")
}

// TestGenerator_Contracts_SingleAgent verifies that generated artifacts are internally consistent:
// - All bind-mount sources referenced in compose exist as files
// - GATEWAY_HOST env var matches the gateway network alias
// - Dockerfile has a CMD
func TestGenerator_Contracts_SingleAgent(t *testing.T) {
	projectDir := t.TempDir()

	agentDir := filepath.Join(projectDir, "test-agent")
	require.NoError(t, os.MkdirAll(agentDir, 0755))
	agentYAML := `
name: test-agent
core_version: latest
runtime:
  image: "@builtin/codex"
`
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "test-agent", Dir: agentDir, Config: mustParseConfig(t, filepath.Join(agentDir, "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	g.SetPresets(testPresets)
	require.NoError(t, g.RunProject(project))

	buildDir := filepath.Join(projectDir, ".build")

	// Parse compose to extract volume mounts and env vars
	composeData, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)

	var compose composeFile
	require.NoError(t, yaml.Unmarshal(composeData, &compose))

	// Verify GATEWAY_HOST matches the gateway alias (in project mode, alias = service name)
	agentSvcRaw, ok := compose.Services["test-agent"].(map[string]any)
	require.True(t, ok, "test-agent service not found or wrong type")
	envRaw, ok := agentSvcRaw["environment"].(map[string]any)
	require.True(t, ok, "environment not found or wrong type")
	assert.Equal(t, "test-agent-gateway", envRaw["GATEWAY_HOST"])

	// Verify gateway alias matches
	gatewaySvcRaw, ok := compose.Services["test-agent-gateway"].(map[string]any)
	require.True(t, ok, "test-agent-gateway service not found or wrong type")
	networks, ok := gatewaySvcRaw["networks"].(map[string]any)
	require.True(t, ok)
	sandbox, ok := networks["sandbox"].(map[string]any)
	require.True(t, ok)
	aliases, ok := sandbox["aliases"].([]any)
	require.True(t, ok)
	assert.Contains(t, aliases, "test-agent-gateway")

	// Verify Dockerfile has CMD
	df, err := os.ReadFile(filepath.Join(buildDir, "test-agent", "Dockerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(df), "CMD")

	// Verify gateway/config.yaml exists (Docker COPY target)
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "gateway", "config.yaml"))

	// Verify runtime config.yaml exists (for potential volume mount)
	assert.FileExists(t, filepath.Join(buildDir, "test-agent", "config.yaml"))
}

// TestGenerator_Contracts_Fleet verifies internal consistency of fleet-mode generation.
func TestGenerator_Contracts_Fleet(t *testing.T) {
	projectDir := t.TempDir()

	// Create two agent directories
	for _, name := range []string{"coder", "reviewer"} {
		agentDir := filepath.Join(projectDir, name)
		require.NoError(t, os.MkdirAll(agentDir, 0755))
		agentYAML := fmt.Sprintf(`
name: %s
core_version: latest
runtime:
  image: "@builtin/codex"
`, name)
		require.NoError(t, os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(agentYAML), 0644))
	}

	project := &config.Project{
		Dir: projectDir,
		Agents: []config.Agent{
			{Name: "coder", Dir: filepath.Join(projectDir, "coder"), Config: mustParseConfig(t, filepath.Join(projectDir, "coder", "agent.yaml"))},
			{Name: "reviewer", Dir: filepath.Join(projectDir, "reviewer"), Config: mustParseConfig(t, filepath.Join(projectDir, "reviewer", "agent.yaml"))},
		},
	}

	g := NewGenerator(projectDir, nil)
	g.SetPresets(testPresets)
	require.NoError(t, g.RunProject(project))

	buildDir := filepath.Join(projectDir, ".build")
	composeData, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)

	var compose composeFile
	require.NoError(t, yaml.Unmarshal(composeData, &compose))

	for _, name := range []string{"coder", "reviewer"} {
		gatewayName := name + "-gateway"

		// Verify GATEWAY_HOST env var matches gateway service name
		agentSvcRaw, ok := compose.Services[name].(map[string]any)
		require.True(t, ok, "service %s not found or wrong type", name)
		envRaw, ok := agentSvcRaw["environment"].(map[string]any)
		require.True(t, ok, "environment not found for %s", name)
		assert.Equal(t, gatewayName, envRaw["GATEWAY_HOST"], "GATEWAY_HOST mismatch for %s", name)

		// Verify gateway network alias matches GATEWAY_HOST
		gatewaySvcRaw, ok := compose.Services[gatewayName].(map[string]any)
		require.True(t, ok, "service %s not found or wrong type", gatewayName)
		networks, ok := gatewaySvcRaw["networks"].(map[string]any)
		require.True(t, ok)
		sandbox, ok := networks["sandbox"].(map[string]any)
		require.True(t, ok)
		aliases, ok := sandbox["aliases"].([]any)
		require.True(t, ok)
		assert.Contains(t, aliases, gatewayName, "gateway alias mismatch for %s", name)

		// Verify bind-mount config.yaml exists as a file (not a directory)
		configPath := filepath.Join(buildDir, name, "config.yaml")
		info, err := os.Stat(configPath)
		require.NoError(t, err, "config.yaml missing for %s", name)
		assert.False(t, info.IsDir(), "config.yaml should be a file, not a directory, for %s", name)

		// Verify Dockerfile has CMD
		df, err := os.ReadFile(filepath.Join(buildDir, name, "Dockerfile"))
		require.NoError(t, err)
		assert.Contains(t, string(df), "CMD", "Dockerfile missing CMD for %s", name)
	}

	// Verify per-agent gateway/config.yaml exists (Docker build context)
	for _, name := range []string{"coder", "reviewer"} {
		assert.FileExists(t, filepath.Join(buildDir, name, "gateway", "config.yaml"))
	}
}

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

func mustParseConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	dir := filepath.Dir(path)
	cfg, err := config.Load(dir)
	require.NoError(t, err)
	return cfg
}

func TestWarnUnresolvedVars(t *testing.T) {
	tests := []struct {
		name        string
		extraBuilds []string
		wantWarns   []string
		wantNoWarn  bool
	}{
		{
			name:        "literal value - no warning",
			extraBuilds: []string{`RUN echo '{"allowed_users":["@coreyortea"]}' > /opt/config.json`},
			wantNoWarn:  true,
		},
		{
			name:        "ENV directive - no warning",
			extraBuilds: []string{"ENV TELEGRAM_BOT_TOKEN=dummy"},
			wantNoWarn:  true,
		},
		{
			name:        "ARG directive - no warning",
			extraBuilds: []string{"ARG MY_BUILD_VAR"},
			wantNoWarn:  true,
		},
		{
			name:        "unresolved ${VAR} in RUN - warns",
			extraBuilds: []string{`RUN echo '{"allowed_users":["@${TELEGRAM_USERNAME}"]}' > /opt/config.json`},
			wantWarns:   []string{"TELEGRAM_USERNAME"},
		},
		{
			name:        "unresolved $VAR without braces - warns",
			extraBuilds: []string{`RUN echo $MY_SECRET > /opt/token`},
			wantWarns:   []string{"MY_SECRET"},
		},
		{
			name:        "multiple unresolved vars - warns for each",
			extraBuilds: []string{`RUN echo '${FOO} ${BAR}' > /opt/cfg`},
			wantWarns:   []string{"FOO", "BAR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contribs := &plugin.Contributions{}
			contribs.Runtime.ExtraBuilds = tt.extraBuilds

			// Capture slog output via custom handler
			var buf bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			defer slog.SetDefault(oldLogger)

			warnUnresolvedVars("test-plugin", contribs)

			output := buf.String()

			if tt.wantNoWarn {
				assert.Empty(t, output, "expected no warning but got: %s", output)
			} else {
				for _, varName := range tt.wantWarns {
					assert.Contains(t, output, fmt.Sprintf("var=%s", varName))
					assert.Contains(t, output, "plugin=test-plugin")
				}
			}
		})
	}
}
