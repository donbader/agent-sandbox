package plugin

import (
	"fmt"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"gopkg.in/yaml.v3"
)

// PluginDef represents a parsed plugin.yaml file.
type PluginDef struct {
	Name           string                  `yaml:"name"`
	Requires       []string                `yaml:"requires"`
	Functions      map[string]FunctionDef  `yaml:"functions"`
	Assets         []AssetEntry            `yaml:"assets"`
	Options        map[string]OptionSchema `yaml:"options"`
	Contributes    Contributions           `yaml:"-"` // populated after template rendering
	ContributesRaw string                  `yaml:"-"` // raw YAML template for contributes block
	BaseDir        string                  `yaml:"-"` // directory where plugin.yaml lives (for resolving relative paths)
	AssetPaths     map[string]string       `yaml:"-"` // resolved asset paths (set by generator after extraction)
}

// FunctionDef declares a computed function provided by a plugin.
type FunctionDef struct {
	Script string `yaml:"script"`
}

// AssetEntry describes a plugin asset directory with optional exclude patterns.
// Supports both simple string ("agent-manager/") and extended form:
//
//	- path: agent-manager/
//	  exclude: [node_modules, dist]
type AssetEntry struct {
	Path    string   `yaml:"path"`
	Exclude []string `yaml:"exclude"`
}

// UnmarshalYAML allows AssetEntry to be either a plain string or a map with path/exclude.
func (a *AssetEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		a.Path = value.Value
		return nil
	}
	if value.Kind == yaml.MappingNode {
		type raw struct {
			Path    string   `yaml:"path"`
			Exclude []string `yaml:"exclude"`
		}
		var r raw
		if err := value.Decode(&r); err != nil {
			return err
		}
		a.Path = r.Path
		a.Exclude = r.Exclude
		return nil
	}
	return fmt.Errorf("asset entry must be a string or map, got %v", value.Kind)
}

type OptionSchema struct {
	Type        string                  `yaml:"type"`
	Required    bool                    `yaml:"required"`
	Default     any                     `yaml:"default"`
	Description string                  `yaml:"description"`
	Properties  map[string]OptionSchema `yaml:"properties"`
	Items       *OptionSchema           `yaml:"items"`
}

type Contributions struct {
	Runtime RuntimeContrib `yaml:"runtime"`
	Gateway GatewayContrib `yaml:"gateway"`
	Sidecar SidecarContrib `yaml:"sidecar"`
}

// BuildStage declares an isolated Docker build stage contributed by a plugin.
type BuildStage struct {
	Base      string                 `yaml:"base"`      // optional, defaults to main base image
	Steps     []string               `yaml:"steps"`     // Dockerfile instructions for the build stage
	Artifacts []config.StageArtifact `yaml:"artifacts"` // what to COPY --from into the final image
}

// NamedBuildStage is a BuildStage with its resolved stage name, populated after render.
type NamedBuildStage struct {
	Name      string                 // Docker stage name: "build-{Name}"
	Base      string
	Steps     []string
	Artifacts []config.StageArtifact
}

type RuntimeContrib struct {
	BuildStage        *BuildStage        `yaml:"build_stage"`  // from plugin YAML; promoted to BuildStages by RenderContributions
	BuildStages       []NamedBuildStage  `yaml:"-"`            // accumulated after render; merged by MergeContributions
	ExtraBuilds       []string           `yaml:"extra_builds"`
	Environment       map[string]string  `yaml:"environment"`
	PreEntrypoint     []string           `yaml:"pre_entrypoint"`
	Ports             []string           `yaml:"ports"`
	NamespacedVolumes []string           `yaml:"namespaced_volumes"` // auto-prefixed with {agentName}- at compose generation
	RawVolumes        []string           `yaml:"raw_volumes"`        // used as-is, no agent name prefix
	CapAdd            []string           `yaml:"cap_add"`            // validated at install time if plugin source is remote
	SkipUserns        bool               `yaml:"skip_userns"`
}

type GatewayContrib struct {
	Egress            []config.EgressRule `yaml:"egress"`
	NamespacedVolumes []string            `yaml:"namespaced_volumes"` // auto-prefixed with {agentName}- at compose generation
	RawVolumes        []string            `yaml:"raw_volumes"`        // used as-is, no agent name prefix
	Routes            []RouteEntry        `yaml:"routes"`
}

// RouteEntry declares an HTTP route handler contributed by a plugin.
// The path is relative to the plugin's namespace (/plugins/{plugin-name}/...).
type RouteEntry struct {
	Path    string `yaml:"path"`    // relative path (e.g. "/callback")
	Handler string `yaml:"handler"` // path to handler .ts/.go file
}

type SidecarContrib struct {
	Services map[string]ComposeService `yaml:"services"`
}

// ComposeService follows docker-compose service spec (subset).
type ComposeService struct {
	Build       string            `yaml:"build"`
	Image       string            `yaml:"image"`
	Environment map[string]string `yaml:"environment"`
	Ports       []string          `yaml:"ports"`
	Volumes     []string          `yaml:"volumes"`
	DependsOn   any               `yaml:"depends_on"`
	Healthcheck *Healthcheck      `yaml:"healthcheck"`
	Networks    []string          `yaml:"networks"`
	CapAdd      []string          `yaml:"cap_add"`
	SecurityOpt []string          `yaml:"security_opt"`
}

type Healthcheck struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval"`
	Timeout  string   `yaml:"timeout"`
	Retries  int      `yaml:"retries"`
}

// ParsePluginYAML parses raw YAML bytes into a PluginDef.
// The contributes block is kept as raw template text (not parsed as YAML)
// because it may contain Go template directives that generate YAML structure.
func ParsePluginYAML(data []byte) (*PluginDef, error) {
	// First, try a full YAML parse. This works for most plugins where contributes
	// only has templates inside string values (valid YAML).
	var full struct {
		Name        string                  `yaml:"name"`
		Requires    []string                `yaml:"requires"`
		Functions   map[string]FunctionDef  `yaml:"functions"`
		Assets      []AssetEntry            `yaml:"assets"`
		Options     map[string]OptionSchema `yaml:"options"`
		Contributes Contributions           `yaml:"contributes"`
	}
	if err := yaml.Unmarshal(data, &full); err == nil && full.Name != "" {
		// Full parse succeeded — extract raw contributes and re-serialize via map[string]any
		// to unescape YAML string values (\" → ") while preserving unknown fields
		// for strict validation during render.
		contributesRaw, _ := splitContributesBlock(data)
		var contribMap map[string]any
		if err := yaml.Unmarshal([]byte(contributesRaw), &contribMap); err == nil && contribMap != nil {
			out, err := yaml.Marshal(contribMap)
			if err == nil {
				contributesRaw = string(out)
			}
		}
		return &PluginDef{
			Name:           full.Name,
			Requires:       full.Requires,
			Functions:      full.Functions,
			Assets:         full.Assets,
			Options:        full.Options,
			ContributesRaw: contributesRaw,
		}, nil
	}

	// Full YAML parse failed (structural templates like {{- range }}).
	// Extract contributes as raw text, parse metadata separately.
	contributesRaw, metadataOnly := splitContributesBlock(data)

	var meta struct {
		Name      string                  `yaml:"name"`
		Requires  []string                `yaml:"requires"`
		Functions map[string]FunctionDef  `yaml:"functions"`
		Assets    []AssetEntry            `yaml:"assets"`
		Options   map[string]OptionSchema `yaml:"options"`
	}
	if err := yaml.Unmarshal(metadataOnly, &meta); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	if meta.Name == "" {
		return nil, fmt.Errorf("plugin.yaml: name is required")
	}

	return &PluginDef{
		Name:           meta.Name,
		Requires:       meta.Requires,
		Functions:      meta.Functions,
		Assets:         meta.Assets,
		Options:        meta.Options,
		ContributesRaw: contributesRaw,
	}, nil
}

// splitContributesBlock splits plugin.yaml into the raw contributes template
// and the remaining metadata YAML (with contributes removed).
func splitContributesBlock(data []byte) (contributes string, metadata []byte) {
	lines := strings.Split(string(data), "\n")
	var metaLines []string
	var contribLines []string
	inContribs := false

	for i := range lines {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if !inContribs {
			if trimmed == "contributes:" {
				inContribs = true
				continue
			}
			metaLines = append(metaLines, line)
		} else {
			// Still in contributes block: indented, blank, or template directive
			if line == "" || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(trimmed, "{{") {
				contribLines = append(contribLines, line)
			} else {
				// Hit a new top-level key — back to metadata
				inContribs = false
				metaLines = append(metaLines, line)
			}
		}
	}

	// Dedent contributes block
	minIndent := -1
	for _, line := range contribLines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "{{") {
			continue
		}
		indent := 0
		for _, ch := range line {
			if ch == ' ' {
				indent++
			} else {
				break
			}
		}
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}

	dedented := make([]string, len(contribLines))
	for i, line := range contribLines {
		trimmed := strings.TrimSpace(line)
		// Don't dedent template directive lines or blank lines
		if trimmed == "" || strings.HasPrefix(trimmed, "{{") {
			dedented[i] = line
		} else if minIndent > 0 && len(line) >= minIndent {
			dedented[i] = line[minIndent:]
		} else {
			dedented[i] = line
		}
	}

	return strings.Join(dedented, "\n"), []byte(strings.Join(metaLines, "\n"))
}
