// Package customruntime implements the custom-runtime feature plugin.
// It provides custom packages, startup hooks, persistent volumes, and home override.
package customruntime

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

func init() {
	resolve.RegisterFeature(&Plugin{})
}

// Plugin implements resolve.FeaturePlugin for custom-runtime.
type Plugin struct{}

func (p *Plugin) Name() string { return "custom-runtime" }

// Resolve extracts contributions from user config in agent.yaml.
func (p *Plugin) Resolve(projectDir string, userConfig map[string]any) (*resolve.FeatureContributions, error) {
	contrib := &resolve.FeatureContributions{}

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
