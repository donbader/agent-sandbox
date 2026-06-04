package v1

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

// GatewayConfigOutput is the merged gateway configuration for rendering.
type GatewayConfigOutput struct {
	Services    []GatewayServiceOutput
	Middlewares []string // paths to custom .go files to copy
}

// GatewayServiceOutput represents a single gateway service entry in the output.
type GatewayServiceOutput struct {
	URL     string
	Network string
	Headers map[string]string
}

// gatewayRuntimeConfig matches the proxy.Config struct in core/gateway.
type gatewayRuntimeConfig struct {
	Listen      string                    `yaml:"listen"`
	DNSListen   string                    `yaml:"dns_listen"`
	MITMDomains []string                  `yaml:"mitm_domains"`
	Rewriters   []gatewayRewriterConfig   `yaml:"rewriters,omitempty"`
	HealthAddr  string                    `yaml:"health_addr,omitempty"`
}

type gatewayRewriterConfig struct {
	Type        string   `yaml:"type"`
	Domains     []string `yaml:"domains"`
	EnvVar      string   `yaml:"env_var,omitempty"`
	Header      string   `yaml:"header,omitempty"`
	ValueFormat string   `yaml:"value_format,omitempty"`
}

// BuildGatewayConfig merges user gateway config with plugin contributions.
func BuildGatewayConfig(cfg *config.V1Config, contribs *plugin.Contributions) *GatewayConfigOutput {
	out := &GatewayConfigOutput{}

	// User-declared services
	for _, svc := range cfg.Gateway.Services {
		out.Services = append(out.Services, GatewayServiceOutput{
			URL:     svc.URL,
			Network: svc.Network,
			Headers: svc.Headers,
		})
		for _, mw := range svc.Middlewares {
			if mw.Custom != "" {
				out.Middlewares = append(out.Middlewares, mw.Custom)
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
			for _, mw := range svc.Middlewares {
				if mw.Custom != "" {
					out.Middlewares = append(out.Middlewares, mw.Custom)
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

		// For each header, create an auth-header rewriter
		for header, value := range svc.Headers {
			// Value might be "Bearer ${ENV_VAR}" — extract env var reference
			envVar, valueFormat := parseHeaderValue(value)
			rc.Rewriters = append(rc.Rewriters, gatewayRewriterConfig{
				Type:        "auth-header",
				Domains:     []string{domain},
				Header:      header,
				EnvVar:      envVar,
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

// parseHeaderValue extracts env var name and value format from a header value template.
// Examples:
//   "Bearer ${TOKEN}" → envVar="TOKEN", valueFormat="Bearer ${value}"
//   "${API_KEY}" → envVar="API_KEY", valueFormat="${value}"
//   "static-value" → envVar="", valueFormat=""
func parseHeaderValue(value string) (envVar, valueFormat string) {
	// Find ${...} pattern
	start := -1
	for i := 0; i < len(value)-1; i++ {
		if value[i] == '$' && value[i+1] == '{' {
			start = i
			break
		}
	}
	if start == -1 {
		return "", ""
	}
	end := -1
	for i := start + 2; i < len(value); i++ {
		if value[i] == '}' {
			end = i
			break
		}
	}
	if end == -1 {
		return "", ""
	}

	envVar = value[start+2 : end]
	// Replace the ${VAR} with ${value} for the gateway's value_format
	valueFormat = value[:start] + "${value}" + value[end+1:]
	return envVar, valueFormat
}
