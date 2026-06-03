package resolve

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// envVarRefPattern matches a ${VAR} reference (whole string only).
var envVarRefPattern = regexp.MustCompile(`^\$\{([A-Z_][A-Z0-9_]*)\}$`)

// envVarInlinePattern matches ${VAR} references anywhere in a string.
var envVarInlinePattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// ExtractEnvVar extracts the environment variable name from a "${VAR}" reference.
// Returns the var name and true if the input is a valid reference, or ("", false) otherwise.
func ExtractEnvVar(ref string) (string, bool) {
	m := envVarRefPattern.FindStringSubmatch(ref)
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

// FindEnvVarRefs returns all env var names referenced as ${VAR} in the string.
func FindEnvVarRefs(s string) []string {
	matches := envVarInlinePattern.FindAllStringSubmatch(s, -1)
	var refs []string
	for _, m := range matches {
		refs = append(refs, m[1])
	}
	return refs
}

// ReplaceEnvVarRef replaces ${envVar} with replacement in s.
func ReplaceEnvVarRef(s, envVar, replacement string) string {
	return strings.ReplaceAll(s, "${"+envVar+"}", replacement)
}

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
	Type        string   // "telegram-url", "auth-header", or "oauth"
	Domains     []string // domains this rewriter applies to
	EnvVar      string   // environment variable holding the secret
	Header      string   // header name to inject (auth-header type only)
	ValueFormat string   // header value format, e.g. "token ${value}" (auth-header type only)

	// OAuth-specific fields (type "oauth" only)
	TokenFile string // path to stored OAuth token JSON file
}

// HTTPService describes a plain HTTP service the gateway should proxy and inject headers into.
type HTTPService struct {
	Host string // hostname (Docker DNS or external)
	Port string // port number (default "80")
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
	AgentEnv        []string         // environment variables for agent container (dummy values, not secrets)
	ChannelConfig    map[string]any   // plugin-specific config passed to channel-manager-config.json
	Rewriters       []RewriterConfig // gateway rewriters to instantiate for this feature
	CommandPluginDir string          // path to TypeScript command plugin source (copied into channel-manager)
	ExternalNetworks []string        // external Docker networks the gateway should join
	HTTPServices    []HTTPService    // plain HTTP services to proxy with header injection
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
