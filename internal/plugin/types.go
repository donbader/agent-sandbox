package plugin

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PluginDef represents a parsed plugin.yaml file.
type PluginDef struct {
	Name        string                  `yaml:"name"`
	Requires    []string                `yaml:"requires"`
	Assets      []string                `yaml:"assets"`
	Options     map[string]OptionSchema `yaml:"options"`
	Contributes Contributions           `yaml:"contributes"`
	BaseDir     string                  `yaml:"-"` // directory where plugin.yaml lives (for resolving relative paths)
	AssetPaths  map[string]string       `yaml:"-"` // resolved asset paths (set by generator after extraction)
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
func ParsePluginYAML(data []byte) (*PluginDef, error) {
	var p PluginDef
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("plugin.yaml: name is required")
	}
	return &p, nil
}
