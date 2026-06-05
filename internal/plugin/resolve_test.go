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

func TestResolve_BuiltinPrefix(t *testing.T) {
	dir := t.TempDir()
	bundled := testBundledFS()

	resolver := NewResolver(dir, bundled)
	p, err := resolver.Resolve("@builtin/github-pat", "")
	require.NoError(t, err)
	assert.Equal(t, "github-pat", p.Name)
}

func TestResolve_BuiltinPrefix_NotFound(t *testing.T) {
	dir := t.TempDir()
	bundled := testBundledFS()
	resolver := NewResolver(dir, bundled)
	_, err := resolver.Resolve("@builtin/nonexistent", "")
	assert.ErrorContains(t, err, "not found in bundled plugins")
}

func TestResolve_BuiltinPrefix_NoBundledFS(t *testing.T) {
	dir := t.TempDir()
	resolver := NewResolver(dir, nil)
	_, err := resolver.Resolve("@builtin/github-pat", "")
	assert.ErrorContains(t, err, "no bundled plugins available")
}

func TestResolve_LocalPathPrefix(t *testing.T) {
	dir := t.TempDir()
	customDir := filepath.Join(dir, "plugins", "custom-thing")
	require.NoError(t, os.MkdirAll(customDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(customDir, "plugin.yaml"), []byte(`
name: custom-thing
contributes:
  runtime:
    extra_builds:
      - "RUN echo custom"
`), 0644))

	resolver := NewResolver(dir, nil)
	p, err := resolver.Resolve("./plugins/custom-thing", "")
	require.NoError(t, err)
	assert.Equal(t, "custom-thing", p.Name)
	assert.Equal(t, filepath.Clean(customDir), p.BaseDir)
}

func TestResolve_LocalPathPrefix_NotFound(t *testing.T) {
	dir := t.TempDir()
	resolver := NewResolver(dir, nil)
	_, err := resolver.Resolve("./nonexistent", "")
	assert.ErrorContains(t, err, "plugin at \"./nonexistent\" not found")
}

func TestResolve_LocalPathPrefix_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	resolver := NewResolver(dir, nil)
	_, err := resolver.Resolve("./../../etc", "")
	assert.ErrorContains(t, err, "escapes project directory")
}

func TestResolve_BareName_Rejected(t *testing.T) {
	dir := t.TempDir()
	bundled := testBundledFS()
	resolver := NewResolver(dir, bundled)
	_, err := resolver.Resolve("github-pat", "")
	assert.ErrorContains(t, err, "must use @builtin/github-pat or ./<path> prefix")
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
