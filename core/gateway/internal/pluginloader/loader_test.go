package pluginloader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPlugins_Middleware(t *testing.T) {
	gateway.ResetForTesting()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "src"), 0755))

	// Write a simple middleware that injects a header
	err := os.WriteFile(filepath.Join(pluginDir, "src", "auth.ts"), []byte(`
		export default function(ctx: any, options: any) {
			ctx.request.setHeader("X-Injected", "from-ts-plugin");
		}
	`), 0644)
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name:    "test-plugin",
				Dir:     pluginDir,
				Options: map[string]any{"token": "secret"},
				Gateway: GatewayContrib{
					Middlewares: []MiddlewareEntry{
						{Script: "./src/auth.ts", Domains: []string{"api.example.com"}},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	// Verify middleware was registered
	all := gateway.All()
	require.Len(t, all, 1)
	assert.Equal(t, "ts:test-plugin:auth.ts", all[0].Name)
	assert.Equal(t, []string{"api.example.com"}, all[0].Domains)

	// Verify middleware actually works
	req, _ := http.NewRequest("GET", "https://api.example.com/test", nil)
	req.Host = "api.example.com"
	ctx := &gateway.MiddlewareContext{Request: req, Env: os.Getenv}
	err = all[0].Func(ctx)
	require.NoError(t, err)
	assert.Equal(t, "from-ts-plugin", req.Header.Get("X-Injected"))
}

func TestLoadPlugins_Route(t *testing.T) {
	gateway.ResetForTesting()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "src"), 0755))

	err := os.WriteFile(filepath.Join(pluginDir, "src", "hello.ts"), []byte(`
		export default function(ctx: any, options: any) {
			ctx.response.status(200);
			ctx.response.header("Content-Type", "text/plain");
			ctx.response.body("hello from plugin");
		}
	`), 0644)
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "test-plugin",
				Dir:  pluginDir,
				Gateway: GatewayContrib{
					Routes: []RouteEntry{
						{Path: "/hello", Handler: "./src/hello.ts"},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	// Verify route was registered
	handler := gateway.MatchRoute("/plugins/test-plugin/hello")
	require.NotNil(t, handler)

	// Verify route handler works
	req, _ := http.NewRequest("GET", "http://localhost:8080/plugins/test-plugin/hello", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "text/plain", w.Header().Get("Content-Type"))
	assert.Equal(t, "hello from plugin", w.Body.String())
}

func TestLoadPlugins_MiddlewareAbort(t *testing.T) {
	gateway.ResetForTesting()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "src"), 0755))

	err := os.WriteFile(filepath.Join(pluginDir, "src", "guard.ts"), []byte(`
		export default function(ctx: any, options: any) {
			ctx.abort(403, '{"error":"forbidden"}');
		}
	`), 0644)
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "test-plugin",
				Dir:  pluginDir,
				Gateway: GatewayContrib{
					Middlewares: []MiddlewareEntry{
						{Script: "./src/guard.ts", Domains: []string{"blocked.com"}},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	all := gateway.All()
	require.Len(t, all, 1)

	req, _ := http.NewRequest("GET", "https://blocked.com/secret", nil)
	req.Host = "blocked.com"
	ctx := &gateway.MiddlewareContext{Request: req, Env: os.Getenv}
	err = all[0].Func(ctx)
	require.NoError(t, err)
	assert.Equal(t, 403, ctx.AbortStatus)
	assert.Equal(t, `{"error":"forbidden"}`, ctx.AbortBody)
}

func TestLoadPlugins_WithOptions(t *testing.T) {
	gateway.ResetForTesting()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "src"), 0755))

	err := os.WriteFile(filepath.Join(pluginDir, "src", "opts.ts"), []byte(`
		export default function(ctx: any, options: any) {
			ctx.response.status(200);
			ctx.response.body(JSON.stringify(options));
		}
	`), 0644)
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name:    "test-plugin",
				Dir:     pluginDir,
				Options: map[string]any{"api_key": "test123", "enabled": true},
				Gateway: GatewayContrib{
					Routes: []RouteEntry{
						{Path: "/opts", Handler: "./src/opts.ts"},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	handler := gateway.MatchRoute("/plugins/test-plugin/opts")
	require.NotNil(t, handler)

	req, _ := http.NewRequest("GET", "http://localhost/plugins/test-plugin/opts", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "test123")
}

func TestLoadPlugins_BadScript(t *testing.T) {
	gateway.ResetForTesting()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "bad-plugin")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "src"), 0755))

	err := os.WriteFile(filepath.Join(pluginDir, "src", "broken.ts"), []byte(`
		export default function( {{{ invalid syntax
	`), 0644)
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "bad-plugin",
				Dir:  pluginDir,
				Gateway: GatewayContrib{
					Middlewares: []MiddlewareEntry{
						{Script: "./src/broken.ts", Domains: []string{"example.com"}},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "esbuild")
}

func TestLoadPluginsFromFile_Missing(t *testing.T) {
	err := LoadPluginsFromFile("/nonexistent/path/plugins.yaml")
	assert.NoError(t, err) // Missing file is not an error (no plugins to load)
}
