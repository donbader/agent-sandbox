package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("valid config with string runtime", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
name: coder
runtime: codex
features:
  - plugin: github
    token: "${GITHUB_PAT}"
`), 0644)
		require.NoError(t, err)

		cfg, err := Load(dir)
		require.NoError(t, err)
		assert.Equal(t, "coder", cfg.Name)
		assert.Equal(t, "codex", cfg.Runtime)
		require.Len(t, cfg.Features, 1)
		assert.Equal(t, "github", cfg.Features[0].Plugin)
		assert.Equal(t, "${GITHUB_PAT}", cfg.Features[0].Config["token"])
	})

	t.Run("feature with optional name", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
name: coder
runtime: codex
features:
  - plugin: static-header
    name: stx-llm-gateway
    domains: ["agent-gateway.stx-ai.net"]
    header: "Authorization"
`), 0644)
		require.NoError(t, err)

		cfg, err := Load(dir)
		require.NoError(t, err)
		require.Len(t, cfg.Features, 1)
		assert.Equal(t, "static-header", cfg.Features[0].Plugin)
		assert.Equal(t, "stx-llm-gateway", cfg.Features[0].Name)
		assert.Equal(t, "Authorization", cfg.Features[0].Config["header"])
		// name and plugin should not leak into Config
		_, hasPlugin := cfg.Features[0].Config["plugin"]
		_, hasName := cfg.Features[0].Config["name"]
		assert.False(t, hasPlugin)
		assert.False(t, hasName)
	})

	t.Run("missing name", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
runtime: codex
`), 0644)
		require.NoError(t, err)

		_, err = Load(dir)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("missing runtime", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
name: coder
`), 0644)
		require.NoError(t, err)

		_, err = Load(dir)
		assert.ErrorContains(t, err, "runtime is required")
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := Load("/nonexistent")
		assert.Error(t, err)
	})

	t.Run("no features", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
name: coder
runtime: codex
`), 0644)
		require.NoError(t, err)

		cfg, err := Load(dir)
		require.NoError(t, err)
		assert.Equal(t, "coder", cfg.Name)
		assert.Empty(t, cfg.Features)
	})

	t.Run("feature missing plugin field", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
name: coder
runtime: codex
features:
  - name: something
    token: "abc"
`), 0644)
		require.NoError(t, err)

		_, err = Load(dir)
		assert.ErrorContains(t, err, "plugin")
	})
}
