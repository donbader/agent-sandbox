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

	// Write agent.yaml that uses the plugin
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
installations:
  - plugin: ./plugins/my-tool
    options:
      version: "2.0.0"
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

	g := NewGenerator(projectDir, nil)
	require.NoError(t, g.Run())

	// Verify outputs
	buildDir := filepath.Join(projectDir, ".build")
	assert.FileExists(t, filepath.Join(buildDir, "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yml"))

	// Check Dockerfile content
	df, err := os.ReadFile(filepath.Join(buildDir, "Dockerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(df), "FROM node:24-slim")
	assert.Contains(t, string(df), "npm install -g my-tool@2.0.0")
	assert.Contains(t, string(df), `CMD ["sleep","infinity"]`)

	// Check compose content
	comp, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(comp), "agent:")
	assert.Contains(t, string(comp), "gateway:")
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
    services:
      - url: https://github.com
        headers:
          Authorization: "Bearer {{ .options.token }}"
`), 0644))

	// Create gateway source in core directory
	gatewayDir := filepath.Join(coreDir, "gateway", "cmd", "gateway")
	require.NoError(t, os.MkdirAll(gatewayDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gatewayDir, "main.go"), []byte("package main\n"), 0644))

	// Create go.mod in coreDir (simulates self-contained core distribution)
	require.NoError(t, os.WriteFile(filepath.Join(coreDir, "go.mod"), []byte("module github.com/donbader/agent-sandbox\n\ngo 1.26\n"), 0644))

	agentYAML := `
name: test-agent
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "ghp_test123"
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

	g := NewGeneratorWithCore(projectDir, coreDir)
	require.NoError(t, g.Run())

	buildDir := filepath.Join(projectDir, ".build")
	assert.FileExists(t, filepath.Join(buildDir, "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "docker-compose.yml"))
	assert.FileExists(t, filepath.Join(buildDir, "gateway-src", "Dockerfile"))
	assert.FileExists(t, filepath.Join(buildDir, "gateway-src", "go.mod"))
	assert.FileExists(t, filepath.Join(buildDir, "gateway-src", "core", "gateway", "cmd", "gateway", "main.go"))
}

func TestGenerator_Run_WithSidecar(t *testing.T) {
	projectDir := t.TempDir()

	// Plugin that contributes a sidecar
	pluginDir := filepath.Join(projectDir, "plugins", "my-sidecar")
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
          PORT: "{{ .options.port }}"
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(pluginYAML), 0644))

	agentYAML := `
name: test-agent
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/my-sidecar
    options:
      port: "8080"
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

	g := NewGenerator(projectDir, nil)
	require.NoError(t, g.Run())

	buildDir := filepath.Join(projectDir, ".build")
	comp, err := os.ReadFile(filepath.Join(buildDir, "docker-compose.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(comp), "mysvc:")
	assert.Contains(t, string(comp), "PORT")
	assert.Contains(t, string(comp), "8080")
}

func TestGenerator_Run_RequiresUnsatisfied(t *testing.T) {
	projectDir := t.TempDir()

	// Plugin that declares a requires dependency
	pluginDir := filepath.Join(projectDir, "plugins", "my-channel")
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
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/my-channel
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

	g := NewGenerator(projectDir, nil)
	err := g.Run()
	require.Error(t, err)
	assert.ErrorContains(t, err, "requires \"@builtin/agent-manager-acp\"")
}

func TestGenerator_Run_RequiresSatisfied(t *testing.T) {
	projectDir := t.TempDir()

	// "dep" plugin (simulates agent-manager-acp)
	depDir := filepath.Join(projectDir, "plugins", "agent-manager-acp")
	require.NoError(t, os.MkdirAll(depDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(depDir, "plugin.yaml"), []byte(`
name: agent-manager-acp
contributes:
  runtime:
    extra_builds:
      - "RUN echo manager"
`), 0644))

	// Plugin that requires agent-manager-acp
	channelDir := filepath.Join(projectDir, "plugins", "my-channel")
	require.NoError(t, os.MkdirAll(channelDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "plugin.yaml"), []byte(`
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
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: ./plugins/agent-manager-acp
  - plugin: ./plugins/my-channel
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

	g := NewGenerator(projectDir, nil)
	require.NoError(t, g.Run())
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

	agentYAML := `
name: test-agent
runtime:
  image: "@builtin/codex"
  entrypoint: ["sleep", "infinity"]
installations:
  - plugin: "@builtin/my-bundled"
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "agent.yaml"), []byte(agentYAML), 0644))

	g := NewGeneratorWithCore(projectDir, coreDir)
	require.NoError(t, g.Run())

	// Verify asset was extracted to .build/plugins/my-bundled/my-src/
	extractedFile := filepath.Join(projectDir, ".build", "plugins", "my-bundled", "my-src", "main.ts")
	assert.FileExists(t, extractedFile)

	// Verify Dockerfile references the extracted path
	df, err := os.ReadFile(filepath.Join(projectDir, ".build", "Dockerfile"))
	require.NoError(t, err)
	assert.Contains(t, string(df), "COPY .build/plugins/my-bundled/my-src/ /opt/my-src/")
}
