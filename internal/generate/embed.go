package generate

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeGatewaySource copies the gateway source to .build/gateway-src/.
// The gateway source is located at gateway/ relative to the project root.
func (g *Generator) writeGatewaySource() error {
	// Find gateway source: look relative to the CLI binary's location,
	// or relative to the project dir (for development)
	srcDir := g.findGatewaySource()
	if srcDir == "" {
		return fmt.Errorf("gateway source not found")
	}

	destDir := filepath.Join(g.OutDir, "gateway-src")
	return copyDir(srcDir, destDir)
}

// findGatewaySource locates the gateway/ directory.
// Resolution order: project dir → executable dir → GATEWAY_SRC env var.
func (g *Generator) findGatewaySource() string {
	// Check GATEWAY_SRC env var (for testing and custom setups)
	if src := os.Getenv("GATEWAY_SRC"); src != "" {
		if info, err := os.Stat(src); err == nil && info.IsDir() {
			return src
		}
	}

	// Check relative to project dir (development mode)
	projectGateway := filepath.Join(g.Dir, "gateway")
	if info, err := os.Stat(projectGateway); err == nil && info.IsDir() {
		return projectGateway
	}

	// Check relative to executable (installed mode)
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		exeGateway := filepath.Join(exeDir, "gateway")
		if info, err := os.Stat(exeGateway); err == nil && info.IsDir() {
			return exeGateway
		}
	}

	return ""
}
