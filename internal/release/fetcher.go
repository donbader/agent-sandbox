// Package release implements core version fetching and caching.
package release

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// GitHubRepo is the repository containing core releases.
	GitHubRepo = "donbader/agent-sandbox"
	// AssetPrefix is the prefix for core tarball assets in GitHub Releases.
	AssetPrefix = "agent-sandbox-core-"
	// LatestCacheTTL is how long the "latest" resolution is cached before re-checking.
	LatestCacheTTL = 1 * time.Hour
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

	if err := download(version, dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("fetch core %s: %w", version, err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".complete"), []byte(version), 0644); err != nil {
		return "", fmt.Errorf("mark complete: %w", err)
	}

	return dir, nil
}

// FetchLatest queries GitHub for the latest core-v* release, downloads it, and returns
// the cache directory. Results are cached for LatestCacheTTL to avoid hitting the API
// on every generate. Old cached versions are automatically cleaned up when a new version
// is fetched.
func FetchLatest() (string, error) {
	version, err := cachedLatestVersion()
	if err == nil && version != "" {
		dir := CacheDir(version)
		if IsCachedAt(dir) {
			return dir, nil
		}
	}

	previousVersion := version
	version, err = resolveLatestVersion()
	if err != nil {
		return "", fmt.Errorf("resolve latest core version: %w", err)
	}

	_ = saveLatestResolution(version)

	dir, err := Fetch(version)
	if err != nil {
		return "", err
	}

	// Clean up old cached versions when a new one is fetched.
	if previousVersion != "" && previousVersion != version {
		cleanOldVersions(version)
	}

	return dir, nil
}

// resolveLatestVersion queries GitHub Releases API for the latest core-v* tag.
func resolveLatestVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases", GitHubRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("query releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode releases: %w", err)
	}

	var versions []string
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if strings.HasPrefix(r.TagName, "core-") {
			versions = append(versions, strings.TrimPrefix(r.TagName, "core-"))
		}
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("no core releases found in %s", GitHubRepo)
	}

	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return versions[0], nil
}

type latestResolution struct {
	Version    string    `json:"version"`
	ResolvedAt time.Time `json:"resolved_at"`
}

func latestCachePath() string {
	return filepath.Join(cacheBase(), "latest.json")
}

func cachedLatestVersion() (string, error) {
	data, err := os.ReadFile(latestCachePath())
	if err != nil {
		return "", err
	}
	var res latestResolution
	if err := json.Unmarshal(data, &res); err != nil {
		return "", err
	}
	if time.Since(res.ResolvedAt) > LatestCacheTTL {
		return "", fmt.Errorf("cache expired")
	}
	return res.Version, nil
}

func saveLatestResolution(version string) error {
	res := latestResolution{Version: version, ResolvedAt: time.Now()}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(latestCachePath()), 0755); err != nil {
		return err
	}
	return os.WriteFile(latestCachePath(), data, 0644)
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

// cleanOldVersions removes all cached version directories except the current one.
func cleanOldVersions(currentVersion string) {
	base := cacheBase()
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == currentVersion {
			continue
		}
		// Only remove directories that look like version dirs (have .complete sentinel).
		if IsCachedAt(filepath.Join(base, entry.Name())) {
			_ = os.RemoveAll(filepath.Join(base, entry.Name()))
		}
	}
}

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
