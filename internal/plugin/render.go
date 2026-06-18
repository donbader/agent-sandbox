package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// RenderContext provides agent-level context available to plugin templates.
// This is the stable interface between the generator and plugins — add fields
// here rather than extending function signatures.
type RenderContext struct {
	// Self is the full agent config, exposed as .agent in templates.
	Self map[string]any

	// Generator holds framework-provided values, exposed as .generator in templates.
	// Always available to all plugins (e.g. {{ .generator.core_version }}).
	Generator map[string]any

	// Functions holds computed function results (name → value).
	// Populated by executing plugin-declared scripts at generate time.
	// Injected into .plugin.<name> for plugins that declare the function.
	Functions map[string]string

	// ProjectRoot is the relative path from the agent directory to the project root.
	// Used to resolve @fleet/ prefixed paths in plugin options.
	// For fleet mode this is ".." ; for standalone mode this is ".".
	ProjectRoot string
}

// RenderContributions resolves Go templates in a plugin's contributions.
// Template data available: .plugin.options (user-provided), .agent (config map).
func RenderContributions(p *PluginDef, opts map[string]any, ctx RenderContext) (*Contributions, error) {
	if err := validateOptions(p.Options, opts); err != nil {
		return nil, err
	}

	// Apply defaults
	resolvedOpts := applyDefaults(p.Options, opts)

	// Resolve @fleet/ prefixed paths to agent-relative paths
	resolveFleetPaths(resolvedOpts, ctx.ProjectRoot)

	// Use raw contributes template (preserved from plugin.yaml without YAML parsing)
	contribTemplate := p.ContributesRaw
	if contribTemplate == "" {
		return &Contributions{}, nil
	}

	funcMap := template.FuncMap{
		"asset": func(name string) string {
			if p.AssetPaths != nil {
				if path, ok := p.AssetPaths[name]; ok {
					return path
				}
			}
			// Fallback: return as-is (local plugins reference relative to project)
			return name
		},
		"toJSON": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("toJSON: %w", err)
			}
			return string(b), nil
		},
	}

	tmpl, err := template.New("contrib").Funcs(funcMap).Parse(contribTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse contributes template: %w", err)
	}

	// Build the .plugin template data with options + injected computed functions
	pluginData := map[string]any{"options": resolvedOpts}

	// Inject computed values for declared functions
	for fn := range p.Functions {
		val, ok := ctx.Functions[fn]
		if !ok {
			return nil, fmt.Errorf("plugin %q declares function %q but it was not computed", p.Name, fn)
		}
		result := val // capture for closure
		pluginData[fn] = func() string { return result }
	}

	data := map[string]any{
		"plugin":    pluginData,
		"agent":     ctx.Self,
		"generator": ctx.Generator,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render contributes template: %w", err)
	}

	var rendered Contributions
	decoder := yaml.NewDecoder(bytes.NewReader(buf.Bytes()))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rendered); err != nil {
		return nil, fmt.Errorf("parse rendered contributes for plugin %q: %w", p.Name, err)
	}

	return &rendered, nil
}

func validateOptions(schema map[string]OptionSchema, opts map[string]any) error {
	for name, s := range schema {
		if s.Required {
			if _, ok := opts[name]; !ok {
				return fmt.Errorf("required option %q not provided", name)
			}
		}
		if val, ok := opts[name]; ok {
			if str, ok := val.(string); ok {
				// Allow @fleet/ prefixed paths — they are resolved separately.
				if strings.HasPrefix(str, "@fleet/") {
					continue
				}
				if strings.Contains(str, "..") {
					return fmt.Errorf("option %q contains path traversal sequence", name)
				}
			}
		}
	}
	return nil
}

func applyDefaults(schema map[string]OptionSchema, opts map[string]any) map[string]any {
	resolved := make(map[string]any, len(opts))
	maps.Copy(resolved, opts)
	for name, s := range schema {
		if _, ok := resolved[name]; !ok && s.Default != nil {
			resolved[name] = s.Default
		}
	}
	return resolved
}

// resolveFleetPaths expands @fleet/ prefixed values in plugin options.
// @fleet/X resolves to <projectRoot>/X relative to the agent directory.
// This allows fleet-level shared resources to be referenced without path traversal.
// For example, @fleet/dorey-home becomes ../dorey-home when ProjectRoot is "..".
func resolveFleetPaths(opts map[string]any, projectRoot string) {
	if projectRoot == "" {
		projectRoot = "."
	}
	for key, val := range opts {
		if str, ok := val.(string); ok {
			if strings.HasPrefix(str, "@fleet/") {
				relPath := strings.TrimPrefix(str, "@fleet/")
				opts[key] = filepath.Join(projectRoot, relPath)
			}
		}
	}
}

// ConfigToMap converts any config struct to a map[string]any via YAML round-trip.
// This keeps plugin templates in sync with the config struct without manual field mapping.
func ConfigToMap(cfg any) map[string]any {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}
