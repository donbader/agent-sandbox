package jsruntime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestContext_ReadHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/api?foo=bar", nil)
	req.Host = "example.com"
	req.Header.Set("X-Custom", "value")

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	val, err := vm.RunString(`ctx.request.headers["X-Custom"]`)
	require.NoError(t, err)
	assert.Equal(t, "value", val.Export())

	val, err = vm.RunString(`ctx.request.method`)
	require.NoError(t, err)
	assert.Equal(t, "GET", val.Export())

	val, err = vm.RunString(`ctx.request.url`)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/api?foo=bar", val.Export())

	val, err = vm.RunString(`ctx.request.host`)
	require.NoError(t, err)
	assert.Equal(t, "example.com", val.Export())
}

func TestRequestContext_ReadQuery(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/api?name=test&page=2", nil)
	req.Host = "example.com"

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	val, err := vm.RunString(`ctx.request.query["name"]`)
	require.NoError(t, err)
	assert.Equal(t, "test", val.Export())

	val, err = vm.RunString(`ctx.request.query["page"]`)
	require.NoError(t, err)
	assert.Equal(t, "2", val.Export())
}

func TestRequestContext_ModifyHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/api", nil)
	req.Host = "example.com"

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	_, err := vm.RunString(`ctx.request.setHeader("Authorization", "Bearer token123")`)
	require.NoError(t, err)

	assert.Equal(t, "Bearer token123", req.Header.Get("Authorization"))
}

func TestRequestContext_Abort(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/api", nil)
	req.Host = "example.com"

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	_, err := vm.RunString(`ctx.abort(401, '{"error":"unauthorized"}')`)
	require.NoError(t, err)

	assert.Equal(t, 401, ctx.AbortStatus)
	assert.Equal(t, `{"error":"unauthorized"}`, ctx.AbortBody)
}

func TestRequestContext_RouteHandler(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://localhost:8080/plugins/test/hello", nil)
	req.Host = "localhost:8080"
	w := httptest.NewRecorder()

	vm := NewVM()
	ctx := NewRequestContext(req, w)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	_, err := vm.RunString(`
		ctx.response.status(200);
		ctx.response.header("Content-Type", "application/json");
		ctx.response.body('{"ok":true}');
	`)
	require.NoError(t, err)

	assert.Equal(t, 200, ctx.ResponseStatus)
	assert.Equal(t, "application/json", ctx.ResponseHeaders.Get("Content-Type"))
	assert.Equal(t, `{"ok":true}`, ctx.ResponseBody)
}
