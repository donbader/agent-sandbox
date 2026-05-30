package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
