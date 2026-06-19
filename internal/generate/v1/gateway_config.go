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
	Services         []GatewayServiceOutput
	AuthHeaders      []AuthHeaderEntry // auth-header entries to bake into config.yaml
	EgressRules      []config.EgressRule
	MiddlewareDomains []string // domains from plugin middlewares that require MITM
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
	EgressRules []egressRuleRuntime `yaml:"egress_rules,omitempty"`
	HealthAddr  string              `yaml:"health_addr,omitempty"`
}

// authHeaderRuntime is the runtime representation of an auth-header entry in config.yaml.
type authHeaderRuntime struct {
	Domain string `yaml:"domain"`
	Header string `yaml:"header"`
	Value  string `yaml:"value"`
}

// egressRuleRuntime is the runtime representation of an egress rule in config.yaml.
type egressRuleRuntime struct {
	Hosts     []string          `yaml:"hosts"`
	Deny      bool              `yaml:"deny,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	DenyPaths []string          `yaml:"deny_paths,omitempty"`
	Target    string            `yaml:"target,omitempty"`
}

// BuildGatewayConfig merges user gateway config with plugin contributions.
func BuildGatewayConfig(cfg *config.Config, contribs *plugin.Contributions) *GatewayConfigOutput {
	out := &GatewayConfigOutput{}

	// Determine effective egress rules
	if len(cfg.Gateway.Egress) > 0 {
		out.EgressRules = cfg.Gateway.Egress
	} else if len(cfg.Gateway.Services) > 0 {
		// Legacy mode: convert services to egress rules internally
		out.EgressRules = config.MigrateServicesToEgress(cfg.Gateway.Services)
	}

	// Process egress rules for auth headers and services
	for _, rule := range out.EgressRules {
		if rule.Deny || len(rule.Headers) == 0 {
			continue
		}
		for _, host := range rule.Hosts {
			if host == "*" {
				continue
			}
			out.Services = append(out.Services, GatewayServiceOutput{
				URL:     "https://" + host,
				Headers: rule.Headers,
			})
			for header, value := range rule.Headers {
				ev, valueFormat := envvar.ParseTemplate(value)
				out.AuthHeaders = append(out.AuthHeaders, AuthHeaderEntry{
					Domain:      host,
					Header:      header,
					EnvVar:      ev,
					ValueFormat: valueFormat,
				})
			}
		}
	}

	// Plugin-contributed services (still supported — plugins use the old API)
	if contribs != nil {
		for _, svc := range contribs.Gateway.Services {
			out.Services = append(out.Services, GatewayServiceOutput{
				URL:     svc.URL,
				Network: svc.Network,
				Headers: svc.Headers,
			})
			domain := extractDomain(svc.URL)
			for header, value := range svc.Headers {
				ev, valueFormat := envvar.ParseTemplate(value)
				out.AuthHeaders = append(out.AuthHeaders, AuthHeaderEntry{
					Domain:      domain,
					Header:      header,
					EnvVar:      ev,
					ValueFormat: valueFormat,
				})
			}
			// Auto-add plugin domains to egress rules (allowed implicitly)
			if domain != "" {
				out.EgressRules = insertPluginDomain(out.EgressRules, domain)
			}
		}

		// Collect domains from plugin middlewares — these require MITM to intercept
		for _, mw := range contribs.Gateway.Middlewares {
			out.MiddlewareDomains = append(out.MiddlewareDomains, mw.Domains...)
		}
	}

	return out
}

// insertPluginDomain ensures a plugin-contributed domain is allowed in egress rules.
// If there's already a matching rule, do nothing. Otherwise insert before catch-all.
func insertPluginDomain(rules []config.EgressRule, domain string) []config.EgressRule {
	// Check if already covered
	m := config.MatchHost(rules, domain)
	if m.Matched && !m.Denied {
		return rules
	}

	// Insert before the last rule if it's a catch-all, otherwise append
	newRule := config.EgressRule{Hosts: []string{domain}}
	if len(rules) > 0 && len(rules[len(rules)-1].Hosts) == 1 && rules[len(rules)-1].Hosts[0] == "*" {
		// Insert before catch-all
		rules = append(rules[:len(rules)-1], newRule, rules[len(rules)-1])
	} else {
		rules = append(rules, newRule)
	}
	return rules
}

// WriteGatewayRuntimeConfig renders the gateway runtime config.yaml into the build dir.
func WriteGatewayRuntimeConfig(buildDir string, gwCfg *GatewayConfigOutput) error {
	rc := gatewayRuntimeConfig{
		Listen:    ":8443",
		DNSListen: ":53",
	}

	// Collect MITM domains from egress rules that need MITM
	mitmSet := make(map[string]bool)
	for _, rule := range gwCfg.EgressRules {
		if !rule.NeedsMITM() {
			continue
		}
		for _, host := range rule.Hosts {
			if host != "*" && !strings.Contains(host, "/") {
				mitmSet[host] = true
			}
		}
	}

	// Also add domains from plugin-contributed services
	for _, svc := range gwCfg.Services {
		domain := extractDomain(svc.URL)
		if domain != "" {
			mitmSet[domain] = true
		}
	}

	// Add domains from plugin middlewares
	for _, domain := range gwCfg.MiddlewareDomains {
		if domain != "" && domain != "*" {
			mitmSet[domain] = true
		}
	}

	for domain := range mitmSet {
		rc.MITMDomains = append(rc.MITMDomains, domain)
	}

	// Convert auth-header entries to runtime format
	for _, ah := range gwCfg.AuthHeaders {
		if ah.EnvVar == "" {
			continue
		}
		value := strings.Replace(ah.ValueFormat, "${value}", "${"+ah.EnvVar+"}", 1)
		rc.AuthHeaders = append(rc.AuthHeaders, authHeaderRuntime{
			Domain: ah.Domain,
			Header: ah.Header,
			Value:  value,
		})
	}

	// Write egress rules to runtime config
	for _, rule := range gwCfg.EgressRules {
		rc.EgressRules = append(rc.EgressRules, egressRuleRuntime{
			Hosts:     rule.Hosts,
			Deny:      rule.Deny,
			Headers:   rule.Headers,
			DenyPaths: rule.DenyPaths,
			Target:    rule.Target,
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
