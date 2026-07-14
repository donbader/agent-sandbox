package v1

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/envvar"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

// GatewayConfigOutput is the merged gateway configuration for rendering.
type GatewayConfigOutput struct {
	AuthHeaders []AuthHeaderEntry // auth-header entries to bake into config.yaml
	EgressRules []config.EgressRule
	Ingress     []plugin.IngressRule // TCP port forwards from gateway to agent
}

// AuthHeaderEntry describes an auth-header middleware to generate at build time.
type AuthHeaderEntry struct {
	Domain      string
	Header      string
	EnvVar      string
	ValueFormat string
}

// gatewayRuntimeConfig matches the proxy.Config struct in core/gateway.
type gatewayRuntimeConfig struct {
	Listen       string               `yaml:"listen"`
	DNSListen    string               `yaml:"dns_listen"`
	MITMDomains  []string             `yaml:"mitm_domains"`
	AuthHeaders  []authHeaderRuntime  `yaml:"auth_headers,omitempty"`
	EgressRules  []egressRuleRuntime  `yaml:"egress_rules,omitempty"`
	PortForwards []portForwardRuntime `yaml:"port_forwards,omitempty"`
	HealthAddr   string               `yaml:"health_addr,omitempty"`
}

// authHeaderRuntime is the runtime representation of an auth-header entry in config.yaml.
type authHeaderRuntime struct {
	Domain string `yaml:"domain"`
	Header string `yaml:"header"`
	Value  string `yaml:"value"`
}

// egressRuleRuntime is the runtime representation of an egress rule in config.yaml.
type egressRuleRuntime struct {
	Hosts       []string            `yaml:"hosts"`
	Deny        bool                `yaml:"deny,omitempty"`
	Headers     map[string]string   `yaml:"headers,omitempty"`
	DenyPaths   []string            `yaml:"deny_paths,omitempty"`
	DenyGraphQL *config.DenyGraphQL `yaml:"deny_graphql,omitempty"`
	Target      string              `yaml:"target,omitempty"`
}

// portForwardRuntime is the runtime representation of a port forward in config.yaml.
type portForwardRuntime struct {
	Listen string `yaml:"listen"`
	Target string `yaml:"target"`
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

	// Process egress rules for auth headers
	for _, rule := range out.EgressRules {
		if rule.Deny || len(rule.Headers) == 0 {
			continue
		}
		for _, host := range rule.Hosts {
			if host == "*" {
				continue
			}
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

	// Merge plugin-contributed egress rules (inserted before catch-all)
	if contribs != nil {
		for _, rule := range contribs.Gateway.Egress {
			normalized := normalizeEgressHosts(rule)
			// Extract auth headers from plugin rules that have static headers
			for _, host := range normalized.Hosts {
				if host == "*" {
					continue
				}
				for header, value := range normalized.Headers {
					ev, valueFormat := envvar.ParseTemplate(value)
					out.AuthHeaders = append(out.AuthHeaders, AuthHeaderEntry{
						Domain:      host,
						Header:      header,
						EnvVar:      ev,
						ValueFormat: valueFormat,
					})
				}
			}
			out.EgressRules = insertPluginEgressRule(out.EgressRules, normalized)
		}
		out.Ingress = contribs.Gateway.Ingress
	}

	return out
}

// insertPluginEgressRule inserts a plugin rule before the catch-all, or appends if no catch-all.
func insertPluginEgressRule(rules []config.EgressRule, rule config.EgressRule) []config.EgressRule {
	if len(rules) > 0 && len(rules[len(rules)-1].Hosts) == 1 && rules[len(rules)-1].Hosts[0] == "*" {
		rules = append(rules[:len(rules)-1], rule, rules[len(rules)-1])
	} else {
		rules = append(rules, rule)
	}
	return rules
}

// normalizeEgressHosts converts any URLs in rule.Hosts to bare hostnames.
func normalizeEgressHosts(rule config.EgressRule) config.EgressRule {
	normalized := rule
	normalized.Hosts = make([]string, 0, len(rule.Hosts))
	for _, h := range rule.Hosts {
		if d := extractDomain(h); d != "" {
			normalized.Hosts = append(normalized.Hosts, d)
		} else {
			normalized.Hosts = append(normalized.Hosts, h)
		}
	}
	return normalized
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

	domains := make([]string, 0, len(mitmSet))
	for domain := range mitmSet {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	rc.MITMDomains = domains

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
			Hosts:       rule.Hosts,
			Deny:        rule.Deny,
			Headers:     rule.Headers,
			DenyPaths:   rule.DenyPaths,
			DenyGraphQL: rule.DenyGraphQL,
			Target:      rule.Target,
		})
	}

	// Write port forwards from ingress rules
	for _, ing := range gwCfg.Ingress {
		rc.PortForwards = append(rc.PortForwards, portForwardRuntime{
			Listen: ":" + ing.Listen,
			Target: ing.Target,
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
