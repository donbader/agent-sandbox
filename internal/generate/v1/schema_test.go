package v1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSchema(t *testing.T) {
	outDir := t.TempDir()

	err := generateSchema(outDir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(outDir, "schema.json"))
	require.NoError(t, err)

	var schema map[string]any
	err = json.Unmarshal(data, &schema)
	require.NoError(t, err, "schema.json should be valid JSON")

	// Verify it's a JSON Schema
	assert.Contains(t, schema, "$schema")
	assert.Contains(t, schema, "properties")

	// Verify key properties exist
	props, _ := schema["properties"].(map[string]any)
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "runtime")
	assert.Contains(t, props, "gateway")
	assert.Contains(t, props, "installations")

	// Verify required fields
	required, _ := schema["required"].([]any)
	assert.Contains(t, required, "name")
	assert.Contains(t, required, "runtime")

	// Verify nested runtime properties
	runtimeProps, _ := props["runtime"].(map[string]any)["properties"].(map[string]any)
	assert.Contains(t, runtimeProps, "image")
	assert.Contains(t, runtimeProps, "extra_builds")
	assert.Contains(t, runtimeProps, "entrypoint")
}
