package jsruntime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dop251/goja"
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

func TestRequestContext_Env(t *testing.T) {
	t.Setenv("TEST_PLUGIN_VAR", "secret123")

	req, _ := http.NewRequest("GET", "https://example.com/api", nil)
	req.Host = "example.com"

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	val, err := vm.RunString(`ctx.env("TEST_PLUGIN_VAR")`)
	require.NoError(t, err)
	assert.Equal(t, "secret123", val.Export())

	// Undefined for missing vars
	val, err = vm.RunString(`ctx.env("NONEXISTENT_VAR")`)
	require.NoError(t, err)
	assert.True(t, goja.IsUndefined(val))
}

func TestRequestContext_SetPath(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://api.telegram.org/botdummy/deleteWebhook", nil)
	req.Host = "api.telegram.org"

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))

	// This mirrors what telegram-token-rewrite.ts does
	_, err := vm.RunString(`ctx.request.setPath("/botREAL_TOKEN/deleteWebhook")`)
	require.NoError(t, err, "setPath should be callable")

	assert.Equal(t, "/botREAL_TOKEN/deleteWebhook", req.URL.Path)
	assert.Equal(t, "", req.URL.RawPath)
}

func TestRequestContext_SetPath_ViaExportDefault(t *testing.T) {
	// Reproduce the exact execution pattern used by the gateway plugin loader:
	// bundledJS + "\n__handler.default(ctx, options);"
	req, _ := http.NewRequest("POST", "https://api.telegram.org/botdummy/deleteWebhook", nil)
	req.Host = "api.telegram.org"

	vm := NewVM()
	ctx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", ctx.ToJSObject(vm)))
	require.NoError(t, vm.Set("options", map[string]any{"bot_token": "REAL_TOKEN"}))

	// Simulate esbuild output with __handler wrapper
	script := `
var __handler = {default: function(ctx, options) {
  var token = options.bot_token;
  if (!token) return;
  var path = ctx.request.path;
  var rewritten = path.replace(/^\/bot[^/]*\//, "/bot" + token + "/");
  if (rewritten !== path) {
    ctx.request.setPath(rewritten);
  }
}};
__handler.default(ctx, options);
`
	_, err := vm.RunString(script)
	require.NoError(t, err, "setPath should work when called from handler pattern")

	assert.Equal(t, "/botREAL_TOKEN/deleteWebhook", req.URL.Path)
}

func TestRequestContext_SetPath_FullExecMiddlewarePath(t *testing.T) {
	// Reproduce the EXACT flow of execMiddleware: NewVM + InjectHostAPIs + real esbuild IIFE
	req, _ := http.NewRequest("POST", "https://api.telegram.org/botdummy/deleteWebhook", nil)
	req.Host = "api.telegram.org"

	vm := NewVM()
	hostCfg := &HostAPIConfig{DataDir: t.TempDir()}
	InjectHostAPIs(vm, hostCfg)

	reqCtx := NewRequestContext(req, nil)
	require.NoError(t, vm.Set("ctx", reqCtx.ToJSObject(vm)))
	require.NoError(t, vm.Set("options", map[string]any{"bot_token": "REAL_TOKEN"}))

	// Real esbuild IIFE output (same format as production)
	bundledJS := `var __handler = (() => {
  var __defProp = Object.defineProperty;
  var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
  var __getOwnPropNames = Object.getOwnPropertyNames;
  var __hasOwnProp = Object.prototype.hasOwnProperty;
  var __export = (target, all) => {
    for (var name in all)
      __defProp(target, name, { get: all[name], enumerable: true });
  };
  var __copyProps = (to, from, except, desc) => {
    if (from && typeof from === "object" || typeof from === "function") {
      for (let key of __getOwnPropNames(from))
        if (!__hasOwnProp.call(to, key) && key !== except)
          __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
    }
    return to;
  };
  var __toCommonJS = (mod) => __copyProps(__defProp({}, "__esModule", { value: true }), mod);
  var telegram_token_rewrite_exports = {};
  __export(telegram_token_rewrite_exports, {
    default: () => telegram_token_rewrite_default
  });
  var handler = (ctx, options) => {
    const realToken = options.bot_token;
    if (!realToken) return;
    gw.secrets.register(realToken);
    const path = ctx.request.path;
    const idx = path.indexOf("/bot");
    if (idx !== -1) {
      const rest = path.substring(idx + 4);
      const slashIdx = rest.indexOf("/");
      if (slashIdx !== -1) {
        const method = rest.substring(slashIdx);
        ctx.request.setPath(path.substring(0, idx) + "/bot" + realToken + method);
      }
    }
  };
  var telegram_token_rewrite_default = handler;
  return __toCommonJS(telegram_token_rewrite_exports);
})();`

	_, err := vm.RunString(bundledJS + "\n__handler.default(ctx, options);")
	require.NoError(t, err, "setPath should work in full execMiddleware reproduction")

	assert.Equal(t, "/botREAL_TOKEN/deleteWebhook", req.URL.Path)
	assert.Equal(t, "", req.URL.RawPath)
	assert.Contains(t, hostCfg.RegisteredSecrets, "REAL_TOKEN")
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
