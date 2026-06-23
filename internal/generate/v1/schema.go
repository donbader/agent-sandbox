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
	enrichRuntimeImage(schema, projectDir)

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
// if/then conditionals for plugin-specific options autocompletion.
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

	// Collect all plugin names for enum (autocompletion of plugin field)
	allNames := make([]interface{}, 0, len(plugins)+len(localPlugins))

	// Build if/then conditionals for each plugin
	conditionals := make([]*jsonschema.Schema, 0, len(plugins)+len(localPlugins))

	for name, def := range plugins {
		fullName := "@builtin/" + name
		allNames = append(allNames, fullName)

		if len(def.Options) > 0 {
			conditionals = append(conditionals, makeIfThen(fullName, def.Options))
		}
	}

	for refName, def := range localPlugins {
		allNames = append(allNames, refName)

		if len(def.Options) > 0 {
			conditionals = append(conditionals, makeIfThen(refName, def.Options))
		}
	}

	// Find and replace installations items in schema tree
	patchInstallationItems(schema, allNames, conditionals)
}

// makeIfThen creates an if/then schema: if plugin==name, then options has specific shape.
func makeIfThen(pluginName string, opts map[string]plugin.OptionSchema) *jsonschema.Schema {
	ifSchema := &jsonschema.Schema{
		Properties: jsonschema.NewProperties(),
	}
	ifSchema.Properties.Set("plugin", &jsonschema.Schema{
		Const: pluginName,
	})

	thenSchema := &jsonschema.Schema{
		Properties: jsonschema.NewProperties(),
	}
	thenSchema.Properties.Set("options", optionSchemaToJSON(opts))

	return &jsonschema.Schema{
		If:   ifSchema,
		Then: thenSchema,
	}
}

// patchInstallationItems recursively searches for the "installations" property
// and replaces its items schema with base properties + if/then conditionals.
func patchInstallationItems(schema *jsonschema.Schema, pluginNames []interface{}, conditionals []*jsonschema.Schema) {
	if schema == nil || schema.Properties == nil {
		return
	}

	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		key := pair.Key
		val := pair.Value

		if key == "installations" && val.Items != nil {
			// Base schema: plugin (enum for autocompletion) + options (object)
			val.Items.Properties = jsonschema.NewProperties()
			val.Items.Type = "object"
			val.Items.Required = []string{"plugin"}
			val.Items.AdditionalProperties = jsonschema.FalseSchema

			val.Items.Properties.Set("plugin", &jsonschema.Schema{
				Type:        "string",
				Enum:        pluginNames,
				Description: "Plugin reference. Use @builtin/name for bundled plugins or @fleet/path for local plugins.",
			})
			val.Items.Properties.Set("options", &jsonschema.Schema{
				Type:        "object",
				Description: "Plugin-specific configuration options (varies by plugin)",
			})

			// Conditional options based on plugin value
			val.Items.AllOf = conditionals
			val.Items.OneOf = nil
			return
		}

		// Recurse into nested objects (e.g. shared.installations in fleet schema)
		if val.Properties != nil {
			patchInstallationItems(val, pluginNames, conditionals)
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

// loadLocalPluginSchemas recursively scans the project directory for plugin.yaml files.
// Returns a map keyed by the reference name (e.g. "@fleet/plugins/telegram-v2").
func loadLocalPluginSchemas(projectDir string) map[string]*plugin.PluginDef {
	plugins := make(map[string]*plugin.PluginDef)
	if projectDir == "" {
		return plugins
	}

	// Skip directories that shouldn't contain user plugins
	skipDirs := map[string]bool{
		".build":       true,
		".git":         true,
		"node_modules": true,
		"dist":         true,
		".cache":       true,
	}

	_ = filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() != "plugin.yaml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		def, err := plugin.ParsePluginYAML(data)
		if err != nil {
			return nil
		}

		// Reference name is @fleet/ + relative path to the directory containing plugin.yaml
		pluginDir := filepath.Dir(path)
		relDir, err := filepath.Rel(projectDir, pluginDir)
		if err != nil {
			return nil
		}
		refName := "@fleet/" + filepath.ToSlash(relDir)
		plugins[refName] = def
		return nil
	})

	return plugins
}

// enrichRuntimeImage adds examples for the runtime.image field based on available presets.
func enrichRuntimeImage(schema *jsonschema.Schema, projectDir string) {
	// Discover presets from the core directory (look relative to project's .build)
	presetNames := []string{"@builtin/pi", "@builtin/claude-code", "@builtin/codex"}

	if schema.Properties == nil {
		return
	}
	runtimePair := schema.Properties.GetPair("runtime")
	if runtimePair == nil || runtimePair.Value.Properties == nil {
		return
	}
	imagePair := runtimePair.Value.Properties.GetPair("image")
	if imagePair == nil {
		return
	}

	examples := make([]interface{}, len(presetNames))
	for i, name := range presetNames {
		examples[i] = name
	}
	imagePair.Value.Examples = examples
	imagePair.Value.Description = "Base image. Use a builtin preset (@builtin/pi, @builtin/claude-code, @builtin/codex) or any Docker image (e.g. node:24-slim)."
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
