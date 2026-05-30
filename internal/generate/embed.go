package generate

import (
	"io/fs"
	"os"
	"path/filepath"

	sandbox "github.com/donbader/agent-sandbox"
)

// writeGatewaySource writes the embedded gateway source to .build/gateway-src/.
// Also generates a go.mod for the Docker build context.
func (g *Generator) writeGatewaySource() error {
	destDir := filepath.Join(g.OutDir, "gateway-src")

	err := fs.WalkDir(sandbox.GatewaySource, "gateway", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel("gateway", path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := sandbox.GatewaySource.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	})
	if err != nil {
		return err
	}

	// Generate go.mod for Docker build context
	goMod := "module github.com/donbader/agent-sandbox/gateway\n\ngo 1.24.3\n\nrequire gopkg.in/yaml.v3 v3.0.1\n"
	return os.WriteFile(filepath.Join(destDir, "go.mod"), []byte(goMod), 0644)
}
