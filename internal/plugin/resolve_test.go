package plugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLocal(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins", "my-plugin")
	require.NoError(t, os.MkdirAll(pluginDir, 0755))

	pluginYAML := `
name: my-plugin
options:
  greeting:
    type: string
    default: "hello"
contributes:
  runtime:
    extra_builds:
      - "RUN echo {{ .options.greeting }}"
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(pluginYAML), 0644))

	resolver := NewResolver(dir, nil)
	p, err := resolver.Resolve("my-plugin", "")
	require.NoError(t, err)
	assert.Equal(t, "my-plugin", p.Name)
}

func TestResolveBundled(t *testing.T) {
	dir := t.TempDir()
	// No local plugins dir — should fall back to bundled
	bundled := testBundledFS()
	resolver := NewResolver(dir, bundled)
	p, err := resolver.Resolve("github-pat", "")
	require.NoError(t, err)
	assert.Equal(t, "github-pat", p.Name)
}

func TestResolve_NotFound(t *testing.T) {
	dir := t.TempDir()
	resolver := NewResolver(dir, nil)
	_, err := resolver.Resolve("nonexistent", "")
	assert.ErrorContains(t, err, "plugin \"nonexistent\" not found")
}

func testBundledFS() fs.FS {
	return fstest.MapFS{
		"github-pat/plugin.yaml": &fstest.MapFile{
			Data: []byte(`
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
`),
		},
	}
}
