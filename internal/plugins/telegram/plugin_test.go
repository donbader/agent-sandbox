package telegram

import (
	"testing"

	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllowedChatIDs(t *testing.T) {
	t.Run("extracts chat IDs from config", func(t *testing.T) {
		ids := AllowedChatIDs(map[string]any{
			"allowed_chat_ids": []any{"123", "456"},
		})
		assert.Equal(t, []string{"123", "456"}, ids)
	})

	t.Run("returns nil when not configured", func(t *testing.T) {
		ids := AllowedChatIDs(map[string]any{})
		assert.Nil(t, ids)
	})

	t.Run("returns nil for wrong type", func(t *testing.T) {
		ids := AllowedChatIDs(map[string]any{
			"allowed_chat_ids": "not-an-array",
		})
		assert.Nil(t, ids)
	})
}

func TestResolve(t *testing.T) {
	t.Run("returns expected contributions", func(t *testing.T) {
		config := map[string]any{
			"allowed_chat_ids": []any{"123", "456"},
		}

		contrib, err := resolve.ResolveFeature("/project", "telegram", config)
		require.NoError(t, err)
		assert.Equal(t, []string{"api.telegram.org"}, contrib.MITMDomains)
		assert.Equal(t, "telegram", contrib.BridgeChannel)
		assert.Equal(t, []string{"TELEGRAM_BOT_TOKEN"}, contrib.EnvVars)
	})

	t.Run("works with empty config", func(t *testing.T) {
		contrib, err := resolve.ResolveFeature("/project", "telegram", map[string]any{})
		require.NoError(t, err)
		assert.Equal(t, []string{"api.telegram.org"}, contrib.MITMDomains)
		assert.Equal(t, "telegram", contrib.BridgeChannel)
		assert.Equal(t, []string{"TELEGRAM_BOT_TOKEN"}, contrib.EnvVars)
	})
}
