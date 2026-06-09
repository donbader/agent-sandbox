package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// extractBundledMiddleware extracts a middleware file from the bundled plugin FS to disk.
// It also extracts all sibling .go files from the same directory (shared helpers).
// Returns the absolute path to the requested file.
func (g *Generator) extractBundledMiddleware(pluginName, relativePath, buildDir string) (string, error) {
	cleanPath := strings.TrimPrefix(relativePath, "./")
	srcPath := pluginName + "/" + cleanPath

	data, err := fs.ReadFile(g.bundledFS, srcPath)
	if err != nil {
		return "", fmt.Errorf("read %s from bundled FS: %w", srcPath, err)
	}

	dstPath := filepath.Join(buildDir, "plugins", pluginName, cleanPath)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dstPath, data, 0644); err != nil {
		return "", err
	}

	// Also extract sibling .go files from the same directory (shared helpers like pkce.go)
	srcDir := filepath.Dir(srcPath)
	entries, err := fs.ReadDir(g.bundledFS, srcDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			if entry.Name() == filepath.Base(cleanPath) {
				continue // already extracted above
			}
			sibData, err := fs.ReadFile(g.bundledFS, srcDir+"/"+entry.Name())
			if err != nil {
				continue
			}
			sibDst := filepath.Join(filepath.Dir(dstPath), entry.Name())
			_ = os.WriteFile(sibDst, sibData, 0644)
		}
	}

	return dstPath, nil
}
