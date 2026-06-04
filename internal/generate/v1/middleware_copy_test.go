// internal/generate/v1/middleware_copy_test.go
package v1

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyCustomMiddleware(t *testing.T) {
	projectDir := t.TempDir()
	outDir := t.TempDir()

	// Create a custom middleware file
	mwDir := filepath.Join(projectDir, "middlewares")
	require.NoError(t, os.MkdirAll(mwDir, 0755))
	mwContent := `package custom

import "github.com/donbader/agent-sandbox/core/sdk/gateway"

func init() {
    gateway.RegisterMiddleware("test", func(ctx *gateway.MiddlewareContext) error {
        return nil
    })
}
`
	require.NoError(t, os.WriteFile(filepath.Join(mwDir, "test.go"), []byte(mwContent), 0644))

	err := CopyCustomMiddleware(projectDir, outDir, []string{"./middlewares/test.go"})
	require.NoError(t, err)

	// Verify file was copied to the custom middleware package dir
	dest := filepath.Join(outDir, "gateway-src", "middlewares", "custom", "test.go")
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Contains(t, string(data), "RegisterMiddleware")
}

func TestCopyCustomMiddleware_Empty(t *testing.T) {
	err := CopyCustomMiddleware("", "", nil)
	require.NoError(t, err)
}

func TestCopyCustomMiddleware_MissingFile(t *testing.T) {
	projectDir := t.TempDir()
	outDir := t.TempDir()

	err := CopyCustomMiddleware(projectDir, outDir, []string{"./nonexistent.go"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read middleware")
}
