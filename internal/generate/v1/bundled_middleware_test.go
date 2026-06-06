package v1

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractBundledMiddleware(t *testing.T) {
	// Create a mock bundled FS with a middleware file
	bundledFS := fstest.MapFS{
		"github-pat/middlewares/github-auth.go": &fstest.MapFile{
			Data: []byte("package custom\n// github auth middleware\n"),
		},
	}

	buildDir := t.TempDir()
	g := &Generator{bundledFS: bundledFS}

	path, err := g.extractBundledMiddleware("github-pat", "./middlewares/github-auth.go", buildDir)
	require.NoError(t, err)

	// Verify file was extracted to expected location
	expectedPath := filepath.Join(buildDir, "plugins", "github-pat", "middlewares", "github-auth.go")
	assert.Equal(t, expectedPath, path)

	// Verify file content
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "github auth middleware")
}

func TestExtractBundledMiddleware_MissingFile(t *testing.T) {
	bundledFS := fstest.MapFS{}

	buildDir := t.TempDir()
	g := &Generator{bundledFS: bundledFS}

	_, err := g.extractBundledMiddleware("github-pat", "./middlewares/nonexistent.go", buildDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read github-pat/middlewares/nonexistent.go")
}
