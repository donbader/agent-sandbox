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

func TestMergeInstallations(t *testing.T) {
	t.Run("shared only", func(t *testing.T) {
		shared := []FeatureEntry{
			{Plugin: "@builtin/github-pat", Config: map[string]any{"token": "${PAT}"}},
		}
		result := MergeInstallations(shared, nil)
		require.Len(t, result, 1)
		assert.Equal(t, "@builtin/github-pat", result[0].Plugin)
		assert.Equal(t, "${PAT}", result[0].Options["token"])
	})

	t.Run("per-agent only", func(t *testing.T) {
		perAgent := []Installation{
			{Plugin: "@builtin/telegram", Options: map[string]any{"bot": "abc"}},
		}
		result := MergeInstallations(nil, perAgent)
		require.Len(t, result, 1)
		assert.Equal(t, "@builtin/telegram", result[0].Plugin)
	})

	t.Run("per-agent overrides shared same plugin", func(t *testing.T) {
		shared := []FeatureEntry{
			{Plugin: "@builtin/github-pat", Config: map[string]any{"token": "shared-token"}},
			{Plugin: "@builtin/mcp-oauth", Config: map[string]any{"provider": "notion"}},
		}
		perAgent := []Installation{
			{Plugin: "@builtin/github-pat", Options: map[string]any{"token": "agent-token"}},
		}

		result := MergeInstallations(shared, perAgent)
		require.Len(t, result, 2)

		// mcp-oauth from shared (not overridden)
		assert.Equal(t, "@builtin/mcp-oauth", result[0].Plugin)
		assert.Equal(t, "notion", result[0].Options["provider"])
		// github-pat from per-agent (overrides shared)
		assert.Equal(t, "@builtin/github-pat", result[1].Plugin)
		assert.Equal(t, "agent-token", result[1].Options["token"])
	})

	t.Run("different plugins merge additively", func(t *testing.T) {
		shared := []FeatureEntry{
			{Plugin: "@builtin/github-pat", Config: map[string]any{"token": "${PAT}"}},
		}
		perAgent := []Installation{
			{Plugin: "@builtin/telegram", Options: map[string]any{"bot": "abc"}},
		}

		result := MergeInstallations(shared, perAgent)
		require.Len(t, result, 2)
		assert.Equal(t, "@builtin/github-pat", result[0].Plugin)
		assert.Equal(t, "@builtin/telegram", result[1].Plugin)
	})
}

func TestLoadFleetAgents(t *testing.T) {
	t.Run("loads and merges", func(t *testing.T) {
		dir := t.TempDir()

		// fleet.yaml
		fleetYAML := `
agents:
  - coder
  - reviewer
shared:
  installations:
    - plugin: "@builtin/github-pat"
      token: "${GITHUB_PAT}"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

		// coder/agent.yaml — has its own github-pat (should override)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "coder"), 0755))
		coderYAML := `
name: coder
runtime:
  image: "@builtin/codex"
installations:
  - plugin: "@builtin/github-pat"
    options:
      token: "${CODER_PAT}"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "coder", "agent.yaml"), []byte(coderYAML), 0644))

		// reviewer/agent.yaml — no github-pat (inherits from shared)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "reviewer"), 0755))
		reviewerYAML := `
name: reviewer
runtime:
  image: "@builtin/codex"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "reviewer", "agent.yaml"), []byte(reviewerYAML), 0644))

		fleet, agents, err := LoadFleetAgents(dir)
		require.NoError(t, err)
		assert.Len(t, fleet.Agents, 2)
		require.Len(t, agents, 2)

		// coder: per-agent github-pat wins
		coder := agents[0]
		assert.Equal(t, "coder", coder.Config.Name)
		require.Len(t, coder.Config.Installations, 1)
		assert.Equal(t, "@builtin/github-pat", coder.Config.Installations[0].Plugin)
		assert.Equal(t, "${CODER_PAT}", coder.Config.Installations[0].Options["token"])

		// reviewer: gets shared github-pat
		reviewer := agents[1]
		assert.Equal(t, "reviewer", reviewer.Config.Name)
		require.Len(t, reviewer.Config.Installations, 1)
		assert.Equal(t, "@builtin/github-pat", reviewer.Config.Installations[0].Plugin)
		assert.Equal(t, "${GITHUB_PAT}", reviewer.Config.Installations[0].Options["token"])
	})

	t.Run("missing agent dir fails", func(t *testing.T) {
		dir := t.TempDir()
		fleetYAML := `
agents:
  - nonexistent
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

		_, _, err := LoadFleetAgents(dir)
		assert.ErrorContains(t, err, "loading agent")
	})
}

func TestMergeGatewayServices(t *testing.T) {
	t.Run("shared only", func(t *testing.T) {
		shared := []GatewayServiceEntry{
			{URL: "https://gateway.example.com", Headers: map[string]string{"Auth": "Bearer x"}},
		}
		result := MergeGatewayServices(shared, nil)
		require.Len(t, result, 1)
		assert.Equal(t, "https://gateway.example.com", result[0].URL)
	})

	t.Run("per-agent only", func(t *testing.T) {
		perAgent := []GatewayServiceEntry{
			{URL: "https://api.openai.com"},
		}
		result := MergeGatewayServices(nil, perAgent)
		require.Len(t, result, 1)
		assert.Equal(t, "https://api.openai.com", result[0].URL)
	})

	t.Run("per-agent overrides shared same URL", func(t *testing.T) {
		shared := []GatewayServiceEntry{
			{URL: "https://gateway.example.com", Headers: map[string]string{"Auth": "shared-token"}},
		}
		perAgent := []GatewayServiceEntry{
			{URL: "https://gateway.example.com", Headers: map[string]string{"Auth": "agent-token"}},
		}

		result := MergeGatewayServices(shared, perAgent)
		require.Len(t, result, 1)
		assert.Equal(t, "agent-token", result[0].Headers["Auth"])
	})

	t.Run("different URLs merge additively", func(t *testing.T) {
		shared := []GatewayServiceEntry{
			{URL: "https://gateway.example.com"},
		}
		perAgent := []GatewayServiceEntry{
			{URL: "https://api.openai.com"},
		}

		result := MergeGatewayServices(shared, perAgent)
		require.Len(t, result, 2)
		assert.Equal(t, "https://gateway.example.com", result[0].URL)
		assert.Equal(t, "https://api.openai.com", result[1].URL)
	})
}

func TestLoadFleetAgents_SharedGateway(t *testing.T) {
	dir := t.TempDir()

	fleetYAML := `
agents:
  - coder
  - reviewer
shared:
  gateway:
    services:
      - url: https://agent-gateway.stx-ai.net
        headers:
          Authorization: Bearer ${STX_TOKEN}
  installations:
    - plugin: "@builtin/github-pat"
      token: "${GITHUB_PAT}"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0644))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "coder"), 0755))
	coderYAML := `
name: coder
runtime:
  image: "@builtin/codex"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "coder", "agent.yaml"), []byte(coderYAML), 0644))

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "reviewer"), 0755))
	reviewerYAML := `
name: reviewer
runtime:
  image: "@builtin/claude-code"
gateway:
  services:
    - url: https://api.anthropic.com
      headers:
        x-api-key: ${ANTHROPIC_KEY}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "reviewer", "agent.yaml"), []byte(reviewerYAML), 0644))

	_, agents, err := LoadFleetAgents(dir)
	require.NoError(t, err)
	require.Len(t, agents, 2)

	// coder: gets shared gateway only
	coder := agents[0]
	require.Len(t, coder.Config.Gateway.Services, 1)
	assert.Equal(t, "https://agent-gateway.stx-ai.net", coder.Config.Gateway.Services[0].URL)

	// reviewer: gets shared + own gateway (additive)
	reviewer := agents[1]
	require.Len(t, reviewer.Config.Gateway.Services, 2)
	assert.Equal(t, "https://agent-gateway.stx-ai.net", reviewer.Config.Gateway.Services[0].URL)
	assert.Equal(t, "https://api.anthropic.com", reviewer.Config.Gateway.Services[1].URL)
}
