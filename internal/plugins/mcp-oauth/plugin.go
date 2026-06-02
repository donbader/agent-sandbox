// Package mcpoauth implements the mcp-oauth feature plugin.
// It enables OAuth Bearer token injection for remote MCP servers via the gateway's
// MITM proxy, and provides an interactive /oauth command in the channel-manager
// for users to authorize connections via paste-back OAuth flow.
package mcpoauth

import (
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// ProviderConfig defines a single OAuth MCP provider.
type ProviderConfig struct {
	MCPURL       string `yaml:"mcp_url" schema:"MCP server URL for OAuth discovery" required:"true" examples:"https://mcp.notion.com/mcp"`
	ClientID     string `yaml:"client_id,omitempty" schema:"Pre-registered OAuth client ID (optional — uses DCR if omitted)"`
	ClientSecret string `yaml:"client_secret,omitempty" schema:"Pre-registered OAuth client secret"`
}

// Config defines the typed configuration for the mcp-oauth plugin.
type Config struct {
	Providers map[string]ProviderConfig `yaml:"providers" schema:"OAuth MCP providers to configure" required:"true"`
	TokenDir  string                    `yaml:"token_dir,omitempty" schema:"Directory for OAuth token files" examples:"/data/oauth-tokens"`
}

func init() {
	resolve.Register("mcp-oauth", func(projectDir string, cfg Config) (*resolve.FeatureContributions, error) {
		if len(cfg.Providers) == 0 {
			return nil, fmt.Errorf("mcp-oauth: at least one provider must be configured")
		}

		tokenDir := cfg.TokenDir
		if tokenDir == "" {
			tokenDir = "/data/oauth-tokens"
		}

		var domains []string
		var rewriters []resolve.RewriterConfig

		// Build per-provider rewriters and collect MITM domains.
		for name, p := range cfg.Providers {
			if p.MCPURL == "" {
				return nil, fmt.Errorf("mcp-oauth: provider %q missing required 'mcp_url'", name)
			}
			u, err := url.Parse(p.MCPURL)
			if err != nil {
				return nil, fmt.Errorf("mcp-oauth: provider %q has invalid mcp_url: %w", name, err)
			}
			domain := u.Hostname()
			tokenFile := filepath.Join(tokenDir, name+".json")

			domains = append(domains, domain)
			rewriters = append(rewriters, resolve.RewriterConfig{
				Type:      "oauth",
				Domains:   []string{domain},
				TokenFile: tokenFile,
			})
		}

		// Build channel-manager config for the /oauth command plugin.
		providersConfig := map[string]any{}
		for name, p := range cfg.Providers {
			entry := map[string]any{
				"mcp_url": p.MCPURL,
			}
			if p.ClientID != "" {
				entry["client_id"] = p.ClientID
			}
			if p.ClientSecret != "" {
				entry["client_secret"] = p.ClientSecret
			}
			providersConfig[name] = entry
		}

		return &resolve.FeatureContributions{
			Name:        "mcp-oauth",
			MITMDomains: domains,
			Rewriters:   rewriters,
			Volumes:     []string{"oauth-tokens:" + tokenDir},
			ChannelConfig: map[string]any{
				"oauth": map[string]any{
					"providers": providersConfig,
					"token_dir": tokenDir,
				},
			},
			CommandPluginDir: "command",
		}, nil
	})
}
