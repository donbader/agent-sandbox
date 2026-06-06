package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_MissingName(t *testing.T) {
	dir := t.TempDir()
	yaml := `runtime:
  image: "@builtin/codex"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
	_, err := Load(dir)
	assert.ErrorContains(t, err, "name is required")
}

func TestLoad_MissingRuntimeImage(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: test
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
	_, err := Load(dir)
	assert.ErrorContains(t, err, "runtime.image is required")
}

func TestLoad_DockerURLDeprecated(t *testing.T) {
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
	_, err := Load(dir)
	assert.ErrorContains(t, err, "docker:// URLs are deprecated")
}

func TestLoad_BasicConfig(t *testing.T) {
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

	cfg, err := Load(dir)
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

func TestLoad_RuntimeEngine(t *testing.T) {
	t.Run("defaults to docker", func(t *testing.T) {
		dir := t.TempDir()
		yaml := `
name: test
runtime:
  image: "@builtin/codex"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
		cfg, err := Load(dir)
		require.NoError(t, err)
		assert.Equal(t, "", cfg.RuntimeEngine)
		assert.Equal(t, "docker", cfg.RuntimeEngineBinary())
	})

	t.Run("podman", func(t *testing.T) {
		dir := t.TempDir()
		yaml := `
name: test
runtime_engine: podman
runtime:
  image: "@builtin/codex"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
		cfg, err := Load(dir)
		require.NoError(t, err)
		assert.Equal(t, "podman", cfg.RuntimeEngine)
		assert.Equal(t, "podman", cfg.RuntimeEngineBinary())
	})

	t.Run("invalid engine rejected", func(t *testing.T) {
		dir := t.TempDir()
		yaml := `
name: test
runtime_engine: containerd
runtime:
  image: "@builtin/codex"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
		_, err := Load(dir)
		assert.ErrorContains(t, err, "runtime_engine must be 'docker' or 'podman'")
	})
}

func TestLoad_PlainHostPort(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: test
runtime:
  image: "@builtin/codex"
gateway:
  services:
    - url: "sidecar:8080"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(yaml), 0644))
	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "sidecar:8080", cfg.Gateway.Services[0].URL)
}

func TestValidate_CollectsAllErrors(t *testing.T) {
	// Config with multiple problems — validation should report all of them.
	cfg := &Config{
		Name:          "", // missing
		RuntimeEngine: "containerd",
		Runtime: RuntimeConfig{
			Image: "", // missing
		},
		Gateway: GatewayConfig{
			Services: []GatewayServiceEntry{
				{URL: "docker://old:8080"},
				{URL: ""},
			},
		},
	}

	err := cfg.Validate()
	require.Error(t, err)

	ve, ok := err.(*ValidationError)
	require.True(t, ok, "expected *ValidationError, got %T", err)
	assert.Len(t, ve.Errors, 5, "should collect all 5 validation errors")
	assert.Contains(t, ve.Error(), "name is required")
	assert.Contains(t, ve.Error(), "runtime.image is required")
	assert.Contains(t, ve.Error(), "runtime_engine must be")
	assert.Contains(t, ve.Error(), "docker:// URLs are deprecated")
	assert.Contains(t, ve.Error(), "url is required")
}

func TestValidate_NoErrorsOnValidConfig(t *testing.T) {
	cfg := &Config{
		Name: "valid-agent",
		Runtime: RuntimeConfig{
			Image: "@builtin/codex",
		},
		Gateway: GatewayConfig{
			Services: []GatewayServiceEntry{
				{URL: "https://api.example.com"},
			},
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}
