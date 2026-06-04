// internal/core/fetcher_test.go
package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheDir(t *testing.T) {
	dir := CacheDir("v1.0.0")
	assert.Contains(t, dir, "agent-sandbox")
	assert.Contains(t, dir, "v1.0.0")
}

func TestIsCached(t *testing.T) {
	cacheDir := t.TempDir()
	version := "v1.0.0"
	versionDir := filepath.Join(cacheDir, version)

	// Not cached yet
	assert.False(t, IsCachedAt(versionDir))

	// Create marker
	require.NoError(t, os.MkdirAll(versionDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(versionDir, ".complete"), []byte(""), 0644))
	assert.True(t, IsCachedAt(versionDir))
}

func TestCacheDir_CustomEnv(t *testing.T) {
	t.Setenv("AGENT_SANDBOX_CACHE", "/tmp/custom-cache")
	dir := CacheDir("v2.0.0")
	assert.Equal(t, "/tmp/custom-cache/core/v2.0.0", dir)
}
