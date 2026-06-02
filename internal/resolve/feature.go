package resolve

import (
	"fmt"
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
// Resolution order: registered plugin → embedded core.
func ResolveFeature(projectDir string, plugin string, instanceName string, userConfig map[string]any) (*FeatureContributions, error) {
	// Check if plugin is registered (has implementation code)
	if p, ok := registry[plugin]; ok {
		contrib, err := p.Resolve(projectDir, userConfig)
		if err != nil {
			return nil, err
		}
		contrib.Name = instanceName
		// Expand relative CommandPluginDir to full embedded path using plugin name.
		if contrib.CommandPluginDir != "" && !strings.HasPrefix(contrib.CommandPluginDir, "internal/") {
			contrib.CommandPluginDir = fmt.Sprintf("internal/plugins/%s/%s", plugin, contrib.CommandPluginDir)
		}
		return contrib, nil
	}

	// Fallback: verify feature.yaml exists in embedded core
	if !featureExists(plugin) {
		return nil, fmt.Errorf("unknown feature %q: no registered plugin or feature.yaml found", plugin)
	}

	return nil, fmt.Errorf("feature %q has no registered implementation", plugin)
}

// featureExists checks if a feature plugin exists in embedded core.
func featureExists(name string) bool {
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
