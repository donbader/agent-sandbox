package telegram

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	t.Run("returns expected contributions", func(t *testing.T) {
		config := map[string]any{
			"access_control": map[string]any{
				"allowed_users":   []any{"@coreyortea"},
				"require_mention": false,
			},
		}

		contrib, err := resolve.ResolveFeature("/project", "telegram", "telegram", config)
		require.NoError(t, err)
		assert.Equal(t, []string{"api.telegram.org"}, contrib.MITMDomains)
		assert.Equal(t, "telegram", contrib.ChannelName)
		assert.Equal(t, []string{"TELEGRAM_BOT_TOKEN"}, contrib.EnvVars)
	})

	t.Run("works with empty config", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "telegram", "telegram", map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.telegram.org"}, contrib.MITMDomains)
		assert.Equal(t, "telegram", contrib.ChannelName)
		assert.Equal(t, []string{"TELEGRAM_BOT_TOKEN"}, contrib.EnvVars)
	})
}
