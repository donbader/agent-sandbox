package generate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	sandbox "github.com/donbader/agent-sandbox"
	"gopkg.in/yaml.v3"
)

// featureSchema represents a parsed feature.yaml with config_schema.
type featureSchema struct {
	Name         string       `yaml:"name"`
	Description  string       `yaml:"description"`
	ConfigSchema configSchema `yaml:"config_schema"`
}

type configSchema struct {
	Properties map[string]schemaProperty `yaml:"properties"`
}

type schemaProperty struct {
	Type        string       `yaml:"type"`
	Description string       `yaml:"description"`
	Items       *schemaItems `yaml:"items"`
}

type schemaItems struct {
	Type string `yaml:"type"`
}

// writeSchema generates .build/schema.json — a JSON Schema for agent.yaml.
// This enables VSCode YAML extension autocompletion and validation.
func (g *Generator) writeSchema() error {
	schema, err := buildAgentSchema()
	if err != nil {
		return fmt.Errorf("building schema: %w", err)
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}

	path := filepath.Join(g.OutDir, "schema.json")
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// buildAgentSchema generates a JSON Schema describing agent.yaml format.
func buildAgentSchema() (map[string]any, error) {
	featureSchemas, err := collectFeatureSchemas()
	if err != nil {
		return nil, err
	}

	schema := map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"title":   "agent-sandbox agent.yaml",
		"type":    "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Agent name",
			},
			"runtime": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string", "description": "Runtime plugin name (e.g., codex)"},
					map[string]any{"type": "object", "description": "Inline runtime definition"},
				},
			},
			"gateway": map[string]any{
				"type":        "boolean",
				"description": "Enable transparent gateway proxy (default: true)",
			},
			"features": map[string]any{
				"type":        "object",
				"description": "Feature plugins and their configuration",
				"properties":  featureSchemas,
			},
		},
		"required": []string{"name", "runtime"},
	}

	return schema, nil
}

// collectFeatureSchemas reads all embedded plugin feature.yaml files and extracts their config_schema.
func collectFeatureSchemas() (map[string]any, error) {
	schemas := map[string]any{}

	entries, err := fs.ReadDir(sandbox.CorePlugins, "internal/plugins")
	if err != nil {
		return nil, fmt.Errorf("reading embedded plugins: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featurePath := filepath.Join("internal/plugins", entry.Name(), "feature.yaml")
		data, err := sandbox.CorePlugins.ReadFile(featurePath)
		if err != nil {
			continue // not a feature plugin (e.g., runtime-only)
		}

		var feat featureSchema
		if err := yaml.Unmarshal(data, &feat); err != nil {
			continue
		}

		if len(feat.ConfigSchema.Properties) == 0 {
			continue
		}

		schemas[entry.Name()] = convertToJSONSchema(feat.ConfigSchema, feat.Description)
	}

	return schemas, nil
}

// convertToJSONSchema converts a feature's config_schema to JSON Schema format.
func convertToJSONSchema(cs configSchema, description string) map[string]any {
	props := map[string]any{}
	for name, prop := range cs.Properties {
		jsonProp := map[string]any{
			"type": prop.Type,
		}
		if prop.Description != "" {
			jsonProp["description"] = prop.Description
		}
		if prop.Type == "array" && prop.Items != nil {
			jsonProp["items"] = map[string]any{"type": prop.Items.Type}
		}
		props[name] = jsonProp
	}

	result := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if description != "" {
		result["description"] = strings.TrimSpace(description)
	}
	return result
}
