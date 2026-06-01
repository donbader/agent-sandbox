package claudecode

import (
	"strings"
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	t.Run("defaults version to latest", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "claude-code", "claude-code", map[string]any{})
		require.NoError(t, err)
		assert.True(t, containsInstallCmd(contrib.Commands, "@anthropic-ai/claude-code@latest"),
			"expected install command with @latest, got: %v", contrib.Commands)
	})

	t.Run("uses specified version", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "claude-code", "claude-code", map[string]any{
			"version": "0.2.0",
		})
		require.NoError(t, err)
		assert.True(t, containsInstallCmd(contrib.Commands, "@anthropic-ai/claude-code@0.2.0"),
			"expected install command with @0.2.0, got: %v", contrib.Commands)
	})

	t.Run("sets MITM domain and env var", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "claude-code", "claude-code", map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.anthropic.com"}, contrib.MITMDomains)
		assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, contrib.EnvVars)
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
