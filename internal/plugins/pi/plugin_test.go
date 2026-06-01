package pi

import (
	"strings"
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	t.Run("defaults to anthropic provider", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "pi", "pi", map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.anthropic.com"}, contrib.MITMDomains)
		assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, contrib.EnvVars)
	})

	t.Run("single custom provider", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "pi", "pi", map[string]any{
			"providers": []any{"openai"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.openai.com"}, contrib.MITMDomains)
		assert.Equal(t, []string{"OPENAI_API_KEY"}, contrib.EnvVars)
	})

	t.Run("multiple providers", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "pi", "pi", map[string]any{
			"providers": []any{"anthropic", "openai", "google"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com"}, contrib.MITMDomains)
		assert.Equal(t, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY"}, contrib.EnvVars)
	})

	t.Run("unknown provider is ignored", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "pi", "pi", map[string]any{
			"providers": []any{"anthropic", "unknown-provider"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.anthropic.com"}, contrib.MITMDomains)
		assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, contrib.EnvVars)
	})

	t.Run("defaults version to latest in install command", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "pi", "pi", map[string]any{})
		require.NoError(t, err)
		assert.True(t, containsInstallCmd(contrib.Commands, "@earendil-works/pi-coding-agent@latest"),
			"expected install command with @latest, got: %v", contrib.Commands)
	})

	t.Run("uses specified version in install command", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "pi", "pi", map[string]any{
			"version": "1.2.3",
		})
		require.NoError(t, err)
		assert.True(t, containsInstallCmd(contrib.Commands, "@earendil-works/pi-coding-agent@1.2.3"),
			"expected install command with @1.2.3, got: %v", contrib.Commands)
	})
}

// containsInstallCmd reports whether any command in cmds contains substr.
func containsInstallCmd(cmds []string, substr string) bool {
	for _, c := range cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}
