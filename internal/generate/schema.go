package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	featureItemSchemas := collectFeatureItemSchemas()

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
				"type":        "string",
				"description": "Runtime plugin name",
				"enum":        []any{"codex"},
			},
			"gateway": map[string]any{
				"type":        "boolean",
				"description": "Enable transparent gateway proxy",
				"default":     true,
			},
			"features": map[string]any{
				"type":        "array",
				"description": "Feature plugins and their configuration",
				"items": map[string]any{
					"oneOf": featureItemSchemas,
				},
			},
		},
		"required": []string{"name", "runtime"},
	}

	return schema
}

// collectFeatureItemSchemas builds a oneOf array where each item is a plugin schema
// with a discriminator on the "plugin" field.
func collectFeatureItemSchemas() []any {
	var schemas []any
	for name, plugin := range resolve.RegisteredPlugins() {
		configType := plugin.ConfigType()
		pluginSchema := structToJSONSchema(configType)

		// Build properties: plugin (const) + name (optional) + plugin-specific fields
		props := map[string]any{
			"plugin": map[string]any{
				"const":       name,
				"description": "Plugin type",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Optional instance name for logging (defaults to features[i])",
			},
		}

		var required []string
		required = append(required, "plugin")

		// Merge plugin-specific properties
		if pluginSchema != nil {
			if pluginProps, ok := pluginSchema["properties"].(map[string]any); ok {
				for k, v := range pluginProps {
					props[k] = v
				}
			}
			// Carry over plugin-specific required fields
			if pluginRequired, ok := pluginSchema["required"].([]string); ok {
				required = append(required, pluginRequired...)
			}
		}

		itemSchema := map[string]any{
			"type":                 "object",
			"properties":          props,
			"required":            required,
			"additionalProperties": false,
		}

		schemas = append(schemas, itemSchema)
	}
	return schemas
}

// Supported struct tags for schema generation:
//
//	yaml:"field_name"       → JSON Schema property name
//	schema:"description"    → description
//	default:"value"         → default value (parsed by type: bool, int, string)
//	enum:"a,b,c"            → enum constraint (comma-separated)
//	examples:"a,b"          → examples array (comma-separated)
//	pattern:"^@"            → regex pattern (strings only)
//	required:"true"         → adds field to parent's required array
//	deprecated:"true"       → marks field as deprecated

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

	return structTypeToSchema(t)
}

// structTypeToSchema converts a reflect.Type (must be struct) to a JSON Schema object.
func structTypeToSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}
		name := strings.Split(yamlTag, ",")[0]

		prop := typeToSchema(field.Type)
		enrichFromTags(prop, field)

		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}

		props[name] = prop
	}

	result := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

// enrichFromTags reads struct tags and adds JSON Schema annotations to the property.
func enrichFromTags(prop map[string]any, field reflect.StructField) {
	if desc := field.Tag.Get("schema"); desc != "" {
		prop["description"] = desc
	}

	if def := field.Tag.Get("default"); def != "" {
		prop["default"] = parseDefault(def, field.Type)
	}

	if enum := field.Tag.Get("enum"); enum != "" {
		values := strings.Split(enum, ",")
		enumAny := make([]any, len(values))
		for i, v := range values {
			enumAny[i] = strings.TrimSpace(v)
		}
		prop["enum"] = enumAny
	}

	if examples := field.Tag.Get("examples"); examples != "" {
		values := strings.Split(examples, ",")
		exAny := make([]any, len(values))
		for i, v := range values {
			exAny[i] = strings.TrimSpace(v)
		}
		prop["examples"] = exAny
	}

	if pattern := field.Tag.Get("pattern"); pattern != "" {
		prop["pattern"] = pattern
	}

	if field.Tag.Get("deprecated") == "true" {
		prop["deprecated"] = true
	}
}

// parseDefault converts a string default value to the appropriate Go type.
func parseDefault(val string, t reflect.Type) any {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return val == "true"
	case reflect.Int, reflect.Int64:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	default:
		return val
	}
}

// typeToSchema converts a reflect.Type to a JSON Schema property definition.
func typeToSchema(t reflect.Type) map[string]any {
	// Dereference pointer
	if t.Kind() == reflect.Ptr {
		return typeToSchema(t.Elem())
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
		return structTypeToSchema(t)
	default:
		return map[string]any{"type": "object"}
	}
}
