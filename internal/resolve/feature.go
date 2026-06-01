package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sandbox "github.com/donbader/agent-sandbox"
	"gopkg.in/yaml.v3"
)

// FeatureConfig represents a parsed feature.yaml.
type FeatureConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ResolveFeature finds a feature plugin by name and returns its contributions.
// Supports named instances via "plugin/instance" syntax (e.g. "static-header/kiro").
// Resolution order: registered plugin → local ext/plugins/ → embedded core.
func ResolveFeature(projectDir string, name string, userConfig map[string]any) (*FeatureContributions, error) {
	// Support named instances: "static-header/kiro" → plugin "static-header", instance "kiro"
	pluginName := name
	if idx := strings.IndexByte(name, '/'); idx >= 0 {
		pluginName = name[:idx]
	}

	// Check if plugin is registered (has implementation code)
	if plugin, ok := registry[pluginName]; ok {
		contrib, err := plugin.Resolve(projectDir, userConfig)
		if err != nil {
			return nil, err
		}
		// Use the full name (with instance suffix) for uniqueness
		contrib.Name = name
		return contrib, nil
	}

	// Fallback: verify feature.yaml exists (for future external plugins without Go code)
	if !featureExists(projectDir, pluginName) {
		return nil, fmt.Errorf("unknown feature %q: no registered plugin or feature.yaml found", pluginName)
	}

	return nil, fmt.Errorf("feature %q has no registered implementation", pluginName)
}

// featureExists checks if a feature plugin exists in local ext/plugins or embedded core.
func featureExists(projectDir string, name string) bool {
	// Check local ext/plugins
	localPath := filepath.Join(projectDir, "ext", "plugins", name, "feature.yaml")
	if _, err := os.Stat(localPath); err == nil {
		return true
	}

	// Check embedded core plugins
	embeddedPath := fmt.Sprintf("internal/plugins/%s/feature.yaml", name)
	if _, err := sandbox.CorePlugins.ReadFile(embeddedPath); err == nil {
		return true
	}

	return false
}

// loadFeatureYAML loads and parses a feature.yaml (for validation).
func loadFeatureYAML(data []byte) (*FeatureConfig, error) {
	var fc FeatureConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, err
	}
	return &fc, nil
}
