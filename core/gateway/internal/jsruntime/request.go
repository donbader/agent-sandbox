package jsruntime

import (
	"fmt"
	"net/http"

	"github.com/dop251/goja"
)

// RequestContext bridges a Go HTTP request/response to the JS world.
type RequestContext struct {
	Request *http.Request
	Writer  http.ResponseWriter // nil for middleware (proxy), non-nil for route handlers

	// Abort fields (middleware mode)
	AbortStatus  int
	AbortBody    string
	AbortHeaders http.Header

	// Response fields (route handler mode)
	ResponseStatus  int
	ResponseHeaders http.Header
	ResponseBody    string
}

// NewRequestContext creates a new request context for JS handlers.
func NewRequestContext(req *http.Request, w http.ResponseWriter) *RequestContext {
	return &RequestContext{
		Request:         req,
		Writer:          w,
		AbortHeaders:    make(http.Header),
		ResponseHeaders: make(http.Header),
	}
}

// ToJSObject converts the context into a JS-accessible object.
func (rc *RequestContext) ToJSObject(vm *VM) map[string]any {
	headers := make(map[string]string)
	for k, v := range rc.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	query := make(map[string]string)
	for k, v := range rc.Request.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	requestObj := map[string]any{
		"method":  rc.Request.Method,
		"url":     rc.Request.URL.String(),
		"host":    rc.Request.Host,
		"path":    rc.Request.URL.Path,
		"query":   query,
		"headers": headers,
		"setHeader": func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			val := call.Argument(1).String()
			rc.Request.Header.Set(key, val)
			return goja.Undefined()
		},
	}

	responseObj := map[string]any{
		"status": func(call goja.FunctionCall) goja.Value {
			rc.ResponseStatus = int(call.Argument(0).ToInteger())
			return goja.Undefined()
		},
		"header": func(call goja.FunctionCall) goja.Value {
			key := call.Argument(0).String()
			val := call.Argument(1).String()
			rc.ResponseHeaders.Set(key, val)
			return goja.Undefined()
		},
		"body": func(call goja.FunctionCall) goja.Value {
			rc.ResponseBody = call.Argument(0).String()
			return goja.Undefined()
		},
	}

	return map[string]any{
		"request":  requestObj,
		"response": responseObj,
		"abort": func(call goja.FunctionCall) goja.Value {
			rc.AbortStatus = int(call.Argument(0).ToInteger())
			if len(call.Arguments) > 1 {
				rc.AbortBody = call.Argument(1).String()
			}
			if len(call.Arguments) > 2 {
				headersVal := call.Argument(2).Export()
				if m, ok := headersVal.(map[string]any); ok {
					for k, v := range m {
						rc.AbortHeaders.Set(k, fmt.Sprintf("%v", v))
					}
				}
			}
			return goja.Undefined()
		},
	}
}
