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

// FeatureContributions holds what a feature adds to the build.
type FeatureContributions struct {
	Commands        []string // RUN commands for Dockerfile
	EntrypointHooks []string // scripts to run on container start (source paths)
	Volumes         []string // named volumes (e.g., "name:/path")
	HomeOverride    string   // directory to copy into home on start
}

// ResolveFeature finds a feature plugin by name and returns its contributions
// based on the user's config for that feature.
// Resolution order: local ./ext/plugins/<name>/ → embedded core plugins.
func ResolveFeature(projectDir string, name string, userConfig map[string]any) (*FeatureContributions, error) {
	// Verify feature.yaml exists (local ext/plugins or embedded core)
	if !featureExists(projectDir, name) {
		return nil, fmt.Errorf("unknown feature %q: no feature.yaml found in ./ext/plugins/%s/ or built-in plugins", name, name)
	}

	// Extract contributions from user config
	contrib := &FeatureContributions{}

	if cmds, ok := userConfig["commands"]; ok {
		if arr, ok := cmds.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					contrib.Commands = append(contrib.Commands, s)
				}
			}
		}
	}

	if hooks, ok := userConfig["entrypoint_hooks"]; ok {
		if arr, ok := hooks.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					contrib.EntrypointHooks = append(contrib.EntrypointHooks, s)
				}
			}
		}
	}

	if vols, ok := userConfig["runtime_volumes"]; ok {
		if arr, ok := vols.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					contrib.Volumes = append(contrib.Volumes, s)
				}
			}
		}
	}

	if ho, ok := userConfig["home_override"]; ok {
		if s, ok := ho.(string); ok {
			contrib.HomeOverride = s
		}
	}

	return contrib, nil
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
