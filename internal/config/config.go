// Package config handles agent.yaml and fleet.yaml parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/runtime"
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
	CoreVersion   string         `yaml:"core_version" json:"core_version" jsonschema:"required,title=core_version,description=Core version to use for generation (semver tag or 'latest' for embedded)"`
	RuntimeEngine string         `yaml:"runtime_engine" json:"runtime_engine,omitempty" jsonschema:"title=runtime_engine,description=Container runtime engine (docker or podman),enum=docker,enum=podman,default=docker"`
	Runtime       RuntimeConfig  `yaml:"runtime" json:"runtime" jsonschema:"required,title=runtime,description=Agent container configuration"`
	Gateway       GatewayConfig  `yaml:"gateway" json:"gateway,omitempty" jsonschema:"title=gateway,description=Transparent egress proxy configuration"`
	Installations []Installation `yaml:"installations" json:"installations,omitempty" jsonschema:"title=installations,description=Plugins to install"`
}

// StageArtifact describes a file to COPY from a build stage into the final image.
type StageArtifact struct {
	From string `yaml:"from" json:"from" jsonschema:"required,description=Source path in build stage"`
	To   string `yaml:"to" json:"to" jsonschema:"required,description=Destination path in final image"`
}

// BuildStageConfig declares an isolated Docker build stage in agent.yaml.
type BuildStageConfig struct {
	Name      string          `yaml:"name" json:"name" jsonschema:"required,description=Stage name (Dockerfile stage becomes build-{name})"`
	Base      string          `yaml:"base" json:"base,omitempty" jsonschema:"description=Base image for this stage (defaults to runtime image)"`
	Steps     []string        `yaml:"steps" json:"steps,omitempty" jsonschema:"description=Dockerfile instructions for this stage"`
	Artifacts []StageArtifact `yaml:"artifacts" json:"artifacts,omitempty" jsonschema:"description=Files to COPY from this stage into the final image"`
}

// RuntimeConfig holds runtime container configuration.
type RuntimeConfig struct {
	Image             string            `yaml:"image" json:"image" jsonschema:"required,title=image,description=Base image (@builtin/codex or any Docker image)"`
	CWD               string            `yaml:"cwd" json:"cwd,omitempty" jsonschema:"title=cwd,description=Working directory for agent sessions (default: /home/agent/workspace)"`
	ExtraBuilds       []string          `yaml:"extra_builds" json:"extra_builds,omitempty" jsonschema:"title=extra_builds,description=Additional Dockerfile instructions layered after the base"`
	Entrypoint        []string          `yaml:"entrypoint" json:"entrypoint,omitempty" jsonschema:"title=entrypoint,description=Container CMD override"`
	NamespacedVolumes []string          `yaml:"namespaced_volumes" json:"namespaced_volumes,omitempty" jsonschema:"title=namespaced_volumes,description=Named volumes auto-prefixed with agent name for fleet isolation"`
	RawVolumes        []string          `yaml:"raw_volumes" json:"raw_volumes,omitempty" jsonschema:"title=raw_volumes,description=Volumes used as-is (bind mounts or intentionally shared named volumes)"`
	Environment       map[string]string  `yaml:"environment" json:"environment,omitempty" jsonschema:"title=environment,description=Environment variables passed to the agent container"`
	BuildStages       []BuildStageConfig `yaml:"build_stages" json:"build_stages,omitempty" jsonschema:"title=build_stages,description=Isolated Docker build stages for caching heavy build steps"`
}

// GatewayConfig holds gateway proxy configuration.
type GatewayConfig struct {
	Egress   []EgressRule          `yaml:"egress" json:"egress,omitempty" jsonschema:"title=egress,description=Ordered egress access control rules. First match wins. No match = deny."`
	Services []GatewayServiceEntry `yaml:"services" json:"services,omitempty" jsonschema:"title=services,description=DEPRECATED: use gateway.egress instead. External services proxied through the gateway."`
}

// GatewayServiceEntry represents an allowed upstream service.
type GatewayServiceEntry struct {
	URL     string            `yaml:"url" json:"url" jsonschema:"required,title=url,description=Service endpoint: HTTPS URL (https://api.example.com) or internal host:port (sidecar:8080)"`
	Network string            `yaml:"network" json:"network,omitempty" jsonschema:"title=network,description=Compose network to attach (optional, defaults to sandbox network)"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty" jsonschema:"title=headers,description=Headers injected by gateway on every proxied request"`
}

// Installation represents a plugin installation with options.
type Installation struct {
	Plugin  string         `yaml:"plugin" json:"plugin" jsonschema:"required,title=plugin,description=Plugin reference. Use @builtin/name for bundled plugins or ./path for local plugins. Bare names are not allowed."`
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

	// Apply defaults
	if cfg.Runtime.CWD == "" {
		cfg.Runtime.CWD = "/home/agent/workspace"
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
	if c.CoreVersion == "" {
		ve.Add("core_version is required (use 'latest' for the embedded version)")
	}
	if c.Runtime.Image == "" {
		ve.Add("runtime.image is required")
	}

	// Validate runtime_engine if specified
	if c.RuntimeEngine != "" && !runtime.IsValid(c.RuntimeEngine) {
		ve.Add(fmt.Sprintf("runtime_engine must be one of %v, got %q", runtime.ValidNames(), c.RuntimeEngine))
	}

	// Validate egress rules
	if len(c.Gateway.Egress) > 0 {
		for _, msg := range ValidateEgressRules(c.Gateway.Egress) {
			ve.Add(msg)
		}
	}

	// Legacy services format is no longer supported — must migrate to egress
	if len(c.Gateway.Services) > 0 {
		ve.Add("gateway.services is removed — run 'agent-sandbox generate --migrate' to convert to gateway.egress format")
	}

	if ve.HasErrors() {
		return ve
	}
	return nil
}

// FleetConfig represents a fleet.yaml file for multi-agent deployments.
type FleetConfig struct {
	Agents []string    `yaml:"agents" json:"agents" jsonschema:"required,title=agents,description=List of agent subdirectory names"`
	Shared SharedBlock `yaml:"shared" json:"shared,omitempty" jsonschema:"title=shared,description=Configuration shared across all agents"`
}

// SharedBlock holds configuration shared across all agents in a fleet.
type SharedBlock struct {
	Installations []Installation `yaml:"installations" json:"installations,omitempty" jsonschema:"title=installations,description=Plugins shared across all agents"`
	Gateway       GatewayConfig  `yaml:"gateway" json:"gateway,omitempty" jsonschema:"title=gateway,description=Gateway services shared across all agents"`
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

// MergeInstallations merges shared installations with per-agent installations.
// Per-agent wins when the same plugin name appears in both.
func MergeInstallations(shared []Installation, perAgent []Installation) []Installation {
	if len(shared) == 0 {
		return perAgent
	}

	// Build set of per-agent plugin names for override detection
	agentPlugins := make(map[string]bool, len(perAgent))
	for _, inst := range perAgent {
		agentPlugins[inst.Plugin] = true
	}

	// Start with shared installations that aren't overridden
	var merged []Installation
	for _, inst := range shared {
		if agentPlugins[inst.Plugin] {
			continue // per-agent overrides
		}
		merged = append(merged, inst)
	}

	// Append all per-agent installations
	merged = append(merged, perAgent...)
	return merged
}

// MergeEgressRules merges shared egress rules with per-agent egress rules.
// Per-agent rules fully override shared rules when present (not additive).
// Rationale: egress rules are ordered and order matters (first-match-wins),
// so merging additively could produce surprising behavior.
func MergeEgressRules(shared, perAgent []EgressRule) []EgressRule {
	if len(perAgent) > 0 {
		return perAgent
	}
	return shared
}


