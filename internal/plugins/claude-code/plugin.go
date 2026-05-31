// Package claudecode implements the claude-code feature plugin.
// It installs the Claude Code CLI and configures MITM for api.anthropic.com.
package claudecode

import (
	"fmt"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the claude-code plugin.
type Config struct {
	Version string `yaml:"version" schema:"Claude Code version to install" default:"latest" examples:"latest,0.2.0"`
}

func init() {
	resolve.Register("claude-code", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		version := cfg.Version
		if version == "" {
			version = "latest"
		}
		return &resolve.FeatureContributions{
			Name: "claude-code",
			Commands: []string{
				"apt-get update && apt-get install -y --no-install-recommends nodejs npm",
				fmt.Sprintf("npm install -g @anthropic-ai/claude-code@%s @agentclientprotocol/claude-agent-acp@latest", version),
			},
			MITMDomains: []string{"api.anthropic.com"},
			EnvVars:     []string{"ANTHROPIC_API_KEY"},
		}, nil
	})
}
