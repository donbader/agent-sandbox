package v1

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectPluginRoutes_Namespacing(t *testing.T) {
	tmpDir := t.TempDir()
	handlerFile := filepath.Join(tmpDir, "handler.go")
	require.NoError(t, os.WriteFile(handlerFile, []byte("package custom"), 0644))

	g := &Generator{projectDir: tmpDir}
	resolved := map[string]*resolvedPlugin{
		"@builtin/mcp-oauth": {
			def: &plugin.PluginDef{Name: "mcp-oauth", BaseDir: tmpDir},
			rendered: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Routes: []plugin.RouteEntry{
						{Path: "/callback", Handler: "handler.go"},
					},
				},
			},
		},
	}

	routes, err := g.collectPluginRoutes(resolved, tmpDir)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, "/plugins/mcp-oauth/callback", routes[0].Path)
	assert.Equal(t, "mcp-oauth", routes[0].PluginName)
}

func TestCollectPluginRoutes_ConflictDetection(t *testing.T) {
	tmpDir := t.TempDir()
	handlerFile := filepath.Join(tmpDir, "handler.go")
	require.NoError(t, os.WriteFile(handlerFile, []byte("package custom"), 0644))

	g := &Generator{projectDir: tmpDir}
	// Two plugins with the same name would produce the same namespace path
	// In practice this can't happen (plugin names are unique), but test the mechanism
	resolved := map[string]*resolvedPlugin{
		"@builtin/my-plugin": {
			def: &plugin.PluginDef{Name: "my-plugin", BaseDir: tmpDir},
			rendered: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Routes: []plugin.RouteEntry{
						{Path: "/webhook", Handler: "handler.go"},
						{Path: "/webhook", Handler: "handler.go"}, // duplicate
					},
				},
			},
		},
	}

	_, err := g.collectPluginRoutes(resolved, tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "route conflict")
}

func TestCollectPluginRoutes_MissingPath(t *testing.T) {
	tmpDir := t.TempDir()
	g := &Generator{projectDir: tmpDir}
	resolved := map[string]*resolvedPlugin{
		"@builtin/bad": {
			def: &plugin.PluginDef{Name: "bad", BaseDir: tmpDir},
			rendered: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Routes: []plugin.RouteEntry{
						{Path: "", Handler: "handler.go"},
					},
				},
			},
		},
	}

	_, err := g.collectPluginRoutes(resolved, tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must have both path and handler")
}

func TestCollectPluginRoutes_BundledFS(t *testing.T) {
	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, ".build")
	require.NoError(t, os.MkdirAll(buildDir, 0755))

	bundledFS := fstest.MapFS{
		"my-plugin/handlers/callback.go": &fstest.MapFile{Data: []byte("package custom")},
	}

	g := &Generator{projectDir: tmpDir, bundledFS: bundledFS}
	resolved := map[string]*resolvedPlugin{
		"@builtin/my-plugin": {
			def: &plugin.PluginDef{Name: "my-plugin"}, // no BaseDir = bundled
			rendered: &plugin.Contributions{
				Gateway: plugin.GatewayContrib{
					Routes: []plugin.RouteEntry{
						{Path: "/callback", Handler: "./handlers/callback.go"},
					},
				},
			},
		},
	}

	routes, err := g.collectPluginRoutes(resolved, buildDir)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	assert.Equal(t, "/plugins/my-plugin/callback", routes[0].Path)

	// Verify handler was extracted
	_, err = os.Stat(routes[0].Handler)
	assert.NoError(t, err)
}

func TestCopyRouteHandlers(t *testing.T) {
	tmpDir := t.TempDir()
	outDir := filepath.Join(tmpDir, ".build")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	// Create a handler file with a template variable
	handlerContent := `package custom

import "github.com/donbader/agent-sandbox/core/sdk/gateway"

func init() {
	gateway.RegisterRoute(gateway.RouteDef{
		Path: "{{ .path }}",
	})
}
`
	handlerFile := filepath.Join(tmpDir, "handler.go")
	require.NoError(t, os.WriteFile(handlerFile, []byte(handlerContent), 0644))

	routes := []RouteRef{
		{
			Path:       "/plugins/mcp-oauth/callback",
			Handler:    handlerFile,
			PluginName: "mcp-oauth",
		},
	}

	err := CopyRouteHandlers(tmpDir, outDir, routes, map[string]any{}, "https://gateway.example.com")
	require.NoError(t, err)

	// Verify the rendered handler exists
	destDir := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom")
	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Verify template was rendered
	content, err := os.ReadFile(filepath.Join(destDir, entries[0].Name()))
	require.NoError(t, err)
	assert.Contains(t, string(content), `/plugins/mcp-oauth/callback`)
	assert.NotContains(t, string(content), "{{ .path }}")
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/callback", "/callback"},
		{"callback", "/callback"},
		{"/callback/", "/callback"},
		{"/foo/bar/", "/foo/bar"},
	}
	for _, tt := range tests {
		got := normalizePath(tt.input)
		assert.Equal(t, tt.want, got, "normalizePath(%q)", tt.input)
	}
}
