// Package githubpat implements the github-pat feature plugin.
// It injects a GitHub personal access token into requests to GitHub API domains
// via the gateway's MITM proxy.
package githubpat

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the github-pat plugin.
type Config struct {
	Domains []string `yaml:"domains" schema:"GitHub API domains to intercept" default:"api.github.com,github.com"`
}

func init() {
	resolve.Register("github-pat", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		domains := cfg.Domains
		if len(domains) == 0 {
			domains = []string{"api.github.com", "github.com"}
		}
		return &resolve.FeatureContributions{
			Name:        "github-pat",
			MITMDomains: domains,
			EnvVars:     []string{"GITHUB_TOKEN"},
			Rewriters: []resolve.RewriterConfig{
				{
					Type:        "auth-header",
					Domains:     domains,
					EnvVar:      "GITHUB_TOKEN",
					Header:      "Authorization",
					ValueFormat: "token ${value}",
				},
			},
		}, nil
	})
}
