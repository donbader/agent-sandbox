package jsruntime

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"

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

// ToJSObject converts the context into a native goja object.
// All sub-objects are native *goja.Object to avoid goja reflecting Go maps/structs
// which can lose method visibility in certain cross-compilation scenarios.
func (rc *RequestContext) ToJSObject(vm *VM) *goja.Object {
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

	rt := vm.Runtime()

	// Build the request object with read-only properties that throw on assignment.
	// This prevents silent bugs where ctx.request.path = "..." does nothing.
	requestObj := rt.NewObject()
	// @ts-prop ctx.request.method: readonly method: string
	_ = requestObj.Set("method", rc.Request.Method)
	// @ts-prop ctx.request.url: readonly url: string
	_ = requestObj.Set("url", rc.Request.URL.String())
	// @ts-prop ctx.request.host: readonly host: string
	_ = requestObj.Set("host", rc.Request.Host)
	// @ts-prop ctx.request.path: readonly path: string
	_ = requestObj.Set("path", rc.Request.URL.Path)
	// @ts-prop ctx.request.query: readonly query: Record<string, string>
	_ = requestObj.Set("query", query)
	// @ts-prop ctx.request.headers: readonly headers: Record<string, string>
	_ = requestObj.Set("headers", headers)
	var bodyStr string
	if rc.Request.Body != nil {
		bodyBytes, err := io.ReadAll(rc.Request.Body)
		if err == nil {
			bodyStr = string(bodyBytes)
			// Replace the body so downstream forwarding still works
			rc.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
	}
	// @ts-prop ctx.request.body: readonly body: string
	_ = requestObj.Set("body", bodyStr)

	// @ts-method ctx.request.setHeader(key: string, value: string): void
	_ = requestObj.Set("setHeader", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		rc.Request.Header.Set(key, val)
		return goja.Undefined()
	})
	// @ts-method ctx.request.setPath(newPath: string): void
	_ = requestObj.Set("setPath", func(call goja.FunctionCall) goja.Value {
		newPath := call.Argument(0).String()
		rc.Request.URL.Path = newPath
		rc.Request.URL.RawPath = ""
		return goja.Undefined()
	})

	// Freeze read-only properties so assignment throws a TypeError in strict mode
	// and is a no-op in sloppy mode (with a helpful error via defineProperty).
	for _, prop := range []string{"method", "url", "host", "path", "query", "headers", "body"} {
		_ = requestObj.DefineDataProperty(prop, requestObj.Get(prop), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
	}

	// Build the response object as a native goja object (not a Go map).
	responseObj := rt.NewObject()
	// @ts-method ctx.response.status(code: number): void
	_ = responseObj.Set("status", func(call goja.FunctionCall) goja.Value {
		rc.ResponseStatus = int(call.Argument(0).ToInteger())
		return goja.Undefined()
	})
	// @ts-method ctx.response.header(key: string, value: string): void
	_ = responseObj.Set("header", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).String()
		rc.ResponseHeaders.Set(key, val)
		return goja.Undefined()
	})
	// @ts-method ctx.response.body(content: string): void
	_ = responseObj.Set("body", func(call goja.FunctionCall) goja.Value {
		rc.ResponseBody = call.Argument(0).String()
		return goja.Undefined()
	})

	// Build the top-level ctx object as a native goja object.
	ctxObj := rt.NewObject()
	// @ts-skip (structural wiring)
	_ = ctxObj.Set("request", requestObj)
	// @ts-skip (structural wiring)
	_ = ctxObj.Set("response", responseObj)
	// @ts-method ctx.env(key: string): string | undefined
	_ = ctxObj.Set("env", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := os.Getenv(key)
		if val == "" {
			return goja.Undefined()
		}
		return rt.ToValue(val)
	})
	// @ts-method ctx.abort(status: number, body?: string, headers?: Record<string, string>): void
	_ = ctxObj.Set("abort", func(call goja.FunctionCall) goja.Value {
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
	})

	return ctxObj
}
