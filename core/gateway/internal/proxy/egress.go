package proxy

import (
	"net"
	"path/filepath"
	"strings"
)

// EgressFilter evaluates egress rules to determine if traffic should be allowed.
type EgressFilter struct {
	rules []EgressRule
}

// NewEgressFilter creates a filter from the given rules.
// If rules is empty, the filter allows all traffic (backward compatible).
func NewEgressFilter(rules []EgressRule) *EgressFilter {
	return &EgressFilter{rules: rules}
}

// HasRules returns true if the filter has any rules configured.
func (f *EgressFilter) HasRules() bool {
	return len(f.rules) > 0
}

// EgressDecision describes the result of evaluating egress rules for a host.
type EgressDecision struct {
	Allowed   bool
	Rule      *EgressRule
	RuleIndex int
}

// AllowHost checks if outbound traffic to the given host is allowed.
// Returns false (blocked) if no rule matches (implicit deny-all).
// Returns true if no rules are configured (backward compat: allow-all).
func (f *EgressFilter) AllowHost(host string) EgressDecision {
	if len(f.rules) == 0 {
		return EgressDecision{Allowed: true, RuleIndex: -1}
	}

	for i, rule := range f.rules {
		for _, pattern := range rule.Hosts {
			if matchHostPattern(pattern, host) {
				return EgressDecision{
					Allowed:   !rule.Deny,
					Rule:      &f.rules[i],
					RuleIndex: i,
				}
			}
		}
	}

	// No match = implicit deny
	return EgressDecision{Allowed: false, RuleIndex: -1}
}

// AllowPath checks if a specific request path is allowed for a matched rule.
// Should only be called if AllowHost returned Allowed=true and the rule has DenyPaths.
func (f *EgressFilter) AllowPath(rule *EgressRule, method, path string) bool {
	if rule == nil || len(rule.DenyPaths) == 0 {
		return true
	}
	return !matchPathPatterns(rule.DenyPaths, method, path)
}

// matchHostPattern checks if a host matches a pattern (domain glob, CIDR, or catch-all).
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

	// Wildcard domain: "*.github.com"
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".github.com"
		if strings.HasSuffix(host, suffix) {
			return true
		}
		// Also match bare domain: "*.github.com" matches "github.com"
		if host == pattern[2:] {
			return true
		}
		return false
	}

	// Exact or glob match
	matched, err := filepath.Match(pattern, host)
	if err != nil {
		return false
	}
	return matched
}

// matchPathPatterns checks if method+path matches any deny_paths pattern.
func matchPathPatterns(denyPaths []string, method, path string) bool {
	for _, pattern := range denyPaths {
		if matchSinglePath(pattern, method, path) {
			return true
		}
	}
	return false
}

// matchSinglePath checks one deny_paths pattern against method+path.
// Pattern formats: "/path/glob" (any method) or "DELETE /path/glob" (specific method).
func matchSinglePath(pattern, method, path string) bool {
	patternMethod := ""
	patternPath := pattern

	if idx := strings.IndexByte(pattern, ' '); idx > 0 {
		candidate := pattern[:idx]
		if isHTTPMethod(candidate) {
			patternMethod = candidate
			patternPath = pattern[idx+1:]
		}
	}

	if patternMethod != "" && !strings.EqualFold(patternMethod, method) {
		return false
	}

	matched, err := filepath.Match(patternPath, path)
	if err == nil && matched {
		return true
	}

	// Prefix match for patterns ending with /*
	if strings.HasSuffix(patternPath, "/*") {
		prefix := patternPath[:len(patternPath)-2]
		if strings.HasPrefix(path, prefix+"/") || path == prefix {
			return true
		}
	}

	return false
}

func isHTTPMethod(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}
