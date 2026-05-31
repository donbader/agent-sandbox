// Package staticheader implements the static-header feature plugin.
// It injects a configurable HTTP header into requests to specified domains
// via the gateway's MITM proxy.
package staticheader

import (
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the static-header plugin.
type Config struct {
	Domains     []string `yaml:"domains" schema:"Domains to intercept" required:"true"`
	Header      string   `yaml:"header" schema:"Header name to inject" required:"true" examples:"Authorization,X-API-Key"`
	ValueFormat string   `yaml:"value_format" schema:"Header value format (use ${value} for env var substitution)" default:"${value}" examples:"Bearer ${value},token ${value}"`
	EnvVar      string   `yaml:"env_var" schema:"Environment variable holding the secret" required:"true"`
}

func init() {
	resolve.Register("static-header", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		valueFormat := cfg.ValueFormat
		if valueFormat == "" {
			valueFormat = "${value}"
		}
		return &resolve.FeatureContributions{
			Name:        "static-header",
			MITMDomains: cfg.Domains,
			EnvVars:     []string{cfg.EnvVar},
			Rewriters: []resolve.RewriterConfig{
				{
					Type:        "auth-header",
					Domains:     cfg.Domains,
					EnvVar:      cfg.EnvVar,
					Header:      cfg.Header,
					ValueFormat: valueFormat,
				},
			},
		}, nil
	})
}
