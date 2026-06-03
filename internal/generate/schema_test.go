package generate

import (
	"os"
	"path/filepath"
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
		props, ok := schema["properties"].(map[string]any)
		require.True(t, ok)
		nameProp, ok := props["name"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "string", nameProp["type"])
		assert.Equal(t, "The name", nameProp["description"])
	})

	t.Run("converts struct with slice fields", func(t *testing.T) {
		type cfg struct {
			Items []string `yaml:"items" schema:"List of items"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props, ok := schema["properties"].(map[string]any)
		require.True(t, ok)
		itemsProp, ok := props["items"].(map[string]any)
		require.True(t, ok)
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
		props, _ := schema["properties"].(map[string]any)
		enabledProp, _ := props["enabled"].(map[string]any)
		assert.Equal(t, "boolean", enabledProp["type"])
	})

	t.Run("converts struct with int fields", func(t *testing.T) {
		type cfg struct {
			Count int `yaml:"count" schema:"Number of items"`
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props, _ := schema["properties"].(map[string]any)
		countProp, _ := props["count"].(map[string]any)
		assert.Equal(t, "integer", countProp["type"])
	})

	t.Run("skips fields without yaml tag", func(t *testing.T) {
		type cfg struct {
			Public  string `yaml:"public"`
			private string //nolint:unused
		}
		schema := structToJSONSchema(cfg{})
		require.NotNil(t, schema)
		props, _ := schema["properties"].(map[string]any)
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
		props, _ := schema["properties"].(map[string]any)
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
			item, _ := s.(map[string]any)
			props, _ := item["properties"].(map[string]any)
			pluginProp, _ := props["plugin"].(map[string]any)
			if pluginProp["const"] == name {
				return item
			}
		}
		return nil
	}

	t.Run("features schema is array with oneOf items", func(t *testing.T) {
		schema := buildAgentSchema()
		props, _ := schema["properties"].(map[string]any)
		features, _ := props["features"].(map[string]any)
		assert.Equal(t, "array", features["type"])
		items, _ := features["items"].(map[string]any)
		assert.Contains(t, items, "oneOf")
	})

	t.Run("each item has plugin as required const", func(t *testing.T) {
		for _, s := range schemas {
			item, _ := s.(map[string]any)
			props, _ := item["properties"].(map[string]any)
			assert.Contains(t, props, "plugin")
			pluginProp, _ := props["plugin"].(map[string]any)
			assert.Contains(t, pluginProp, "const")

			required, _ := item["required"].([]string)
			assert.Contains(t, required, "plugin")
		}
	})

	t.Run("each item has optional name field", func(t *testing.T) {
		for _, s := range schemas {
			item, _ := s.(map[string]any)
			props, _ := item["properties"].(map[string]any)
			assert.Contains(t, props, "name")
			nameProp, _ := props["name"].(map[string]any)
			assert.Equal(t, "string", nameProp["type"])
		}
	})

	t.Run("includes custom-runtime plugin properties", func(t *testing.T) {
		item := findPlugin("custom-runtime")
		require.NotNil(t, item)
		props, _ := item["properties"].(map[string]any)
		assert.Contains(t, props, "commands")
		assert.Contains(t, props, "entrypoint_hooks")
		assert.Contains(t, props, "runtime_volumes")
		assert.Contains(t, props, "home_override")
		assert.Contains(t, props, "env")
	})

	t.Run("includes telegram plugin properties", func(t *testing.T) {
		item := findPlugin("telegram")
		require.NotNil(t, item)
		props, _ := item["properties"].(map[string]any)
		assert.Contains(t, props, "access_control")

		required, _ := item["required"].([]string)
		assert.Contains(t, required, "access_control")
	})

	t.Run("custom-runtime commands has correct type", func(t *testing.T) {
		item := findPlugin("custom-runtime")
		require.NotNil(t, item)
		props, _ := item["properties"].(map[string]any)
		cmdProp, _ := props["commands"].(map[string]any)
		assert.Equal(t, "array", cmdProp["type"])
		assert.Equal(t, map[string]any{"type": "string"}, cmdProp["items"])
		assert.Equal(t, "Additional RUN commands for the Dockerfile", cmdProp["description"])
	})
}

func TestBuildFleetSchema(t *testing.T) {
	schema := buildFleetSchema()

	t.Run("has correct title and type", func(t *testing.T) {
		assert.Equal(t, "agent-sandbox fleet.yaml", schema["title"])
		assert.Equal(t, "object", schema["type"])
	})

	t.Run("has agents property as string array", func(t *testing.T) {
		props, _ := schema["properties"].(map[string]any)
		agents, _ := props["agents"].(map[string]any)
		assert.Equal(t, "array", agents["type"])
		items, _ := agents["items"].(map[string]any)
		assert.Equal(t, "string", items["type"])
	})

	t.Run("has shared.features with oneOf items", func(t *testing.T) {
		props, _ := schema["properties"].(map[string]any)
		shared, _ := props["shared"].(map[string]any)
		assert.Equal(t, "object", shared["type"])
		sharedProps, _ := shared["properties"].(map[string]any)
		features, _ := sharedProps["features"].(map[string]any)
		assert.Equal(t, "array", features["type"])
		items, _ := features["items"].(map[string]any)
		oneOf, _ := items["oneOf"].([]any)
		assert.NotEmpty(t, oneOf)
	})

	t.Run("agents is required", func(t *testing.T) {
		required, _ := schema["required"].([]string)
		assert.Contains(t, required, "agents")
	})
}

func TestWriteFleetSchema(t *testing.T) {
	outDir := t.TempDir()
	err := WriteFleetSchema(outDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outDir, "fleet-schema.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent-sandbox fleet.yaml")
	assert.Contains(t, string(data), "agents")
	assert.Contains(t, string(data), "shared")
}
