// Package config handles agent.yaml and fleet.yaml parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

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

// AgentConfig represents an agent.yaml file.
type AgentConfig struct {
	Name     string         `yaml:"name" schema:"Agent name" required:"true" examples:"my-agent"`
	Runtime  string         `yaml:"runtime" schema:"Runtime plugin name" required:"true" enum:"codex"`
	LogLevel string         `yaml:"log_level" schema:"Log verbosity level" default:"info" enum:"info,debug"`
	Gateway  *bool          `yaml:"gateway" schema:"Enable transparent gateway proxy" default:"true"`
	Features []FeatureEntry `yaml:"features" schema:"Feature plugins and their configuration"`
}

// GatewayEnabled returns whether the gateway should be included.
// Defaults to true if not specified.
func (c *AgentConfig) GatewayEnabled() bool {
	if c.Gateway == nil {
		return true
	}
	return *c.Gateway
}

// Load reads and parses an agent.yaml file from the given directory.
func Load(dir string) (*AgentConfig, error) {
	path := filepath.Join(dir, "agent.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading agent.yaml: %w", err)
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing agent.yaml: %w", err)
	}

	if cfg.Name == "" {
		return nil, fmt.Errorf("agent.yaml: name is required")
	}
	if cfg.Runtime == "" {
		return nil, fmt.Errorf("agent.yaml: runtime is required")
	}

	return &cfg, nil
}
