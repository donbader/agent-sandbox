// internal/core/fetcher_test.go
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestLatestResolutionCache(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENT_SANDBOX_CACHE", tmpDir)

	// No cache file yet — should return error
	_, err := cachedLatestVersion()
	assert.Error(t, err)

	// Save a resolution
	require.NoError(t, saveLatestResolution("v1.2.3"))

	// Should return cached version
	version, err := cachedLatestVersion()
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", version)
}

func TestLatestResolutionCache_Expired(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("AGENT_SANDBOX_CACHE", tmpDir)

	// Write an expired resolution (resolved_at in the past)
	res := latestResolution{Version: "v0.9.0", ResolvedAt: time.Now().Add(-2 * LatestCacheTTL)}
	data, err := json.Marshal(res)
	require.NoError(t, err)
	cachePath := latestCachePath()
	require.NoError(t, os.MkdirAll(filepath.Dir(cachePath), 0755))
	require.NoError(t, os.WriteFile(cachePath, data, 0644))

	// Should return error (expired)
	_, err = cachedLatestVersion()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}
