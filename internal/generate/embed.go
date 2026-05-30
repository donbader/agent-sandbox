package generate

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:_gateway-src
var gatewaySource embed.FS

// writeGatewaySource writes the embedded gateway source to .build/gateway-src/.
// The source is stored in _gateway-src/ (underscore prefix prevents Go from
// compiling it as part of this module). Files named *.embed are renamed back
// (go.mod.embed → go.mod) since go:embed cannot embed directories containing
// go.mod files (treated as separate modules).
func (g *Generator) writeGatewaySource() error {
	destDir := filepath.Join(g.OutDir, "gateway-src")

	return fs.WalkDir(gatewaySource, "_gateway-src", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel("_gateway-src", path)
		if err != nil {
			return err
		}

		// Rename .embed suffix back to original name
		destName := relPath
		if strings.HasSuffix(destName, ".embed") {
			destName = strings.TrimSuffix(destName, ".embed")
		}
		destPath := filepath.Join(destDir, destName)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := gatewaySource.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	})
}
