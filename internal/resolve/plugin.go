package resolve

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// FeaturePlugin defines the interface that feature plugins implement.
// The generic Register function wraps typed plugins into this interface.
type FeaturePlugin interface {
	// Name returns the plugin's identifier (matches feature.yaml name field).
	Name() string

	// ConfigType returns the zero-value of the plugin's config struct (for schema generation).
	ConfigType() any

	// Resolve takes user config from agent.yaml and returns what this plugin
	// contributes to the build (Dockerfile commands, entrypoint hooks, volumes, etc).
	Resolve(projectDir string, rawConfig map[string]any) (*FeatureContributions, error)
}

// RewriterConfig describes a gateway rewriter to instantiate for a set of domains.
type RewriterConfig struct {
	Type        string   // "telegram-url" or "auth-header"
	Domains     []string // domains this rewriter applies to
	EnvVar      string   // environment variable holding the secret
	Header      string   // header name to inject (auth-header type only)
	ValueFormat string   // header value format, e.g. "token ${value}" (auth-header type only)
}

// FeatureContributions holds what a feature adds to the build.
type FeatureContributions struct {
	Name            string           // plugin name (for diagnostics and logging)
	Commands        []string         // RUN commands for Dockerfile
	EntrypointHooks []string         // scripts to run on container start (source paths)
	Volumes         []string         // named volumes (e.g., "name:/path")
	HomeOverride    string           // directory to copy into home on start
	MITMDomains     []string         // domains the gateway should MITM (terminate TLS)
	ChannelName   string           // channel type (e.g., "telegram")
	EnvVars         []string         // environment variables (added to .env.example and compose)
	AgentEnv        []string         // environment variables for agent container (dummy values, not secrets)
	ChannelConfig    map[string]any   // plugin-specific config passed to channel-manager-config.json
	Rewriters       []RewriterConfig // gateway rewriters to instantiate for this feature
}

// registry holds registered feature plugins.
var registry = map[string]FeaturePlugin{}

// Register registers a typed feature plugin using generics.
// The framework handles unmarshaling rawConfig into the typed Config struct.
func Register[C any](name string, fn func(projectDir string, cfg C) (*FeatureContributions, error)) {
	registry[name] = &typedPlugin[C]{name: name, resolveFn: fn}
}

// RegisteredPlugins returns all registered plugins (for schema generation).
func RegisteredPlugins() map[string]FeaturePlugin {
	return registry
}

// typedPlugin wraps a generic resolve function into the FeaturePlugin interface.
type typedPlugin[C any] struct {
	name      string
	resolveFn func(string, C) (*FeatureContributions, error)
}

func (p *typedPlugin[C]) Name() string { return p.name }

func (p *typedPlugin[C]) ConfigType() any {
	var zero C
	return zero
}

func (p *typedPlugin[C]) Resolve(projectDir string, raw map[string]any) (*FeatureContributions, error) {
	var cfg C
	// yaml round-trip: map[string]any → yaml bytes → typed struct
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshaling config for %s: %w", p.name, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config for %s: %w", p.name, err)
	}
	return p.resolveFn(projectDir, cfg)
}
