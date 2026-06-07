// Package core implements core version fetching and caching.
package core

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// GitHubRepo is the repository containing core releases.
	GitHubRepo = "donbader/agent-sandbox"
	// AssetPrefix is the prefix for core tarball assets in GitHub Releases.
	AssetPrefix = "agent-sandbox-core-"
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

// download fetches a core tarball from GitHub Releases and extracts it to destDir.
// Expected asset: agent-sandbox-core-{version}.tar.gz
// Release tag: core-{version} (e.g. core-v1.5.0)
// URL: https://github.com/{repo}/releases/download/core-{version}/{asset}
func download(version, destDir string) error {
	tag := "core-" + version
	asset := AssetPrefix + version + ".tar.gz"
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", GitHubRepo, tag, asset)

	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("core version %s not found (no release asset at %s)", version, url)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	return extractTarGz(resp.Body, destDir)
}

// extractTarGz extracts a .tar.gz stream into destDir.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Sanitize path to prevent traversal
		name := filepath.Clean(hdr.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}

		target := filepath.Join(destDir, name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
	}

	return nil
}
