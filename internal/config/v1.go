package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// V1Config represents a v1 agent.yaml file.
type V1Config struct {
	Name          string         `yaml:"name"`
	LogLevel      string         `yaml:"log_level"`
	CoreVersion   string         `yaml:"core_version"`
	Runtime       RuntimeConfig  `yaml:"runtime"`
	Gateway       GatewayConfig  `yaml:"gateway"`
	Installations []Installation `yaml:"installations"`
}

// RuntimeConfig holds runtime container configuration.
type RuntimeConfig struct {
	Image       string   `yaml:"image"`
	ExtraBuilds []string `yaml:"extra_builds"`
	Entrypoint  []string `yaml:"entrypoint"`
	Volumes     []string `yaml:"volumes"`
}

// GatewayConfig holds gateway proxy configuration.
type GatewayConfig struct {
	Services []GatewayServiceEntry `yaml:"services"`
}

// GatewayServiceEntry represents an allowed upstream service.
type GatewayServiceEntry struct {
	URL         string            `yaml:"url"`
	Network     string            `yaml:"network"`
	Headers     map[string]string `yaml:"headers"`
	Middlewares []MiddlewareEntry `yaml:"middlewares"`
}

// MiddlewareEntry represents a gateway middleware configuration.
type MiddlewareEntry struct {
	Custom string `yaml:"custom"`
}

// Installation represents a plugin installation with options.
type Installation struct {
	Plugin  string         `yaml:"plugin"`
	Source  string         `yaml:"source"`
	Options map[string]any `yaml:"options"`
}

// LoadV1 loads and parses a v1 agent.yaml from the given directory.
func LoadV1(dir string) (*V1Config, error) {
	path := filepath.Join(dir, "agent.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent.yaml: %w", err)
	}

	var cfg V1Config
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
