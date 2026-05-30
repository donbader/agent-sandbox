package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRuntime(t *testing.T) {
	t.Run("resolves from local plugins dir", func(t *testing.T) {
		dir := t.TempDir()
		pluginDir := filepath.Join(dir, "plugins", "custom")
		require.NoError(t, os.MkdirAll(pluginDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "runtime.yaml"), []byte(`
name: custom
base_image: python:3.12-slim
install:
  - pip install my-cli
cmd: ["my-cli", "run"]
`), 0644))

		rc, err := ResolveRuntime(dir, "custom")
		require.NoError(t, err)
		assert.Equal(t, "custom", rc.Name)
		assert.Equal(t, "python:3.12-slim", rc.BaseImage)
		assert.Equal(t, []string{"pip install my-cli"}, rc.Install)
		assert.Equal(t, []string{"my-cli", "run"}, rc.Cmd)
		assert.Equal(t, "agent", rc.User) // default
	})

	t.Run("resolves embedded codex", func(t *testing.T) {
		rc, err := ResolveRuntime("/nonexistent", "codex")
		require.NoError(t, err)
		assert.Equal(t, "codex", rc.Name)
		assert.Equal(t, "node:22-slim", rc.BaseImage)
		assert.Contains(t, rc.Install[len(rc.Install)-1], "codex")
		assert.Equal(t, []string{"sleep", "infinity"}, rc.Cmd)
	})

	t.Run("local overrides embedded", func(t *testing.T) {
		dir := t.TempDir()
		pluginDir := filepath.Join(dir, "plugins", "codex")
		require.NoError(t, os.MkdirAll(pluginDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "runtime.yaml"), []byte(`
name: codex
base_image: node:20-slim
install:
  - npm install -g @openai/codex@0.1.0
cmd: ["codex", "exec", "--full-auto"]
`), 0644))

		rc, err := ResolveRuntime(dir, "codex")
		require.NoError(t, err)
		assert.Equal(t, "node:20-slim", rc.BaseImage) // local override wins
	})

	t.Run("unknown runtime", func(t *testing.T) {
		_, err := ResolveRuntime("/nonexistent", "unknown-runtime")
		assert.ErrorContains(t, err, "unknown runtime")
	})
}

func TestResolveInlineRuntime(t *testing.T) {
	t.Run("valid inline", func(t *testing.T) {
		inline := map[string]any{
			"base_image": "python:3.12-slim",
			"install":    []any{"pip install my-cli"},
			"cmd":        []any{"my-cli", "run"},
		}

		rc, err := ResolveInlineRuntime(inline)
		require.NoError(t, err)
		assert.Equal(t, "python:3.12-slim", rc.BaseImage)
		assert.Equal(t, []string{"pip install my-cli"}, rc.Install)
		assert.Equal(t, []string{"my-cli", "run"}, rc.Cmd)
		assert.Equal(t, "agent", rc.User)
	})

	t.Run("missing base_image", func(t *testing.T) {
		inline := map[string]any{
			"install": []any{"pip install my-cli"},
		}

		_, err := ResolveInlineRuntime(inline)
		assert.ErrorContains(t, err, "base_image is required")
	})

	t.Run("defaults cmd to sleep infinity", func(t *testing.T) {
		inline := map[string]any{
			"base_image": "python:3.12-slim",
		}

		rc, err := ResolveInlineRuntime(inline)
		require.NoError(t, err)
		assert.Equal(t, []string{"sleep", "infinity"}, rc.Cmd)
	})
}
