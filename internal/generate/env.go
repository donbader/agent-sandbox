package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
)

// scanEnvVars finds all ${VAR} references in the agent config (recursively).
var envVarPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// ScanConfigEnvVars finds all ${VAR} references in the given feature entries.
// Exported for use by fleet-level .env.example generation.
func ScanConfigEnvVars(features []config.FeatureEntry) []string {
	sources := map[string][]string{}
	var order []string

	for i, entry := range features {
		source := fmt.Sprintf("features[%d]", i)
		if entry.Name != "" {
			source = entry.Name
		}
		keys := sortedKeys(entry.Config)
		for _, key := range keys {
			scanValueWithSource(entry.Config[key], envVarPattern, sources, &order,
				fmt.Sprintf("feature:%s.%s", source, key))
		}
	}

	return order
}

func (g *Generator) scanEnvVars() []string {
	sources := map[string][]string{} // var name → list of sources
	var order []string               // preserve insertion order

	// Scan config for ${VAR} references
	for i, entry := range g.Config.Features {
		source := fmt.Sprintf("features[%d]", i)
		if entry.Name != "" {
			source = entry.Name
		}
		keys := sortedKeys(entry.Config)
		for _, key := range keys {
			scanValueWithSource(entry.Config[key], envVarPattern, sources, &order,
				fmt.Sprintf("feature:%s.%s", source, key))
		}
	}

	// Warn about env vars defined in multiple places
	for _, name := range order {
		if len(sources[name]) > 1 {
			fmt.Fprintf(os.Stderr, "warning: env var %s defined in multiple places: %s\n",
				name, strings.Join(sources[name], ", "))
		}
	}

	return order
}

// scanValueWithSource recursively walks a value and extracts ${VAR} references,
// tracking the source location for conflict warnings.
func scanValueWithSource(v any, pattern *regexp.Regexp, sources map[string][]string, order *[]string, source string) {
	switch val := v.(type) {
	case string:
		matches := pattern.FindAllStringSubmatch(val, -1)
		for _, m := range matches {
			name := m[1]
			if _, exists := sources[name]; !exists {
				*order = append(*order, name)
			}
			sources[name] = append(sources[name], source)
		}
	case []any:
		for _, item := range val {
			scanValueWithSource(item, pattern, sources, order, source)
		}
	case map[string]any:
		keys := sortedKeys(val)
		for _, k := range keys {
			scanValueWithSource(val[k], pattern, sources, order, source+"."+k)
		}
	}
}

// sortedKeys returns the keys of a map[string]any in sorted order for deterministic output.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mergedEnvVars returns all env vars from config ${VAR} references, deduplicated and sorted alphabetically.
func (g *Generator) mergedEnvVars() []string {
	vars := g.scanEnvVars()
	sort.Strings(vars)
	return vars
}

// writeEnvExample generates .env.example at the project root (next to agent.yaml).
func (g *Generator) writeEnvExample() error {
	envVars := g.mergedEnvVars()
	if len(envVars) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("# Environment variables for agent-sandbox\n")
	b.WriteString("# Copy to .env and fill in values\n\n")
	for _, v := range envVars {
		_, _ = fmt.Fprintf(&b, "%s=\n", v)
	}

	path := filepath.Join(g.Dir, ".env.example")
	return os.WriteFile(path, []byte(b.String()), 0644)
}
