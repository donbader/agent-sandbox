package gateway

import (
	"net"
	"net/http"
)

// MiddlewareContext provides request access and environment resolution for custom middleware.
type MiddlewareContext struct {
	Request *http.Request
	Env     func(string) string
}

// MiddlewareFunc is the signature for custom gateway middleware.
type MiddlewareFunc func(ctx *MiddlewareContext) error

// MiddlewareDef defines a middleware with domain scoping.
type MiddlewareDef struct {
	Name    string
	Domains []string
	Func    MiddlewareFunc
}

var registry []MiddlewareDef

// RegisterMiddleware registers a domain-scoped middleware.
func RegisterMiddleware(def MiddlewareDef) {
	registry = append(registry, def)
}

// All returns all registered middleware definitions.
func All() []MiddlewareDef {
	return registry
}

// MatchingMiddleware returns middleware whose domain list matches the given request host.
// If a middleware has no domains configured, it matches all requests.
func MatchingMiddleware(req *http.Request) []MiddlewareDef {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	var matched []MiddlewareDef
	for _, mw := range registry {
		if len(mw.Domains) == 0 || domainMatches(mw.Domains, host) {
			matched = append(matched, mw)
		}
	}
	return matched
}

// domainMatches returns true if host matches any domain in the list.
func domainMatches(domains []string, host string) bool {
	for _, d := range domains {
		if host == d {
			return true
		}
	}
	return false
}

// secrets collects values that should be redacted from logs.
var secrets []string

// RegisterSecret declares a value that should be redacted from gateway logs.
// Call this in init() alongside RegisterMiddleware for any baked-in secrets.
func RegisterSecret(value string) {
	if value != "" {
		secrets = append(secrets, value)
	}
}

// Secrets returns all secrets registered by middleware for log redaction.
func Secrets() []string {
	return secrets
}

// ResetForTesting clears all registered middleware and secrets. Test use only.
func ResetForTesting() {
	registry = nil
	secrets = nil
}
