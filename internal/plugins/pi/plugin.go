// Package pi implements the pi feature plugin.
// It installs the Pi CLI and configures MITM for the selected API providers.
package pi

import (
	"fmt"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the pi plugin.
type Config struct {
	Version   string   `yaml:"version" schema:"Pi version to install" default:"latest"`
	Providers []string `yaml:"providers" schema:"API providers to enable MITM for" examples:"anthropic,openai,google" default:"anthropic"`
}

var domainMap = map[string]string{
	"anthropic": "api.anthropic.com",
	"openai":    "api.openai.com",
	"google":    "generativelanguage.googleapis.com",
}

var envMap = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"google":    "GOOGLE_API_KEY",
}

func init() {
	resolve.Register("pi", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		version := cfg.Version
		if version == "" {
			version = "latest"
		}

		providers := cfg.Providers
		if len(providers) == 0 {
			providers = []string{"anthropic"}
		}

		var domains []string
		var envVars []string
		for _, p := range providers {
			if d, ok := domainMap[p]; ok {
				domains = append(domains, d)
			}
			if e, ok := envMap[p]; ok {
				envVars = append(envVars, e)
			}
		}

		return &resolve.FeatureContributions{
			Name: "pi",
			Commands: []string{
				"apt-get update && apt-get install -y --no-install-recommends nodejs npm git",
				fmt.Sprintf("npm install -g @earendil-works/pi-coding-agent@%s pi-acp@latest", version),
			},
			MITMDomains: domains,
			EnvVars:     envVars,
		}, nil
	})
}
