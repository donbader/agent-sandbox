package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// extractBundledMiddleware extracts a middleware file from the bundled plugin FS to disk.
// Returns the absolute path to the extracted file.
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

	return dstPath, nil
}
