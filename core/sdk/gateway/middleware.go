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
