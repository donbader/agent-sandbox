package resolve

import (
	"fmt"
	"os"
	"path/filepath"

	sandbox "github.com/donbader/agent-sandbox"
	"gopkg.in/yaml.v3"
)

// FeatureConfig represents a parsed feature.yaml.
type FeatureConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ResolveFeature finds a feature plugin by name and returns its contributions.
// Resolution order: registered plugin → local ext/plugins/ → embedded core.
func ResolveFeature(projectDir string, plugin string, instanceName string, userConfig map[string]any) (*FeatureContributions, error) {
	// Check if plugin is registered (has implementation code)
	if p, ok := registry[plugin]; ok {
		contrib, err := p.Resolve(projectDir, userConfig)
		if err != nil {
			return nil, err
		}
		contrib.Name = instanceName
		return contrib, nil
	}

	// Fallback: verify feature.yaml exists (for future external plugins without Go code)
	if !featureExists(projectDir, plugin) {
		return nil, fmt.Errorf("unknown feature %q: no registered plugin or feature.yaml found", plugin)
	}

	return nil, fmt.Errorf("feature %q has no registered implementation", plugin)
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
