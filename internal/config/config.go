// Package config handles agent.yaml and fleet.yaml parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AgentConfig represents an agent.yaml file.
type AgentConfig struct {
	Name     string                    `yaml:"name" schema:"Agent name" required:"true" examples:"my-agent"`
	Runtime  string                    `yaml:"runtime" schema:"Runtime plugin name" required:"true" enum:"codex"`
	Gateway  *bool                     `yaml:"gateway" schema:"Enable transparent gateway proxy" default:"true"`
	Features map[string]map[string]any `yaml:"features" schema:"Feature plugins and their configuration"`
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
