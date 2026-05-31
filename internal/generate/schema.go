package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// writeSchema generates .build/schema.json — a JSON Schema for agent.yaml.
// This enables VSCode YAML extension autocompletion and validation.
func (g *Generator) writeSchema() error {
	schema := buildAgentSchema()

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}

	path := filepath.Join(g.OutDir, "schema.json")
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// buildAgentSchema generates a JSON Schema describing agent.yaml format.
func buildAgentSchema() map[string]any {
	featureSchemas := collectFeatureSchemas()

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

	return schema
}

// collectFeatureSchemas uses reflection on registered plugins' ConfigType()
// to generate JSON Schema for each plugin's configuration.
func collectFeatureSchemas() map[string]any {
	schemas := map[string]any{}
	for name, plugin := range resolve.RegisteredPlugins() {
		configType := plugin.ConfigType()
		schema := structToJSONSchema(configType)
		if schema != nil {
			schemas[name] = schema
		}
	}
	return schemas
}

// structToJSONSchema converts a struct to JSON Schema using reflection and struct tags.
func structToJSONSchema(v any) map[string]any {
	if v == nil {
		return nil
	}
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	if t.NumField() == 0 {
		return nil
	}

	props := map[string]any{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}
		name := strings.Split(yamlTag, ",")[0]

		prop := typeToSchema(field.Type)

		if desc := field.Tag.Get("schema"); desc != "" {
			prop["description"] = desc
		}

		props[name] = prop
	}

	return map[string]any{
		"type":       "object",
		"properties": props,
	}
}

// typeToSchema converts a reflect.Type to a JSON Schema property definition.
func typeToSchema(t reflect.Type) map[string]any {
	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		schema := typeToSchema(t.Elem())
		return schema
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		prop := map[string]any{"type": "array"}
		prop["items"] = typeToSchema(t.Elem())
		return prop
	case reflect.Map:
		prop := map[string]any{"type": "object"}
		prop["additionalProperties"] = typeToSchema(t.Elem())
		return prop
	case reflect.Struct:
		// Recursively expand struct fields
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			yamlTag := field.Tag.Get("yaml")
			if yamlTag == "" || yamlTag == "-" {
				continue
			}
			name := strings.Split(yamlTag, ",")[0]
			fieldSchema := typeToSchema(field.Type)
			if desc := field.Tag.Get("schema"); desc != "" {
				fieldSchema["description"] = desc
			}
			props[name] = fieldSchema
		}
		return map[string]any{"type": "object", "properties": props}
	default:
		return map[string]any{"type": "object"}
	}
}
