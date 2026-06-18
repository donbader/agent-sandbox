package config

import (
	"fmt"
	"net/url"
	"strings"
)

// MigrateServicesToEgress converts the old gateway.services format to the new gateway.egress format.
// Returns nil if there are no services to migrate.
func MigrateServicesToEgress(services []GatewayServiceEntry) []EgressRule {
	if len(services) == 0 {
		return nil
	}

	var rules []EgressRule
	for _, svc := range services {
		domain := extractMigrationDomain(svc.URL)
		if domain == "" {
			continue
		}

		rule := EgressRule{
			Hosts: []string{domain},
		}
		if len(svc.Headers) > 0 {
			rule.Headers = svc.Headers
		}
		rules = append(rules, rule)
	}

	// Add catch-all allow at the end (preserves current default-allow behavior)
	rules = append(rules, EgressRule{
		Hosts: []string{"*"},
	})

	return rules
}

// HasLegacyServices returns true if the config uses the old gateway.services format.
func HasLegacyServices(cfg *Config) bool {
	return len(cfg.Gateway.Services) > 0 && len(cfg.Gateway.Egress) == 0
}

// FormatEgressYAML formats egress rules as YAML for display/migration output.
func FormatEgressYAML(rules []EgressRule) string {
	var sb strings.Builder
	sb.WriteString("gateway:\n")
	sb.WriteString("  egress:\n")
	for _, rule := range rules {
		sb.WriteString("    - hosts: [")
		for i, h := range rule.Hosts {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%q", h)
		}
		sb.WriteString("]\n")
		if rule.Deny {
			sb.WriteString("      deny: true\n")
		}
		if len(rule.Headers) > 0 {
			sb.WriteString("      headers:\n")
			for k, v := range rule.Headers {
				fmt.Fprintf(&sb, "        %s: %s\n", k, v)
			}
		}
		if len(rule.DenyPaths) > 0 {
			sb.WriteString("      deny_paths:\n")
			for _, p := range rule.DenyPaths {
				fmt.Fprintf(&sb, "        - %q\n", p)
			}
		}
	}
	return sb.String()
}

// extractMigrationDomain extracts hostname from a service URL for migration.
func extractMigrationDomain(rawURL string) string {
	if strings.Contains(rawURL, "://") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	// host:port
	if idx := strings.LastIndex(rawURL, ":"); idx > 0 {
		return rawURL[:idx]
	}
	return rawURL
}
