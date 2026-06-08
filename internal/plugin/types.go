package plugin

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// PluginDef represents a parsed plugin.yaml file.
type PluginDef struct {
	Name           string                  `yaml:"name"`
	Requires       []string                `yaml:"requires"`
	Assets         []string                `yaml:"assets"`
	Options        map[string]OptionSchema `yaml:"options"`
	Contributes    Contributions           `yaml:"-"`          // populated after template rendering
	ContributesRaw string                  `yaml:"-"`          // raw YAML template for contributes block
	BaseDir        string                  `yaml:"-"`          // directory where plugin.yaml lives (for resolving relative paths)
	AssetPaths     map[string]string       `yaml:"-"`          // resolved asset paths (set by generator after extraction)
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

type RuntimeContrib struct {
	ExtraBuilds   []string `yaml:"extra_builds"`
	PreEntrypoint []string `yaml:"pre_entrypoint"`
	Ports         []string `yaml:"ports"`
	Volumes       []string `yaml:"volumes"`
}

type GatewayContrib struct {
	Services []GatewayService `yaml:"services"`
	Volumes  []string         `yaml:"volumes"`
	Routes   []RouteEntry     `yaml:"routes"`
}

// RouteEntry declares an HTTP route handler contributed by a plugin.
// The path is relative to the plugin's namespace (/plugins/{plugin-name}/...).
type RouteEntry struct {
	Path    string `yaml:"path"`    // relative path (e.g. "/callback")
	Handler string `yaml:"handler"` // path to handler .go file
}

type GatewayService struct {
	URL         string            `yaml:"url"`
	Network     string            `yaml:"network"`
	Headers     map[string]string `yaml:"headers"`
	Middlewares []MiddlewareRef   `yaml:"middlewares"`
}

type MiddlewareRef struct {
	Custom string `yaml:"custom"`
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
		Assets      []string                `yaml:"assets"`
		Options     map[string]OptionSchema `yaml:"options"`
		Contributes Contributions           `yaml:"contributes"`
	}
	if err := yaml.Unmarshal(data, &full); err == nil && full.Name != "" {
		// Full parse succeeded — re-marshal contributes from the typed struct.
		// This properly unescapes YAML string values (e.g. \" → ") so Go templates work.
		var contributesRaw string
		out, err := yaml.Marshal(&full.Contributes)
		if err == nil {
			contributesRaw = string(out)
		}
		return &PluginDef{
			Name:           full.Name,
			Requires:       full.Requires,
			Assets:         full.Assets,
			Options:        full.Options,
			ContributesRaw: contributesRaw,
		}, nil
	}

	// Full YAML parse failed (structural templates like {{- range }}).
	// Extract contributes as raw text, parse metadata separately.
	contributesRaw, metadataOnly := splitContributesBlock(data)

	var meta struct {
		Name     string                  `yaml:"name"`
		Requires []string                `yaml:"requires"`
		Assets   []string                `yaml:"assets"`
		Options  map[string]OptionSchema `yaml:"options"`
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

	for i := 0; i < len(lines); i++ {
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
		if minIndent > 0 && len(line) >= minIndent {
			dedented[i] = line[minIndent:]
		} else {
			dedented[i] = line
		}
	}

	return strings.Join(dedented, "\n"), []byte(strings.Join(metaLines, "\n"))
}
