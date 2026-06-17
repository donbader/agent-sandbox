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
}

// authHeaderRuntime is the runtime representation of an auth-header entry in config.yaml.
type authHeaderRuntime struct {
	Domain string `yaml:"domain"`
	Header string `yaml:"header"`
	Value  string `yaml:"value"`
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
	}

	for _, svc := range gwCfg.Services {
		domain := extractDomain(svc.URL)
		if domain == "" {
			continue
		}
		rc.MITMDomains = append(rc.MITMDomains, domain)
	}

	// Convert auth-header entries to runtime format, preserving ${VAR} for runtime resolution.
	for _, ah := range gwCfg.AuthHeaders {
		if ah.EnvVar == "" {
			continue // skip entries with no env var reference
		}
		// Write the value format with ${ENV_VAR} for runtime resolution by the gateway.
		value := strings.Replace(ah.ValueFormat, "${value}", "${"+ah.EnvVar+"}", 1)
		rc.AuthHeaders = append(rc.AuthHeaders, authHeaderRuntime{
			Domain: ah.Domain,
			Header: ah.Header,
			Value:  value,
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
