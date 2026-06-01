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

func TestCollectFeatureItemSchemas(t *testing.T) {
	schemas := collectFeatureItemSchemas()
	require.NotEmpty(t, schemas)

	// Helper to find a plugin schema by name in the oneOf array
	findPlugin := func(name string) map[string]any {
		for _, s := range schemas {
			item := s.(map[string]any)
			props := item["properties"].(map[string]any)
			pluginProp := props["plugin"].(map[string]any)
			if pluginProp["const"] == name {
				return item
			}
		}
		return nil
	}

	t.Run("features schema is array with oneOf items", func(t *testing.T) {
		schema := buildAgentSchema()
		props := schema["properties"].(map[string]any)
		features := props["features"].(map[string]any)
		assert.Equal(t, "array", features["type"])
		items := features["items"].(map[string]any)
		assert.Contains(t, items, "oneOf")
	})

	t.Run("each item has plugin as required const", func(t *testing.T) {
		for _, s := range schemas {
			item := s.(map[string]any)
			props := item["properties"].(map[string]any)
			assert.Contains(t, props, "plugin")
			pluginProp := props["plugin"].(map[string]any)
			assert.Contains(t, pluginProp, "const")

			required := item["required"].([]string)
			assert.Contains(t, required, "plugin")
		}
	})

	t.Run("each item has optional name field", func(t *testing.T) {
		for _, s := range schemas {
			item := s.(map[string]any)
			props := item["properties"].(map[string]any)
			assert.Contains(t, props, "name")
			nameProp := props["name"].(map[string]any)
			assert.Equal(t, "string", nameProp["type"])
		}
	})

	t.Run("includes custom-runtime plugin properties", func(t *testing.T) {
		item := findPlugin("custom-runtime")
		require.NotNil(t, item)
		props := item["properties"].(map[string]any)
		assert.Contains(t, props, "commands")
		assert.Contains(t, props, "entrypoint_hooks")
		assert.Contains(t, props, "runtime_volumes")
		assert.Contains(t, props, "home_override")
		assert.Contains(t, props, "env")
	})

	t.Run("includes telegram plugin properties", func(t *testing.T) {
		item := findPlugin("telegram")
		require.NotNil(t, item)
		props := item["properties"].(map[string]any)
		assert.Contains(t, props, "access_control")

		required := item["required"].([]string)
		assert.Contains(t, required, "access_control")
	})

	t.Run("custom-runtime commands has correct type", func(t *testing.T) {
		item := findPlugin("custom-runtime")
		require.NotNil(t, item)
		props := item["properties"].(map[string]any)
		cmdProp := props["commands"].(map[string]any)
		assert.Equal(t, "array", cmdProp["type"])
		assert.Equal(t, map[string]any{"type": "string"}, cmdProp["items"])
		assert.Equal(t, "Additional RUN commands for the Dockerfile", cmdProp["description"])
	})
}
