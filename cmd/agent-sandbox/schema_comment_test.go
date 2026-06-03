package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureSchemaComment(t *testing.T) {
	t.Run("inserts comment when missing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agent.yaml")
		require.NoError(t, os.WriteFile(path, []byte("name: coder\nruntime: codex\n"), 0644))

		err := ensureSchemaComment(path, ".build/schema.json")
		require.NoError(t, err)

		data, _ := os.ReadFile(path)
		assert.Equal(t, "# yaml-language-server: $schema=.build/schema.json\nname: coder\nruntime: codex\n", string(data))
	})

	t.Run("replaces wrong schema path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "fleet.yaml")
		require.NoError(t, os.WriteFile(path, []byte("# yaml-language-server: $schema=.build/schema.json\nagents:\n  - coder\n"), 0644))

		err := ensureSchemaComment(path, ".build/fleet-schema.json")
		require.NoError(t, err)

		data, _ := os.ReadFile(path)
		assert.Equal(t, "# yaml-language-server: $schema=.build/fleet-schema.json\nagents:\n  - coder\n", string(data))
	})

	t.Run("no-op when comment already correct", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agent.yaml")
		content := "# yaml-language-server: $schema=.build/schema.json\nname: coder\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		err := ensureSchemaComment(path, ".build/schema.json")
		require.NoError(t, err)

		data, _ := os.ReadFile(path)
		assert.Equal(t, content, string(data))
	})

	t.Run("handles file with other comments at top", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agent.yaml")
		require.NoError(t, os.WriteFile(path, []byte("# My agent config\nname: coder\n"), 0644))

		err := ensureSchemaComment(path, ".build/schema.json")
		require.NoError(t, err)

		data, _ := os.ReadFile(path)
		assert.Equal(t, "# yaml-language-server: $schema=.build/schema.json\n# My agent config\nname: coder\n", string(data))
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		err := ensureSchemaComment("/nonexistent/path.yaml", ".build/schema.json")
		assert.Error(t, err)
	})
}
