package config

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

// EgressRule defines a single egress access control rule.
// Rules are evaluated in order; first match wins.
// If no rule matches, traffic is denied (implicit deny-all).
type EgressRule struct {
	Hosts     []string          `yaml:"hosts" json:"hosts" jsonschema:"required,title=hosts,description=Host patterns to match (domain globs or CIDRs). Use ['*'] as catch-all."`
	Deny      bool              `yaml:"deny,omitempty" json:"deny,omitempty" jsonschema:"title=deny,description=If true block matching traffic"`
	Headers   map[string]string `yaml:"headers,omitempty" json:"headers,omitempty" jsonschema:"title=headers,description=Headers injected by gateway (implies MITM + allow)"`
	DenyPaths []string          `yaml:"deny_paths,omitempty" json:"deny_paths,omitempty" jsonschema:"title=deny_paths,description=URL path patterns to block (implies MITM). Format: METHOD /path/glob or /path/glob"`
	Network   string            `yaml:"network,omitempty" json:"network,omitempty" jsonschema:"title=network,description=Compose network to attach gateway to (for internal services)"`
	Target    string            `yaml:"target,omitempty" json:"target,omitempty" jsonschema:"title=target,description=Forwarding destination (host:port) for internal services. Omit for standard HTTPS passthrough."`
}

// EgressMatch describes the result of matching a host against egress rules.
type EgressMatch struct {
	Matched   bool
	Denied    bool
	Rule      *EgressRule
	RuleIndex int
}

// MatchHost evaluates the egress rules for a given host, returning the first match.
// If no rule matches, returns EgressMatch{Matched: false} which means implicit deny.
func MatchHost(rules []EgressRule, host string) EgressMatch {
	for i, rule := range rules {
		for _, pattern := range rule.Hosts {
			if matchHostPattern(pattern, host) {
				return EgressMatch{
					Matched:   true,
					Denied:    rule.Deny,
					Rule:      &rules[i],
					RuleIndex: i,
				}
			}
		}
	}
	return EgressMatch{Matched: false}
}

// MatchPath checks if a request method+path is denied by deny_paths rules.
// Returns true if the path should be blocked.
func MatchPath(denyPaths []string, method, path string) bool {
	for _, pattern := range denyPaths {
		if matchPathPattern(pattern, method, path) {
			return true
		}
	}
	return false
}

// matchHostPattern checks if a host matches a pattern.
// Patterns can be:
//   - Exact domain: "api.github.com"
//   - Wildcard domain: "*.github.com"
//   - CIDR: "10.0.0.0/8"
//   - Catch-all: "*"
func matchHostPattern(pattern, host string) bool {
	if pattern == "*" {
		return true
	}

	// Try CIDR match
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err == nil {
			ip := net.ParseIP(host)
			if ip != nil {
				return cidr.Contains(ip)
			}
			return false
		}
	}

	// Wildcard domain match: "*.github.com" matches "api.github.com", "foo.bar.github.com"
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".github.com"
		if strings.HasSuffix(host, suffix) {
			return true
		}
		// Also match the bare domain: "*.github.com" matches "github.com"
		if host == pattern[2:] {
			return true
		}
		return false
	}

	// Exact domain match (using filepath.Match for simple glob support)
	matched, err := filepath.Match(pattern, host)
	if err != nil {
		return false
	}
	return matched
}

// matchPathPattern checks if method+path matches a deny_paths pattern.
// Pattern formats:
//   - "/path/glob" — matches any method
//   - "DELETE /path/glob" — matches specific method
func matchPathPattern(pattern, method, path string) bool {
	patternMethod := ""
	patternPath := pattern

	// Check if pattern has a method prefix
	if idx := strings.IndexByte(pattern, ' '); idx > 0 {
		candidate := pattern[:idx]
		// Only treat as method if it's all uppercase letters
		if isHTTPMethod(candidate) {
			patternMethod = candidate
			patternPath = pattern[idx+1:]
		}
	}

	// Check method constraint
	if patternMethod != "" && !strings.EqualFold(patternMethod, method) {
		return false
	}

	// Match path using filepath.Match (glob-style)
	matched, err := filepath.Match(patternPath, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	// Also support prefix matching for patterns ending with /*
	if strings.HasSuffix(patternPath, "/*") {
		prefix := patternPath[:len(patternPath)-2]
		if strings.HasPrefix(path, prefix+"/") || path == prefix {
			return true
		}
	}

	return false
}

// isHTTPMethod checks if s is a valid HTTP method.
func isHTTPMethod(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}

// ValidateEgressRules validates a list of egress rules.
func ValidateEgressRules(rules []EgressRule) []string {
	var errs []string

	for i, rule := range rules {
		if len(rule.Hosts) == 0 {
			errs = append(errs, fmt.Sprintf("gateway.egress[%d]: hosts is required", i))
		}

		if rule.Deny && len(rule.Headers) > 0 {
			errs = append(errs, fmt.Sprintf("gateway.egress[%d]: cannot have both deny: true and headers", i))
		}

		if rule.Deny && len(rule.DenyPaths) > 0 {
			errs = append(errs, fmt.Sprintf("gateway.egress[%d]: cannot have both deny: true and deny_paths (entire host is already denied)", i))
		}

		for j, pattern := range rule.Hosts {
			if pattern == "" {
				errs = append(errs, fmt.Sprintf("gateway.egress[%d].hosts[%d]: empty pattern", i, j))
			}
		}
	}

	return errs
}

// NeedsMITM returns true if a rule requires TLS MITM (has headers or deny_paths).
func (r *EgressRule) NeedsMITM() bool {
	return len(r.Headers) > 0 || len(r.DenyPaths) > 0
}

// IsAllow returns true if the rule allows traffic (not denied).
func (r *EgressRule) IsAllow() bool {
	return !r.Deny
}
