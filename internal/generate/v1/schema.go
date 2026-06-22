package v1

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/invopop/jsonschema"
)

func generateSchema(outDir string, pluginsFS fs.FS, projectDir string) error {
	reflector := &jsonschema.Reflector{
		DoNotReference: true,
	}
	schema := reflector.Reflect(&config.Config{})
	schema.Title = "agent-sandbox configuration"
	schema.Description = "Configuration schema for agent-sandbox agent.yaml"

	enrichInstallations(schema, pluginsFS, projectDir)

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outDir, "schema.json"), data, 0644); err != nil {
		return err
	}

	return generateFleetSchema(outDir, pluginsFS, projectDir)
}

func generateFleetSchema(outDir string, pluginsFS fs.FS, projectDir string) error {
	reflector := &jsonschema.Reflector{
		DoNotReference: true,
	}
	schema := reflector.Reflect(&config.FleetConfig{})
	schema.Title = "agent-sandbox fleet configuration"
	schema.Description = "Configuration schema for agent-sandbox fleet.yaml"

	enrichInstallations(schema, pluginsFS, projectDir)

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fleet schema: %w", err)
	}

	return os.WriteFile(filepath.Join(outDir, "fleet-schema.json"), data, 0644)
}

// enrichInstallations walks the schema to find Installation items and adds
// oneOf variants with plugin-specific options schemas.
func enrichInstallations(schema *jsonschema.Schema, pluginsFS fs.FS, projectDir string) {
	plugins := make(map[string]*plugin.PluginDef)

	// Load builtin plugins from bundled FS
	if pluginsFS != nil {
		plugins = loadPluginSchemas(pluginsFS)
	}

	// Load local plugins from project's plugins/ directory
	localPlugins := loadLocalPluginSchemas(projectDir)

	if len(plugins) == 0 && len(localPlugins) == 0 {
		return
	}

	// Build oneOf variants for each builtin plugin
	variants := make([]*jsonschema.Schema, 0, len(plugins)+len(localPlugins)+1)
	allNames := make([]interface{}, 0, len(plugins)+len(localPlugins))

	for name, def := range plugins {
		fullName := "@builtin/" + name
		allNames = append(allNames, fullName)

		variant := &jsonschema.Schema{
			Type:       "object",
			Properties: jsonschema.NewProperties(),
			Required:   []string{"plugin"},
		}
		variant.Properties.Set("plugin", &jsonschema.Schema{
			Type:  "string",
			Const: fullName,
		})
		if len(def.Options) > 0 {
			variant.Properties.Set("options", optionSchemaToJSON(def.Options))
		}
		variants = append(variants, variant)
	}

	// Build oneOf variants for each local plugin
	for refName, def := range localPlugins {
		allNames = append(allNames, refName)

		variant := &jsonschema.Schema{
			Type:       "object",
			Properties: jsonschema.NewProperties(),
			Required:   []string{"plugin"},
		}
		variant.Properties.Set("plugin", &jsonschema.Schema{
			Type:  "string",
			Const: refName,
		})
		if len(def.Options) > 0 {
			variant.Properties.Set("options", optionSchemaToJSON(def.Options))
		}
		variants = append(variants, variant)
	}

	// Fallback for unknown plugins — uses "not enum" to exclude all known plugins,
	// ensuring each installation matches exactly one oneOf branch.
	fallback := &jsonschema.Schema{
		Type:       "object",
		Properties: jsonschema.NewProperties(),
		Required:   []string{"plugin"},
	}
	fallback.Properties.Set("plugin", &jsonschema.Schema{
		Type:        "string",
		Description: "Plugin reference. Use @builtin/name for bundled plugins or @fleet/path for local plugins.",
		Not:         &jsonschema.Schema{Enum: allNames},
	})
	fallback.Properties.Set("options", &jsonschema.Schema{
		Type:        "object",
		Description: "Plugin-specific configuration options",
	})
	variants = append(variants, fallback)

	// Find and replace installations items in schema tree
	patchInstallationItems(schema, variants)
}

// patchInstallationItems recursively searches for the "installations" property
// and replaces its items schema with oneOf variants.
func patchInstallationItems(schema *jsonschema.Schema, variants []*jsonschema.Schema) {
	if schema == nil || schema.Properties == nil {
		return
	}

	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		key := pair.Key
		val := pair.Value

		if key == "installations" && val.Items != nil {
			val.Items.Properties = nil
			val.Items.Type = ""
			val.Items.Required = nil
			val.Items.AdditionalProperties = nil
			val.Items.OneOf = variants
			return
		}

		// Recurse into nested objects (e.g. shared.installations in fleet schema)
		if val.Properties != nil {
			patchInstallationItems(val, variants)
		}
	}
}

// loadPluginSchemas reads all plugin.yaml files from the bundled FS.
func loadPluginSchemas(pluginsFS fs.FS) map[string]*plugin.PluginDef {
	plugins := make(map[string]*plugin.PluginDef)

	entries, err := fs.ReadDir(pluginsFS, ".")
	if err != nil {
		return plugins
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(pluginsFS, filepath.Join(entry.Name(), "plugin.yaml"))
		if err != nil {
			continue
		}
		def, err := plugin.ParsePluginYAML(data)
		if err != nil {
			continue
		}
		plugins[entry.Name()] = def
	}
	return plugins
}

// loadLocalPluginSchemas reads plugin.yaml files from the project's plugins/ directory.
// Returns a map keyed by the reference name (e.g. "@fleet/plugins/telegram-v2").
func loadLocalPluginSchemas(projectDir string) map[string]*plugin.PluginDef {
	plugins := make(map[string]*plugin.PluginDef)
	if projectDir == "" {
		return plugins
	}

	pluginsDir := filepath.Join(projectDir, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return plugins
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pluginsDir, entry.Name(), "plugin.yaml"))
		if err != nil {
			continue
		}
		def, err := plugin.ParsePluginYAML(data)
		if err != nil {
			continue
		}
		refName := "@fleet/plugins/" + entry.Name()
		plugins[refName] = def
	}
	return plugins
}

// optionSchemaToJSON converts a plugin's option definitions to a JSON Schema object.
func optionSchemaToJSON(opts map[string]plugin.OptionSchema) *jsonschema.Schema {
	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: jsonschema.NewProperties(),
	}

	var required []string
	for name, opt := range opts {
		prop := optionToJSONProp(opt)
		schema.Properties.Set(name, prop)
		if opt.Required {
			required = append(required, name)
		}
	}
	if len(required) > 0 {
		schema.Required = required
	}
	return schema
}

// optionToJSONProp converts a single OptionSchema to a JSON Schema property.
func optionToJSONProp(opt plugin.OptionSchema) *jsonschema.Schema {
	prop := &jsonschema.Schema{
		Description: opt.Description,
	}

	switch opt.Type {
	case "string", "project-path":
		prop.Type = "string"
	case "boolean":
		prop.Type = "boolean"
	case "integer", "number":
		prop.Type = "integer"
	case "array":
		prop.Type = "array"
		prop.Items = &jsonschema.Schema{Type: "string"}
	case "object":
		prop.Type = "object"
		if len(opt.Properties) > 0 {
			prop.Properties = jsonschema.NewProperties()
			var required []string
			for k, v := range opt.Properties {
				prop.Properties.Set(k, optionToJSONProp(v))
				if v.Required {
					required = append(required, k)
				}
			}
			if len(required) > 0 {
				prop.Required = required
			}
		}
		if opt.AdditionalProperties != nil {
			ap := optionToJSONProp(*opt.AdditionalProperties)
			prop.AdditionalProperties = ap
		}
	default:
		prop.Type = "string"
	}

	if opt.Default != nil {
		prop.Default = opt.Default
	}
	return prop
}
