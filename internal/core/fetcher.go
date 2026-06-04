// Package core implements core version fetching and caching.
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// CacheDir returns the path where a specific core version is cached.
func CacheDir(version string) string {
	base := cacheBase()
	return filepath.Join(base, version)
}

// IsCachedAt checks if a core version is fully downloaded at the given path.
func IsCachedAt(versionDir string) bool {
	_, err := os.Stat(filepath.Join(versionDir, ".complete"))
	return err == nil
}

// Fetch downloads a core version if not already cached. Returns the path to the cached core.
func Fetch(version string) (string, error) {
	dir := CacheDir(version)
	if IsCachedAt(dir) {
		return dir, nil
	}

	// Download from GitHub releases
	if err := download(version, dir); err != nil {
		// Clean up partial download
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("fetch core %s: %w", version, err)
	}

	// Mark complete
	if err := os.WriteFile(filepath.Join(dir, ".complete"), []byte(version), 0644); err != nil {
		return "", fmt.Errorf("mark complete: %w", err)
	}

	return dir, nil
}

func cacheBase() string {
	if dir := os.Getenv("AGENT_SANDBOX_CACHE"); dir != "" {
		return filepath.Join(dir, "core")
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", "agent-sandbox", "core")
	default:
		if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
			return filepath.Join(xdg, "agent-sandbox", "core")
		}
		return filepath.Join(home, ".cache", "agent-sandbox", "core")
	}
}

func download(version, destDir string) error {
	// TODO: implement GitHub release tarball download
	return fmt.Errorf("remote fetch not yet implemented for version %s (use local core)", version)
}
