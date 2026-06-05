package v1

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/envvar"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

// GatewayConfigOutput is the merged gateway configuration for rendering.
type GatewayConfigOutput struct {
	Services    []GatewayServiceOutput
	Middlewares []MiddlewareRef   // custom .go files to copy with domain scope
	AuthHeaders []AuthHeaderEntry // auth-header entries to generate as .go files
}

// MiddlewareRef associates a custom middleware file with its target domains.
type MiddlewareRef struct {
	Path    string   // relative or absolute path to .go file
	Domains []string // domains this middleware applies to
}

// AuthHeaderEntry describes an auth-header middleware to generate at build time.
type AuthHeaderEntry struct {
	Domain      string
	Header      string
	EnvVar      string
	ValueFormat string
}

// GatewayServiceOutput represents a single gateway service entry in the output.
type GatewayServiceOutput struct {
	URL     string
	Network string
	Headers map[string]string
}

// gatewayRuntimeConfig matches the proxy.Config struct in core/gateway.
type gatewayRuntimeConfig struct {
	Listen      string   `yaml:"listen"`
	DNSListen   string   `yaml:"dns_listen"`
	MITMDomains []string `yaml:"mitm_domains"`
	HealthAddr  string   `yaml:"health_addr,omitempty"`
}

// BuildGatewayConfig merges user gateway config with plugin contributions.
func BuildGatewayConfig(cfg *config.Config, contribs *plugin.Contributions) *GatewayConfigOutput {
	out := &GatewayConfigOutput{}

	// User-declared services
	for _, svc := range cfg.Gateway.Services {
		out.Services = append(out.Services, GatewayServiceOutput{
			URL:     svc.URL,
			Network: svc.Network,
			Headers: svc.Headers,
		})
		domain := extractDomain(svc.URL)
		for _, mw := range svc.Middlewares {
			if mw.Custom != "" {
				out.Middlewares = append(out.Middlewares, MiddlewareRef{
					Path:    mw.Custom,
					Domains: []string{domain},
				})
			}
		}
	}

	// Plugin-contributed services
	if contribs != nil {
		for _, svc := range contribs.Gateway.Services {
			out.Services = append(out.Services, GatewayServiceOutput{
				URL:     svc.URL,
				Network: svc.Network,
				Headers: svc.Headers,
			})
			domain := extractDomain(svc.URL)
			for _, mw := range svc.Middlewares {
				if mw.Custom != "" {
					out.Middlewares = append(out.Middlewares, MiddlewareRef{
						Path:    mw.Custom,
						Domains: []string{domain},
					})
				}
			}
		}
	}

	return out
}

// WriteGatewayRuntimeConfig renders the gateway runtime config.yaml into the build dir.
func WriteGatewayRuntimeConfig(buildDir string, gwCfg *GatewayConfigOutput) error {
	rc := gatewayRuntimeConfig{
		Listen:    ":8443",
		DNSListen: ":53",
	}

	for _, svc := range gwCfg.Services {
		domain := extractDomain(svc.URL)
		if domain == "" {
			continue
		}
		rc.MITMDomains = append(rc.MITMDomains, domain)

		// Collect auth-header entries (generated as .go files, not runtime YAML)
		for header, value := range svc.Headers {
			ev, valueFormat := envvar.ParseTemplate(value)
			gwCfg.AuthHeaders = append(gwCfg.AuthHeaders, AuthHeaderEntry{
				Domain:      domain,
				Header:      header,
				EnvVar:      ev,
				ValueFormat: valueFormat,
			})
		}
	}

	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshal gateway config: %w", err)
	}

	configDir := filepath.Join(buildDir, "gateway-src")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create gateway-src dir: %w", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write gateway config: %w", err)
	}

	return nil
}

// extractDomain extracts the hostname from a URL.
func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
