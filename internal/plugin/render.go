package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// RenderContext provides agent-level context available to plugin templates.
// This is the stable interface between the generator and plugins — add fields
// here rather than extending function signatures.
type RenderContext struct {
	// Self is the full agent config, exposed as .self in templates.
	// Plugins can access any config field: {{ .self.name }}, {{ .self.runtime.image }}, etc.
	Self map[string]any
}

// RenderContributions resolves Go templates in a plugin's contributions.
// Template data available: .plugin.options (user-provided), .agent (config map).
func RenderContributions(p *PluginDef, opts map[string]any, ctx RenderContext) (*Contributions, error) {
	if err := validateOptions(p.Options, opts); err != nil {
		return nil, err
	}

	// Apply defaults
	resolvedOpts := applyDefaults(p.Options, opts)

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

	data := map[string]any{
		"plugin": map[string]any{"options": resolvedOpts},
		"agent":  ctx.Self,
	}
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
	maps.Copy(resolved, opts)
	for name, s := range schema {
		if _, ok := resolved[name]; !ok && s.Default != nil {
			resolved[name] = s.Default
		}
	}
	return resolved
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
