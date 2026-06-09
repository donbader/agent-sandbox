package gateway

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// MiddlewareContext provides request access and environment resolution for custom middleware.
type MiddlewareContext struct {
	Request *http.Request
	Env     func(string) string

	// Abort fields: if AbortStatus is set (non-zero), the gateway returns this
	// response instead of proxying the request to the upstream.
	AbortStatus  int
	AbortHeaders http.Header
	AbortBody    string
}

// Abort sets the context to return an HTTP response instead of proxying.
func (c *MiddlewareContext) Abort(status int, body string) {
	c.AbortStatus = status
	c.AbortBody = body
}

// SetAbortHeader sets a header on the abort response.
func (c *MiddlewareContext) SetAbortHeader(key, value string) {
	if c.AbortHeaders == nil {
		c.AbortHeaders = make(http.Header)
	}
	c.AbortHeaders.Set(key, value)
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
var secretsMu sync.Mutex

// RegisterSecret declares a value that should be redacted from gateway logs.
// Call this in init() alongside RegisterMiddleware for any baked-in secrets.
func RegisterSecret(value string) {
	if value != "" {
		secretsMu.Lock()
		secrets = append(secrets, value)
		secretsMu.Unlock()
	}
}

// Secrets returns all secrets registered by middleware for log redaction.
func Secrets() []string {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	result := make([]string, len(secrets))
	copy(result, secrets)
	return result
}

// ResetForTesting clears all registered middleware and secrets. Test use only.
func ResetForTesting() {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	registry = nil
	secrets = nil
	routeRegistry = nil
}

// RouteHandlerFunc is the signature for custom gateway route handlers.
// Unlike middleware (which intercepts proxy requests), route handlers serve
// direct HTTP requests to the gateway on registered paths.
type RouteHandlerFunc func(w http.ResponseWriter, r *http.Request)

// RouteDef defines a route handler with a path pattern.
type RouteDef struct {
	// Path is the full namespaced path (e.g. /plugins/mcp-oauth/notion/callback).
	Path    string
	Handler RouteHandlerFunc
}

var routeRegistry []RouteDef

// RegisterRoute registers a direct HTTP route handler on the gateway.
func RegisterRoute(def RouteDef) {
	routeRegistry = append(routeRegistry, def)
}

// Routes returns all registered route definitions.
func Routes() []RouteDef {
	return routeRegistry
}

// MatchRoute returns the handler for the given request path, or nil if no route matches.
func MatchRoute(path string) RouteHandlerFunc {
	for _, route := range routeRegistry {
		if path == route.Path || strings.HasPrefix(path, route.Path+"/") {
			return route.Handler
		}
	}
	return nil
}
