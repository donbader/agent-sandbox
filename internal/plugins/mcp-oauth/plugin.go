// Package mcpoauth implements the mcp-oauth feature plugin.
// It enables OAuth Bearer token injection for MCP server domains via the gateway's
// MITM proxy. Tokens are read from a file on disk (created by the setup flow) and
// automatically refreshed when expired.
package mcpoauth

import (
	"fmt"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Config defines the typed configuration for the mcp-oauth plugin.
type Config struct {
	Domains   []string `yaml:"domains" schema:"MCP server domains to intercept" required:"true" examples:"mcp.notion.com,mcp.slack.com"`
	TokenFile string   `yaml:"token_file" schema:"Path to stored OAuth token JSON file (inside gateway container)" required:"true" examples:"/data/oauth-tokens/notion.json"`
}

func init() {
	resolve.Register("mcp-oauth", func(_ string, cfg Config) (*resolve.FeatureContributions, error) {
		if len(cfg.Domains) == 0 {
			return nil, fmt.Errorf("mcp-oauth: missing required option 'domains'")
		}
		if cfg.TokenFile == "" {
			return nil, fmt.Errorf("mcp-oauth: missing required option 'token_file'")
		}

		return &resolve.FeatureContributions{
			Name:        "mcp-oauth",
			MITMDomains: cfg.Domains,
			Rewriters: []resolve.RewriterConfig{
				{
					Type:      "oauth",
					Domains:   cfg.Domains,
					TokenFile: cfg.TokenFile,
				},
			},
		}, nil
	})
}
