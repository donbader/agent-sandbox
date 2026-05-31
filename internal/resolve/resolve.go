// Package resolve handles plugin resolution — finding runtime.yaml from local
// project directory or embedded defaults.
package resolve

import (
	"fmt"
	"os"
	"path/filepath"

	sandbox "github.com/donbader/agent-sandbox"
	"gopkg.in/yaml.v3"
)

// RuntimeConfig represents a parsed runtime.yaml.
type RuntimeConfig struct {
	Name      string   `yaml:"name"`
	BaseImage string   `yaml:"base_image"`
	Install   []string `yaml:"install"`
	Cmd       []string `yaml:"cmd"`
	Ports     []string `yaml:"ports"`
	User      string   `yaml:"user"`
}

// ResolveRuntime finds and parses a runtime plugin by name.
// Resolution order: local ./ext/plugins/<name>/runtime.yaml → embedded defaults.
func ResolveRuntime(projectDir string, name string) (*RuntimeConfig, error) {
	// 1. Try local ext/plugins directory
	localPath := filepath.Join(projectDir, "ext", "plugins", name, "runtime.yaml")
	if data, err := os.ReadFile(localPath); err == nil {
		return parseRuntime(data, localPath)
	}

	// 2. Try embedded defaults
	embeddedPath := fmt.Sprintf("internal/plugins/%s/runtime.yaml", name)
	if data, err := sandbox.CorePlugins.ReadFile(embeddedPath); err == nil {
		return parseRuntime(data, embeddedPath)
	}

	return nil, fmt.Errorf("unknown runtime %q: no runtime.yaml found in ./ext/plugins/%s/ or built-in plugins", name, name)
}


func parseRuntime(data []byte, source string) (*RuntimeConfig, error) {
	var rc RuntimeConfig
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", source, err)
	}

	if rc.BaseImage == "" {
		return nil, fmt.Errorf("%s: base_image is required", source)
	}

	// Default user
	if rc.User == "" {
		rc.User = "agent"
	}

	// Default cmd
	if len(rc.Cmd) == 0 {
		rc.Cmd = []string{"sleep", "infinity"}
	}

	return &rc, nil
}
