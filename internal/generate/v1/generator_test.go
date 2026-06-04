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
  - plugin: github-pat
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
  - plugin: my-sidecar
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
