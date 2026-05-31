package generate

import (
	"reflect"
	"strings"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// collectFeatureSchemas uses reflection on registered plugins' ConfigType()
// to generate JSON Schema from struct tags (single source of truth).
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
	t := reflect.TypeOf(v)
	if t == nil {
		return nil
	}
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
		// Take first part of yaml tag (before comma)
		name := strings.Split(yamlTag, ",")[0]

		prop := map[string]any{}
		switch field.Type.Kind() {
		case reflect.String:
			prop["type"] = "string"
		case reflect.Slice:
			prop["type"] = "array"
			if field.Type.Elem().Kind() == reflect.String {
				prop["items"] = map[string]any{"type": "string"}
			}
		case reflect.Bool:
			prop["type"] = "boolean"
		case reflect.Int, reflect.Int64:
			prop["type"] = "integer"
		default:
			prop["type"] = "object"
		}

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
