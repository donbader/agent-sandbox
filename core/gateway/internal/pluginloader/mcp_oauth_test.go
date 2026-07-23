package pluginloader

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMcpOAuth_CallbackQueryParsing verifies that the mcp-oauth callback handler
// can parse query parameters from the request without using Web APIs (URLSearchParams)
// that are unavailable in the goja JS runtime.
func TestMcpOAuth_CallbackQueryParsing(t *testing.T) {
	gateway.ResetForTesting()

	// Point to the real mcp-oauth plugin source
	pluginDir, err := filepath.Abs("../../../plugins/mcp-oauth")
	require.NoError(t, err)

	// Verify the plugin source exists
	_, err = os.Stat(filepath.Join(pluginDir, "src", "callback.ts"))
	require.NoError(t, err, "mcp-oauth callback.ts not found at %s", pluginDir)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "mcp-oauth",
				Dir:  pluginDir,
				Options: map[string]any{
					"providers": map[string]any{
						"notion": map[string]any{
							"mcp_url": "https://mcp.notion.com/mcp",
						},
					},
					"callback_url": "http://127.0.0.1:8080/plugins/mcp-oauth/callback",
					"token_dir":    t.TempDir(),
				},
				Gateway: GatewayContrib{
					Routes: []RouteEntry{
						{Path: "/callback", Handler: "./src/callback.ts"},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	handler := gateway.MatchRoute("/plugins/mcp-oauth/callback")
	require.NotNil(t, handler)

	// Simulate OAuth callback with code and state query params.
	// The state won't be valid, so we expect a 403 "invalid state signature" —
	// but crucially, NOT a JS runtime error like "URLSearchParams is not defined".
	req, _ := http.NewRequest("GET", "http://127.0.0.1:8080/plugins/mcp-oauth/callback?code=testcode123&state=invalidsig:notion", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	// Should get a domain-logic error (403 invalid state), NOT a 500 plugin error
	assert.Equal(t, 403, w.Code, "expected 403 for invalid state, got %d: %s", w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "invalid state")
}

// TestMcpOAuth_CallbackMissingCode verifies handling of missing code parameter.
func TestMcpOAuth_CallbackMissingCode(t *testing.T) {
	gateway.ResetForTesting()

	pluginDir, err := filepath.Abs("../../../plugins/mcp-oauth")
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "mcp-oauth",
				Dir:  pluginDir,
				Options: map[string]any{
					"providers":    map[string]any{},
					"callback_url": "http://127.0.0.1:8080/plugins/mcp-oauth/callback",
					"token_dir":    t.TempDir(),
				},
				Gateway: GatewayContrib{
					Routes: []RouteEntry{
						{Path: "/callback", Handler: "./src/callback.ts"},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	handler := gateway.MatchRoute("/plugins/mcp-oauth/callback")
	require.NotNil(t, handler)

	// No code param — should get 400, not a runtime error
	req, _ := http.NewRequest("GET", "http://127.0.0.1:8080/plugins/mcp-oauth/callback?state=foo", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "missing code")
}

// TestMcpOAuth_LoginHandler verifies the login route doesn't crash on
// ES6 APIs (Object.keys, string methods, etc.) in the goja runtime.
func TestMcpOAuth_LoginHandler(t *testing.T) {
	gateway.ResetForTesting()

	pluginDir, err := filepath.Abs("../../../plugins/mcp-oauth")
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "mcp-oauth",
				Dir:  pluginDir,
				Options: map[string]any{
					"providers": map[string]any{
						"notion": map[string]any{
							"mcp_url": "https://mcp.notion.com/mcp",
						},
					},
					"callback_url": "http://127.0.0.1:8080/plugins/mcp-oauth/callback",
					"token_dir":    t.TempDir(),
				},
				Gateway: GatewayContrib{
					Routes: []RouteEntry{
						{Path: "/login", Handler: "./src/login.ts"},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	handler := gateway.MatchRoute("/plugins/mcp-oauth/login")
	require.NotNil(t, handler)

	// Request login for a provider that has no client_id — will attempt discovery.
	// Discovery will fail (no server), but the JS should execute without runtime errors.
	req, _ := http.NewRequest("GET", "http://127.0.0.1:8080/plugins/mcp-oauth/login/notion", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	// Should NOT be a 500 with "plugin error: TypeError/ReferenceError"
	body := w.Body.String()
	assert.NotContains(t, body, "ReferenceError", "JS runtime error: %s", body)
	assert.NotContains(t, body, "TypeError", "JS runtime error: %s", body)
}

// TestMcpOAuth_OAuthMiddleware verifies the oauth middleware doesn't crash
// on Object.entries or other ES6 runtime APIs in goja.
func TestMcpOAuth_OAuthMiddleware(t *testing.T) {
	gateway.ResetForTesting()

	pluginDir, err := filepath.Abs("../../../plugins/mcp-oauth")
	require.NoError(t, err)

	cfg := &PluginsConfig{
		Plugins: []PluginConfig{
			{
				Name: "mcp-oauth",
				Dir:  pluginDir,
				Options: map[string]any{
					"providers": map[string]any{
						"notion": map[string]any{
							"mcp_url": "https://mcp.notion.com/mcp",
						},
					},
					"callback_url": "http://127.0.0.1:8080/plugins/mcp-oauth/callback",
					"token_dir":    t.TempDir(),
				},
				Gateway: GatewayContrib{
					Middlewares: []MiddlewareEntry{
						{Script: "./src/oauth.ts", Domains: []string{"mcp.notion.com"}},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	all := gateway.All()
	require.Len(t, all, 1)

	// Request to mcp.notion.com — no token file exists, so it should pass through
	// without auth (let upstream handle it). No abort, no crash.
	req, _ := http.NewRequest("GET", "https://mcp.notion.com/mcp", nil)
	req.Host = "mcp.notion.com"
	ctx := &gateway.MiddlewareContext{Request: req, Env: os.Getenv}
	err = all[0].Func(ctx)

	require.NoError(t, err, "middleware should not return a Go error")
	// Should pass through without aborting (no token = let upstream respond)
	assert.Equal(t, 0, ctx.AbortStatus, "should not abort when no token is stored")
	assert.Empty(t, ctx.AbortBody)
}

// TestMcpOAuth_LoginUsesQueryCallbackURL verifies that a ?callback_url=... query param
// is passed as redirect_uri in DCR, not the internal gateway hostname.
func TestMcpOAuth_LoginUsesQueryCallbackURL(t *testing.T) {
	gateway.ResetForTesting()

	// Track the redirect_uris sent to the DCR endpoint.
	var capturedRedirectURIs []string

	// Mock MCP server: serves .well-known metadata and a DCR endpoint.
	mockMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/oauth-authorization-server"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "http://" + r.Host,
				"authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
				"registration_endpoint":  "http://" + r.Host + "/register",
			})
		case r.URL.Path == "/register":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if uris, ok := body["redirect_uris"].([]any); ok {
				for _, u := range uris {
					capturedRedirectURIs = append(capturedRedirectURIs, u.(string))
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"client_id":     "test-client-id",
				"client_secret": "test-client-secret",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockMCP.Close()

	pluginDir, err := filepath.Abs("../../../plugins/mcp-oauth")
	require.NoError(t, err)

	cfg := &PluginsConfig{
		AllowPrivateIPs: true, // allow mock server on loopback
		Plugins: []PluginConfig{
			{
				Name: "mcp-oauth",
				Dir:  pluginDir,
				Options: map[string]any{
					"providers": map[string]any{
						"testprovider": map[string]any{
							"mcp_url": mockMCP.URL + "/mcp",
						},
					},
					"token_dir": t.TempDir(),
				},
				Gateway: GatewayContrib{
					Routes: []RouteEntry{
						{Path: "/login", Handler: "./src/login.ts"},
					},
				},
			},
		},
	}

	err = LoadPlugins(cfg)
	require.NoError(t, err)

	handler := gateway.MatchRoute("/plugins/mcp-oauth/login")
	require.NotNil(t, handler)

	// Call with explicit callback_url query param — simulates what the skill instructs.
	req, _ := http.NewRequest("GET",
		"http://maggie-gateway:8080/plugins/mcp-oauth/login/testprovider?callback_url=http://127.0.0.1/plugins/mcp-oauth/callback",
		nil)
	req.Host = "maggie-gateway:8080"
	w := httptest.NewRecorder()
	handler(w, req)

	// DCR must have fired with the loopback URL, not the gateway's internal hostname.
	require.NotEmpty(t, capturedRedirectURIs, "DCR was not called; login response: %s", w.Body.String())
	assert.Equal(t, "http://127.0.0.1/plugins/mcp-oauth/callback", capturedRedirectURIs[0],
		"redirect_uri in DCR must be the query-param callback_url, not the gateway hostname")
}
