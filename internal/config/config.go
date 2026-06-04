// Package config handles agent.yaml and fleet.yaml parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents an agent.yaml file.
type Config struct {
	Name          string         `yaml:"name" json:"name" jsonschema:"required,title=name,description=Agent instance name"`
	LogLevel      string         `yaml:"log_level" json:"log_level,omitempty" jsonschema:"title=log_level,description=Logging verbosity,enum=info,enum=debug"`
	CoreVersion   string         `yaml:"core_version" json:"core_version,omitempty" jsonschema:"title=core_version,description=Core version to use for generation"`
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
	URL         string            `yaml:"url" json:"url" jsonschema:"required,title=url,description=HTTPS endpoint or docker://<service>:<port> for sidecars"`
	Network     string            `yaml:"network" json:"network,omitempty" jsonschema:"title=network,description=Docker network to attach (required for docker:// URLs)"`
	Headers     map[string]string `yaml:"headers" json:"headers,omitempty" jsonschema:"title=headers,description=Headers injected by gateway on every proxied request"`
	Middlewares []MiddlewareEntry `yaml:"middlewares" json:"middlewares,omitempty" jsonschema:"title=middlewares,description=Custom middleware chain"`
}

// MiddlewareEntry represents a gateway middleware configuration.
type MiddlewareEntry struct {
	Custom string `yaml:"custom" json:"custom" jsonschema:"required,title=custom,description=Relative path to custom middleware .go file"`
}

// Installation represents a plugin installation with options.
type Installation struct {
	Plugin  string         `yaml:"plugin" json:"plugin" jsonschema:"required,title=plugin,description=Plugin name (bundled or local)"`
	Source  string         `yaml:"source" json:"source,omitempty" jsonschema:"title=source,description=Plugin source (local path or remote git URL)"`
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

	if cfg.Name == "" {
		return nil, fmt.Errorf("agent.yaml: name is required")
	}
	if cfg.Runtime.Image == "" {
		return nil, fmt.Errorf("agent.yaml: runtime.image is required")
	}

	for i, svc := range cfg.Gateway.Services {
		if strings.HasPrefix(svc.URL, "docker://") && svc.Network == "" {
			return nil, fmt.Errorf("agent.yaml: gateway.services[%d]: network is required for docker:// URLs", i)
		}
	}

	return &cfg, nil
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

// SharedBlock holds features shared across all agents.
type SharedBlock struct {
	Features []FeatureEntry `yaml:"features"`
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


