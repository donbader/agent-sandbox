package generate

import (
	"fmt"
	"strings"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// BuiltinVar defines a generate-time variable that gets resolved during artifact generation.
// Unlike runtime env vars (which go to .env.example), built-in vars are resolved immediately.
type BuiltinVar struct {
	Name        string
	Description string
	Resolve     func(runtime *resolve.RuntimeConfig) string
}

// Builtins is the registry of all built-in variables.
// Add new magic variables here.
var Builtins = []BuiltinVar{
	{
		Name:        "AGENT_HOME",
		Description: "Agent's home directory path (e.g., /home/agent)",
		Resolve: func(runtime *resolve.RuntimeConfig) string {
			return fmt.Sprintf("/home/%s", runtime.User)
		},
	},
	{
		Name:        "AGENT_USER",
		Description: "Agent's username inside the container",
		Resolve: func(runtime *resolve.RuntimeConfig) string {
			return runtime.User
		},
	},
}

// resolveBuiltins replaces all {{ .BUILTIN_VAR }} references in a string with their resolved values.
// Uses Go template-style syntax to distinguish from runtime env vars (${VAR}).
func resolveBuiltins(s string, runtime *resolve.RuntimeConfig) string {
	result := s
	for _, b := range Builtins {
		placeholder := fmt.Sprintf("{{ .%s }}", b.Name)
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, b.Resolve(runtime))
		}
	}
	return result
}

// resolveFeatureBuiltins resolves built-in variables in all feature contribution string values.
func (g *Generator) resolveFeatureBuiltins() {
	for _, f := range g.Features {
		for i, cmd := range f.Commands {
			f.Commands[i] = resolveBuiltins(cmd, g.Runtime)
		}
		for i, hook := range f.EntrypointHooks {
			f.EntrypointHooks[i] = resolveBuiltins(hook, g.Runtime)
		}
		for i, vol := range f.Volumes {
			f.Volumes[i] = resolveBuiltins(vol, g.Runtime)
		}
		if f.HomeOverride != "" {
			f.HomeOverride = resolveBuiltins(f.HomeOverride, g.Runtime)
		}
	}
}
