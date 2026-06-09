package v1

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/envvar"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

// GatewayConfigOutput is the merged gateway configuration for rendering.
type GatewayConfigOutput struct {
	Services    []GatewayServiceOutput
	AuthHeaders []AuthHeaderEntry // auth-header entries to bake into config.yaml
	PublicURL   string            // gateway public URL for callbacks
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
	Listen      string              `yaml:"listen"`
	DNSListen   string              `yaml:"dns_listen"`
	MITMDomains []string            `yaml:"mitm_domains"`
	AuthHeaders []authHeaderRuntime `yaml:"auth_headers,omitempty"`
	HealthAddr  string              `yaml:"health_addr,omitempty"`
	PublicURL   string              `yaml:"public_url,omitempty"`
}

// authHeaderRuntime is the runtime representation of an auth-header entry in config.yaml.
type authHeaderRuntime struct {
	Domain string `yaml:"domain"`
	Header string `yaml:"header"`
	Value  string `yaml:"value"`
}

// BuildGatewayConfig merges user gateway config with plugin contributions.
func BuildGatewayConfig(cfg *config.Config, contribs *plugin.Contributions) *GatewayConfigOutput {
	publicURL := cfg.Gateway.PublicURL
	// Default to localhost:8080 when no public_url configured (local dev)
	if publicURL == "" {
		publicURL = "http://localhost:8080"
	}

	out := &GatewayConfigOutput{
		PublicURL: publicURL,
	}

	// User-declared services
	for _, svc := range cfg.Gateway.Services {
		out.Services = append(out.Services, GatewayServiceOutput{
			URL:     svc.URL,
			Network: svc.Network,
			Headers: svc.Headers,
		})
		domain := extractDomain(svc.URL)
		// Collect auth-header entries from declared headers
		for header, value := range svc.Headers {
			ev, valueFormat := envvar.ParseTemplate(value)
			out.AuthHeaders = append(out.AuthHeaders, AuthHeaderEntry{
				Domain:      domain,
				Header:      header,
				EnvVar:      ev,
				ValueFormat: valueFormat,
			})
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
			// Collect auth-header entries from plugin-contributed headers
			for header, value := range svc.Headers {
				ev, valueFormat := envvar.ParseTemplate(value)
				out.AuthHeaders = append(out.AuthHeaders, AuthHeaderEntry{
					Domain:      domain,
					Header:      header,
					EnvVar:      ev,
					ValueFormat: valueFormat,
				})
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
		PublicURL: gwCfg.PublicURL,
	}

	for _, svc := range gwCfg.Services {
		domain := extractDomain(svc.URL)
		if domain == "" {
			continue
		}
		rc.MITMDomains = append(rc.MITMDomains, domain)
	}

	// Convert auth-header entries to runtime format with resolved env var values.
	for _, ah := range gwCfg.AuthHeaders {
		if ah.EnvVar == "" {
			continue // skip entries with no env var reference
		}
		envVal := os.Getenv(ah.EnvVar)
		if envVal == "" {
			continue // skip entries where env var is not set
		}
		resolvedValue := strings.Replace(ah.ValueFormat, "${value}", envVal, 1)
		rc.AuthHeaders = append(rc.AuthHeaders, authHeaderRuntime{
			Domain: ah.Domain,
			Header: ah.Header,
			Value:  resolvedValue,
		})
	}

	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshal gateway config: %w", err)
	}

	configPath := filepath.Join(buildDir, "config.yaml")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write gateway config: %w", err)
	}

	return nil
}

// extractDomain extracts the hostname from a URL or host:port string.
func extractDomain(rawURL string) string {
	// If it looks like a URL with a scheme, parse it
	if strings.Contains(rawURL, "://") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	// Plain host:port — extract host
	host, _, err := net.SplitHostPort(rawURL)
	if err != nil {
		// No port, treat as bare hostname
		return rawURL
	}
	return host
}
