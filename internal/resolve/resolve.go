// Package resolve handles plugin resolution — finding runtime.yaml from local
// project directory or embedded defaults.
package resolve

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed embedded/codex/runtime.yaml
var embeddedPlugins embed.FS

// RuntimeConfig represents a parsed runtime.yaml.
type RuntimeConfig struct {
	Name      string   `yaml:"name"`
	BaseImage string   `yaml:"base_image"`
	Install   []string `yaml:"install"`
	Cmd       []string `yaml:"cmd"`
	User      string   `yaml:"user"`
}

// ResolveRuntime finds and parses a runtime plugin by name.
// Resolution order: local ./plugins/<name>/runtime.yaml → embedded defaults.
func ResolveRuntime(projectDir string, name string) (*RuntimeConfig, error) {
	// 1. Try local plugins directory
	localPath := filepath.Join(projectDir, "plugins", name, "runtime.yaml")
	if data, err := os.ReadFile(localPath); err == nil {
		return parseRuntime(data, localPath)
	}

	// 2. Try embedded defaults
	embeddedPath := fmt.Sprintf("embedded/%s/runtime.yaml", name)
	if data, err := embeddedPlugins.ReadFile(embeddedPath); err == nil {
		return parseRuntime(data, embeddedPath)
	}

	return nil, fmt.Errorf("unknown runtime %q: no runtime.yaml found in ./plugins/%s/ or built-in plugins", name, name)
}

// ResolveInlineRuntime parses an inline runtime definition from agent.yaml.
func ResolveInlineRuntime(inline map[string]any) (*RuntimeConfig, error) {
	// Re-marshal and unmarshal to reuse YAML parsing
	data, err := yaml.Marshal(inline)
	if err != nil {
		return nil, fmt.Errorf("invalid inline runtime: %w", err)
	}

	rc, err := parseRuntime(data, "inline")
	if err != nil {
		return nil, err
	}

	if rc.BaseImage == "" {
		return nil, fmt.Errorf("inline runtime: base_image is required")
	}

	return rc, nil
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
