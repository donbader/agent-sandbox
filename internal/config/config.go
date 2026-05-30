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
	Name     string                    `yaml:"name"`
	Runtime  any                       `yaml:"runtime"` // string or inline map
	Features map[string]map[string]any `yaml:"features"`
}

// RuntimeName returns the runtime name if it's a string reference.
// Returns empty string if it's an inline definition.
func (c *AgentConfig) RuntimeName() string {
	if s, ok := c.Runtime.(string); ok {
		return s
	}
	return ""
}

// RuntimeInline returns the inline runtime definition if present.
// Returns nil if runtime is a string reference.
func (c *AgentConfig) RuntimeInline() map[string]any {
	if m, ok := c.Runtime.(map[string]any); ok {
		return m
	}
	return nil
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
	if cfg.Runtime == nil {
		return nil, fmt.Errorf("agent.yaml: runtime is required")
	}

	return &cfg, nil
}
