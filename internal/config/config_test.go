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
  github:
    token: "${GITHUB_PAT}"
`), 0644)
		require.NoError(t, err)

		cfg, err := Load(dir)
		require.NoError(t, err)
		assert.Equal(t, "coder", cfg.Name)
		assert.Equal(t, "codex", cfg.RuntimeName())
		assert.Nil(t, cfg.RuntimeInline())
		assert.Equal(t, "${GITHUB_PAT}", cfg.Features["github"]["token"])
	})

	t.Run("inline runtime", func(t *testing.T) {
		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(`
name: my-agent
runtime:
  base_image: python:3.12-slim
  install:
    - pip install my-agent-cli
  cmd: ["my-agent-cli", "--headless"]
`), 0644)
		require.NoError(t, err)

		cfg, err := Load(dir)
		require.NoError(t, err)
		assert.Equal(t, "my-agent", cfg.Name)
		assert.Equal(t, "", cfg.RuntimeName())
		inline := cfg.RuntimeInline()
		require.NotNil(t, inline)
		assert.Equal(t, "python:3.12-slim", inline["base_image"])
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
		assert.Nil(t, cfg.Features)
	})
}
