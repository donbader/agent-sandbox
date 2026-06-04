package gateway

import "net/http"

// MiddlewareContext provides request access and environment resolution for custom middleware.
type MiddlewareContext struct {
	Request *http.Request
	Env     func(string) string
}

// MiddlewareFunc is the signature for custom gateway middleware.
type MiddlewareFunc func(ctx *MiddlewareContext) error

var registry = map[string]MiddlewareFunc{}

// RegisterMiddleware registers a named middleware function.
func RegisterMiddleware(name string, fn MiddlewareFunc) {
	registry[name] = fn
}

// Get returns a registered middleware by name.
func Get(name string) (MiddlewareFunc, bool) {
	fn, ok := registry[name]
	return fn, ok
}

// All returns all registered middleware.
func All() map[string]MiddlewareFunc {
	return registry
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
