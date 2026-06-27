package plugin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/stretchr/testify/require"
)

// TestBuiltinPluginTemplates loads every plugin.yaml in core/plugins/ and
// verifies that its contributes template parses and renders without error.
// This catches undefined template functions, syntax errors, and type mismatches
// before they reach a release.
func TestBuiltinPluginTemplates(t *testing.T) {
	pluginsDir := filepath.Join("..", "..", "core", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	require.NoError(t, err, "failed to read core/plugins directory")

	var tested int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		yamlPath := filepath.Join(pluginsDir, entry.Name(), "plugin.yaml")
		if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(yamlPath)
			require.NoError(t, err)

			p, err := plugin.ParsePluginYAML(raw)
			require.NoError(t, err, "failed to parse plugin.yaml")

			opts := sampleOptions(p.Options)
			ctx := plugin.RenderContext{
				Self:      map[string]any{"name": "test-agent", "runtime": map[string]any{"image": "@builtin/pi"}},
				Generator: map[string]any{"core_version": "v1.100.1"},
				Functions: sampleFunctions(p),
			}

			_, err = plugin.RenderContributions(p, opts, ctx)
			require.NoError(t, err, "template render failed for plugin %q", p.Name)
		})
		tested++
	}
	require.Greater(t, tested, 0, "no plugins found to test")
}

// sampleOptions generates minimal valid options for a plugin based on its schema.
func sampleOptions(schema map[string]plugin.OptionSchema) map[string]any {
	opts := make(map[string]any)
	for name, s := range schema {
		if s.Default != nil && !s.Required {
			continue // will be filled by applyDefaults
		}
		opts[name] = sampleValue(s)
	}
	return opts
}

func sampleValue(s plugin.OptionSchema) any {
	switch s.Type {
	case "string":
		return "test-value"
	case "project-path":
		return "@fleet/test-path"
	case "boolean":
		return true
	case "integer":
		return 1
	case "array":
		return []any{"item-1"}
	case "object":
		// Build nested object from properties if defined
		if len(s.Properties) > 0 {
			obj := make(map[string]any)
			for k, prop := range s.Properties {
				obj[k] = sampleValue(prop)
			}
			return obj
		}
		// Generic object — provide a sample entry that satisfies common patterns
		// (e.g. mcp-oauth providers need mcp_url)
		return map[string]any{
			"sample": map[string]any{
				"mcp_url": "https://example.com/mcp",
			},
		}
	default:
		return "test"
	}
}

// TestGithubPatPlugin_DenyGraphQL verifies that the github-pat plugin renders
// deny_graphql.mutations correctly into the contributed egress rule.
func TestGithubPatPlugin_DenyGraphQL(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "core", "plugins", "github-pat", "plugin.yaml"))
	require.NoError(t, err)

	p, err := plugin.ParsePluginYAML(raw)
	require.NoError(t, err)

	opts := map[string]any{
		"token": "${GITHUB_PAT}",
		"deny_graphql": map[string]any{
			"mutations": []any{"mergePullRequest"},
		},
	}
	ctx := plugin.RenderContext{
		Self:      map[string]any{"name": "test-agent"},
		Generator: map[string]any{"core_version": "v1.0.0"},
	}

	contribs, err := plugin.RenderContributions(p, opts, ctx)
	require.NoError(t, err)

	// github-pat contributes exactly one egress rule covering api.github.com and github.com
	require.Len(t, contribs.Gateway.Egress, 1, "expected one egress rule")
	rule := contribs.Gateway.Egress[0]
	require.Contains(t, rule.Hosts, "api.github.com")
	require.Contains(t, rule.Hosts, "github.com")

	// deny_graphql must be rendered with the provided mutation name
	require.NotNil(t, rule.DenyGraphQL, "deny_graphql should be present on egress rule")
	require.Equal(t, []string{"mergePullRequest"}, rule.DenyGraphQL.Mutations)
}

// TestGithubPatPlugin_NoDenyGraphQL verifies that omitting deny_graphql produces
// an egress rule with no DenyGraphQL field (template conditional path).
func TestGithubPatPlugin_NoDenyGraphQL(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "core", "plugins", "github-pat", "plugin.yaml"))
	require.NoError(t, err)

	p, err := plugin.ParsePluginYAML(raw)
	require.NoError(t, err)

	opts := map[string]any{"token": "${GITHUB_PAT}"}
	ctx := plugin.RenderContext{
		Self:      map[string]any{"name": "test-agent"},
		Generator: map[string]any{"core_version": "v1.0.0"},
	}

	contribs, err := plugin.RenderContributions(p, opts, ctx)
	require.NoError(t, err)

	require.Len(t, contribs.Gateway.Egress, 1)
	require.Nil(t, contribs.Gateway.Egress[0].DenyGraphQL, "deny_graphql should be absent when not configured")
}

// sampleFunctions provides stub values for plugins that declare computed functions.
func sampleFunctions(p *plugin.PluginDef) map[string]string {
	fns := make(map[string]string)
	for name := range p.Functions {
		fns[name] = "test-computed-value"
	}
	return fns
}
