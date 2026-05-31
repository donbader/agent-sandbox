// Package customruntime implements the custom-runtime feature plugin.
// It provides custom packages, startup hooks, persistent volumes, and home override.
package customruntime

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the custom-runtime plugin.
type Config struct {
	Commands        []string `yaml:"commands" schema:"Additional RUN commands for the Dockerfile"`
	EntrypointHooks []string `yaml:"entrypoint_hooks" schema:"Scripts to run on container start (paths relative to agent.yaml)"`
	RuntimeVolumes  []string `yaml:"runtime_volumes" schema:"Named volumes (e.g., agent-home:/home/agent)"`
	HomeOverride    string   `yaml:"home_override" schema:"Directory to copy into home on start"`
	Env             []string `yaml:"env" schema:"Environment variables to pass to the container"`
}

func init() {
	resolve.Register("custom-runtime", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		return &resolve.FeatureContributions{
			Commands:        cfg.Commands,
			EntrypointHooks: cfg.EntrypointHooks,
			Volumes:         cfg.RuntimeVolumes,
			HomeOverride:    cfg.HomeOverride,
			EnvVars:         cfg.Env,
		}, nil
	})
}
