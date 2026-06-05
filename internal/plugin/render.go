package plugin

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// RenderContributions resolves Go templates in a plugin's contributions using provided options.
func RenderContributions(p *PluginDef, opts map[string]any) (*Contributions, error) {
	if err := validateOptions(p.Options, opts); err != nil {
		return nil, err
	}

	// Apply defaults
	resolvedOpts := applyDefaults(p.Options, opts)

	// Re-marshal the contributes block to YAML, then template-render it
	contribYAML, err := yaml.Marshal(p.Contributes)
	if err != nil {
		return nil, fmt.Errorf("marshal contributes: %w", err)
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
	}

	tmpl, err := template.New("contrib").Funcs(funcMap).Parse(string(contribYAML))
	if err != nil {
		return nil, fmt.Errorf("parse contributes template: %w", err)
	}

	data := map[string]any{"options": resolvedOpts}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render contributes template: %w", err)
	}

	var rendered Contributions
	if err := yaml.Unmarshal(buf.Bytes(), &rendered); err != nil {
		return nil, fmt.Errorf("parse rendered contributes: %w", err)
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
	for k, v := range opts {
		resolved[k] = v
	}
	for name, s := range schema {
		if _, ok := resolved[name]; !ok && s.Default != nil {
			resolved[name] = s.Default
		}
	}
	return resolved
}
