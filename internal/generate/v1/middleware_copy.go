package v1

import (
	"fmt"
	"os"
	"path/filepath"
)

// CopyCustomMiddleware copies custom middleware .go files into the gateway build context.
func CopyCustomMiddleware(projectDir, outDir string, middlewarePaths []string) error {
	if len(middlewarePaths) == 0 {
		return nil
	}

	destDir := filepath.Join(outDir, "gateway-src", "middlewares", "custom")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create middleware dest dir: %w", err)
	}

	for _, mwPath := range middlewarePaths {
		var srcPath string
		if filepath.IsAbs(mwPath) {
			srcPath = mwPath
		} else {
			srcPath = filepath.Join(projectDir, mwPath)
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read middleware %s: %w", mwPath, err)
		}

		destFile := filepath.Join(destDir, filepath.Base(mwPath))
		if err := os.WriteFile(destFile, data, 0644); err != nil {
			return fmt.Errorf("write middleware %s: %w", destFile, err)
		}
	}

	return nil
}
