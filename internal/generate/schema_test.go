package generate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Import plugins to trigger init() registration
	_ "github.com/donbader/agent-sandbox/internal/plugins/custom-runtime"
	_ "github.com/donbader/agent-sandbox/internal/plugins/telegram"
)

func TestStructToJSONSchema(t *testing.T) {
	t.Run("converts struct with string fields", func(t *testing.T) {
		type cfg struct {
			Name string `yaml:"name" schema:"The name"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		assert.Equal(t, "object", schema["type"])
		props := schema["properties"].(map[string]any)
		nameProp := props["name"].(map[string]any)
		assert.Equal(t, "string", nameProp["type"])
		assert.Equal(t, "The name", nameProp["description"])
	})

	t.Run("converts struct with slice fields", func(t *testing.T) {
		type cfg struct {
			Items []string `yaml:"items" schema:"List of items"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props := schema["properties"].(map[string]any)
		itemsProp := props["items"].(map[string]any)
		assert.Equal(t, "array", itemsProp["type"])
		assert.Equal(t, map[string]any{"type": "string"}, itemsProp["items"])
		assert.Equal(t, "List of items", itemsProp["description"])
	})

	t.Run("converts struct with bool fields", func(t *testing.T) {
		type cfg struct {
			Enabled bool `yaml:"enabled" schema:"Whether enabled"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props := schema["properties"].(map[string]any)
		enabledProp := props["enabled"].(map[string]any)
		assert.Equal(t, "boolean", enabledProp["type"])
	})

	t.Run("converts struct with int fields", func(t *testing.T) {
		type cfg struct {
			Count int `yaml:"count" schema:"Number of items"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props := schema["properties"].(map[string]any)
		countProp := props["count"].(map[string]any)
		assert.Equal(t, "integer", countProp["type"])
	})

	t.Run("skips fields without yaml tag", func(t *testing.T) {
		type cfg struct {
			Public  string `yaml:"public"`
			private string //nolint:unused
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props := schema["properties"].(map[string]any)
		assert.Len(t, props, 1)
		assert.Contains(t, props, "public")
	})

	t.Run("skips yaml dash tag", func(t *testing.T) {
		type cfg struct {
			Skipped string `yaml:"-"`
			Kept    string `yaml:"kept"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props := schema["properties"].(map[string]any)
		assert.Len(t, props, 1)
		assert.Contains(t, props, "kept")
	})

	t.Run("returns nil for non-struct", func(t *testing.T) {
		schema := structToJSONSchema("not a struct")
		assert.Nil(t, schema)
	})

	t.Run("returns nil for nil", func(t *testing.T) {
		schema := structToJSONSchema(nil)
		assert.Nil(t, schema)
	})

	t.Run("returns nil for empty struct", func(t *testing.T) {
		type cfg struct{}
		schema := structToJSONSchema(cfg{})
		assert.Nil(t, schema)
	})
}

func TestCollectFeatureSchemas(t *testing.T) {
	schemas := collectFeatureSchemas()

	t.Run("includes custom-runtime schema", func(t *testing.T) {
		schema, ok := schemas["custom-runtime"]
		require.True(t, ok)
		s := schema.(map[string]any)
		assert.Equal(t, "object", s["type"])
		props := s["properties"].(map[string]any)
		assert.Contains(t, props, "commands")
		assert.Contains(t, props, "entrypoint_hooks")
		assert.Contains(t, props, "runtime_volumes")
		assert.Contains(t, props, "home_override")
		assert.Contains(t, props, "env")
	})

	t.Run("includes telegram schema", func(t *testing.T) {
		schema, ok := schemas["telegram"]
		require.True(t, ok)
		s := schema.(map[string]any)
		assert.Equal(t, "object", s["type"])
		props := s["properties"].(map[string]any)
		assert.Contains(t, props, "allowed_chat_ids")
	})

	t.Run("custom-runtime commands has correct type", func(t *testing.T) {
		s := schemas["custom-runtime"].(map[string]any)
		props := s["properties"].(map[string]any)
		cmdProp := props["commands"].(map[string]any)
		assert.Equal(t, "array", cmdProp["type"])
		assert.Equal(t, map[string]any{"type": "string"}, cmdProp["items"])
		assert.Equal(t, "Additional RUN commands for the Dockerfile", cmdProp["description"])
	})
}
