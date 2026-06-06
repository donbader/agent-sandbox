// Package config handles agent.yaml and fleet.yaml parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultRuntimeEngine is the default container runtime when not specified.
const DefaultRuntimeEngine = "docker"

// ValidationError collects multiple config validation failures.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0]
	}
	return fmt.Sprintf("%d validation errors:\n- %s", len(e.Errors), strings.Join(e.Errors, "\n- "))
}

// Add appends an error message to the collection.
func (e *ValidationError) Add(msg string) {
	e.Errors = append(e.Errors, msg)
}

// HasErrors returns true if any validation errors were collected.
func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// RuntimeEngineBinary returns the container runtime CLI binary name.
func (c *Config) RuntimeEngineBinary() string {
	switch c.RuntimeEngine {
	case "podman":
		return "podman"
	default:
		return "docker"
	}
}

// Config represents an agent.yaml file.
type Config struct {
	Name          string         `yaml:"name" json:"name" jsonschema:"required,title=name,description=Agent instance name"`
	LogLevel      string         `yaml:"log_level" json:"log_level,omitempty" jsonschema:"title=log_level,description=Logging verbosity,enum=info,enum=debug"`
	CoreVersion   string         `yaml:"core_version" json:"core_version,omitempty" jsonschema:"title=core_version,description=Core version to use for generation"`
	RuntimeEngine string         `yaml:"runtime_engine" json:"runtime_engine,omitempty" jsonschema:"title=runtime_engine,description=Container runtime engine (docker or podman),enum=docker,enum=podman,default=docker"`
	Runtime       RuntimeConfig  `yaml:"runtime" json:"runtime" jsonschema:"required,title=runtime,description=Agent container configuration"`
	Gateway       GatewayConfig  `yaml:"gateway" json:"gateway,omitempty" jsonschema:"title=gateway,description=Transparent egress proxy configuration"`
	Installations []Installation `yaml:"installations" json:"installations,omitempty" jsonschema:"title=installations,description=Plugins to install"`
}

// RuntimeConfig holds runtime container configuration.
type RuntimeConfig struct {
	Image       string   `yaml:"image" json:"image" jsonschema:"required,title=image,description=Base image (@builtin/codex or any Docker image)"`
	ExtraBuilds []string `yaml:"extra_builds" json:"extra_builds,omitempty" jsonschema:"title=extra_builds,description=Additional Dockerfile instructions layered after the base"`
	Entrypoint  []string `yaml:"entrypoint" json:"entrypoint,omitempty" jsonschema:"title=entrypoint,description=Container CMD override"`
	Volumes     []string `yaml:"volumes" json:"volumes,omitempty" jsonschema:"title=volumes,description=Named or bind mount volumes"`
}

// GatewayConfig holds gateway proxy configuration.
type GatewayConfig struct {
	Services []GatewayServiceEntry `yaml:"services" json:"services,omitempty" jsonschema:"title=services,description=External services proxied through the gateway"`
}

// GatewayServiceEntry represents an allowed upstream service.
type GatewayServiceEntry struct {
	URL         string            `yaml:"url" json:"url" jsonschema:"required,title=url,description=Service endpoint: HTTPS URL (https://api.example.com) or internal host:port (sidecar:8080)"`
	Network     string            `yaml:"network" json:"network,omitempty" jsonschema:"title=network,description=Compose network to attach (optional, defaults to sandbox network)"`
	Headers     map[string]string `yaml:"headers" json:"headers,omitempty" jsonschema:"title=headers,description=Headers injected by gateway on every proxied request"`
	Middlewares []MiddlewareEntry `yaml:"middlewares" json:"middlewares,omitempty" jsonschema:"title=middlewares,description=Custom middleware chain"`
}

// MiddlewareEntry represents a gateway middleware configuration.
type MiddlewareEntry struct {
	Custom string `yaml:"custom" json:"custom" jsonschema:"required,title=custom,description=Relative path to custom middleware .go file"`
}

// Installation represents a plugin installation with options.
type Installation struct {
	Plugin  string         `yaml:"plugin" json:"plugin" jsonschema:"required,title=plugin,description=Plugin reference. Use @builtin/name for bundled plugins or ./path for local plugins. Bare names are not allowed."`
	Source  string         `yaml:"source" json:"source,omitempty" jsonschema:"title=source,description=Plugin source (reserved for future remote resolution)"`
	Options map[string]any `yaml:"options" json:"options,omitempty" jsonschema:"title=options,description=Plugin-specific configuration options"`
}

// Load loads and parses an agent.yaml from the given directory.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "agent.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse agent.yaml: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks all config fields and returns a ValidationError collecting
// all problems found (not just the first one).
func (c *Config) Validate() error {
	ve := &ValidationError{}

	if c.Name == "" {
		ve.Add("name is required")
	}
	if c.Runtime.Image == "" {
		ve.Add("runtime.image is required")
	}

	// Validate runtime_engine if specified
	if c.RuntimeEngine != "" && c.RuntimeEngine != "docker" && c.RuntimeEngine != "podman" {
		ve.Add(fmt.Sprintf("runtime_engine must be 'docker' or 'podman', got %q", c.RuntimeEngine))
	}

	// Validate service URLs
	for i, svc := range c.Gateway.Services {
		if svc.URL == "" {
			ve.Add(fmt.Sprintf("gateway.services[%d]: url is required", i))
			continue
		}
		if strings.HasPrefix(svc.URL, "docker://") {
			ve.Add(fmt.Sprintf("gateway.services[%d]: docker:// URLs are deprecated, use plain host:port (e.g. %s)", i, strings.TrimPrefix(svc.URL, "docker://")))
		}
	}

	if ve.HasErrors() {
		return ve
	}
	return nil
}

// FeatureEntry represents a single feature plugin entry in the features array.
type FeatureEntry struct {
	Plugin string         `yaml:"plugin" schema:"Plugin type name" required:"true"`
	Name   string         `yaml:"name" schema:"Optional instance name for logging (defaults to features[i])"`
	Config map[string]any `yaml:"-"` // remaining fields after plugin/name extraction
}

// UnmarshalYAML implements custom unmarshaling to separate plugin/name from config fields.
func (f *FeatureEntry) UnmarshalYAML(value *yaml.Node) error {
	// First decode into a map to get all fields
	var raw map[string]any
	if err := value.Decode(&raw); err != nil {
		return err
	}

	// Extract plugin (required)
	plugin, ok := raw["plugin"]
	if !ok {
		return fmt.Errorf("feature entry missing required 'plugin' field")
	}
	pluginStr, ok := plugin.(string)
	if !ok {
		return fmt.Errorf("feature entry 'plugin' must be a string")
	}
	f.Plugin = pluginStr
	delete(raw, "plugin")

	// Extract name (optional)
	if name, ok := raw["name"]; ok {
		nameStr, ok := name.(string)
		if !ok {
			return fmt.Errorf("feature entry 'name' must be a string")
		}
		f.Name = nameStr
		delete(raw, "name")
	}

	// Remaining fields are the plugin config
	f.Config = raw
	return nil
}

// FleetConfig represents a fleet.yaml file for multi-agent deployments.
type FleetConfig struct {
	Agents []string    `yaml:"agents"`
	Shared SharedBlock `yaml:"shared"`
}

// SharedBlock holds configuration shared across all agents in a fleet.
type SharedBlock struct {
	Installations []FeatureEntry `yaml:"installations"`
	Gateway       GatewayConfig  `yaml:"gateway"`
}

// LoadFleet reads and parses a fleet.yaml file from the given directory.
func LoadFleet(dir string) (*FleetConfig, error) {
	path := filepath.Join(dir, "fleet.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading fleet.yaml: %w", err)
	}

	var cfg FleetConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing fleet.yaml: %w", err)
	}

	if len(cfg.Agents) == 0 {
		return nil, fmt.Errorf("fleet.yaml: agents list is required")
	}

	return &cfg, nil
}

// FleetAgent pairs an agent config with its source directory.
type FleetAgent struct {
	Config *Config
	Dir    string // absolute path to the agent's directory
}

// LoadFleetAgents loads fleet.yaml and all referenced agent configs,
// merging shared installations and gateway services into each agent.
// Returns configs ready for generation.
func LoadFleetAgents(dir string) (*FleetConfig, []FleetAgent, error) {
	fleet, err := LoadFleet(dir)
	if err != nil {
		return nil, nil, err
	}

	var agents []FleetAgent
	for _, agentName := range fleet.Agents {
		agentDir := filepath.Join(dir, agentName)
		cfg, err := Load(agentDir)
		if err != nil {
			return nil, nil, fmt.Errorf("loading agent %q: %w", agentName, err)
		}

		// Merge shared installations into agent config (per-agent overrides shared)
		cfg.Installations = MergeInstallations(fleet.Shared.Installations, cfg.Installations)

		// Merge shared gateway services (shared prepended, per-agent appended)
		cfg.Gateway.Services = MergeGatewayServices(fleet.Shared.Gateway.Services, cfg.Gateway.Services)

		agents = append(agents, FleetAgent{Config: cfg, Dir: agentDir})
	}

	return fleet, agents, nil
}

// MergeInstallations merges shared features with per-agent installations.
// Per-agent wins when the same plugin name appears in both.
func MergeInstallations(shared []FeatureEntry, perAgent []Installation) []Installation {
	if len(shared) == 0 {
		return perAgent
	}

	// Build set of per-agent plugin names for override detection
	agentPlugins := make(map[string]bool, len(perAgent))
	for _, inst := range perAgent {
		agentPlugins[inst.Plugin] = true
	}

	// Start with shared features that aren't overridden
	var merged []Installation
	for _, feat := range shared {
		if agentPlugins[feat.Plugin] {
			continue // per-agent overrides
		}
		merged = append(merged, Installation{
			Plugin:  feat.Plugin,
			Options: feat.Config,
		})
	}

	// Append all per-agent installations
	merged = append(merged, perAgent...)
	return merged
}

// MergeGatewayServices merges shared gateway services with per-agent services.
// Shared services are prepended; per-agent services with the same URL override shared.
func MergeGatewayServices(shared, perAgent []GatewayServiceEntry) []GatewayServiceEntry {
	if len(shared) == 0 {
		return perAgent
	}
	if len(perAgent) == 0 {
		return shared
	}

	// Build set of per-agent URLs for dedup
	agentURLs := make(map[string]bool, len(perAgent))
	for _, svc := range perAgent {
		agentURLs[svc.URL] = true
	}

	// Shared services that aren't overridden by per-agent
	var merged []GatewayServiceEntry
	for _, svc := range shared {
		if agentURLs[svc.URL] {
			continue
		}
		merged = append(merged, svc)
	}

	// Append all per-agent services
	merged = append(merged, perAgent...)
	return merged
}


