// Package sdk defines the plugin interfaces for agent-sandbox.
//
// Runtime plugins are data-driven (runtime.yaml) — no Go interface needed.
// Feature plugins use Go interfaces for gateway handlers (compiled during Docker build).
package sdk

import (
	"embed"
	"net/http"
)

// FeaturePlugin provides additive capabilities (credentials, channels, Docker, home).
// Multiple FeaturePlugins can be active per agent (listed under features:).
type FeaturePlugin interface {
	Name() string
	ConfigSchema() ConfigSchema
	Contribute(ctx ContributeContext) (*FeatureContributions, error)
}

// ContributeContext provides information about the agent being built.
type ContributeContext struct {
	AgentName string
	Config    map[string]any
	FleetDir  string // root directory of the fleet/agent config
}

// ConfigSchema describes the configuration a plugin accepts.
type ConfigSchema struct {
	// JSON Schema for validation (optional, nil = no validation)
	JSONSchema []byte
}

// FeatureContributions is what a FeaturePlugin provides.
type FeatureContributions struct {
	Image      *ImageContribution
	Gateway    *GatewayContribution
	Bridge     *BridgeContribution
	Compose    *ComposeContribution
	Entrypoint *EntrypointContribution
}

// ImageContribution adds files and commands to the Dockerfile.
type ImageContribution struct {
	Files    []File   // COPY into image
	Commands []string // RUN commands
}

// File represents a file to COPY into the image.
type File struct {
	Source embed.FS // embedded source
	Path   string   // source path within embed.FS
	Dest   string   // destination path in image
}

// GatewayContribution declares hosts this plugin handles and provides a handler factory.
type GatewayContribution struct {
	Hosts      []string                                          // hosts this plugin handles
	NewHandler func(cfg map[string]any) (RequestHandler, error) // factory for runtime handler
}

// RequestHandler processes intercepted HTTP requests (e.g., inject credentials).
type RequestHandler interface {
	HandleRequest(req *http.Request) error
}

// BridgeContribution provides TypeScript source for a channel plugin.
type BridgeContribution struct {
	Name   string   // channel plugin name
	Source embed.FS // TypeScript source directory
	Path   string   // path within embed.FS
}

// ComposeContribution adds services, volumes, or environment to docker-compose.yml.
type ComposeContribution struct {
	Services map[string]any // additional compose services
	Volumes  []string       // named volumes to declare
	EnvVars  []EnvVar       // environment variables for the agent service
}

// EnvVar is an environment variable with conflict strategy.
type EnvVar struct {
	Name     string
	Value    string
	Strategy EnvVarStrategy
}

// EnvVarStrategy determines how conflicts are handled when multiple plugins set the same var.
type EnvVarStrategy int

const (
	Override         EnvVarStrategy = iota // last plugin wins
	ErrorIfConflict                        // same var by two plugins = error
	Append                                 // concatenate with separator
)

// EntrypointContribution adds hooks to the container entrypoint.
type EntrypointContribution struct {
	Hooks []Hook
}

// Hook is a script that runs during container startup.
type Hook struct {
	Name     string // hook identifier
	Script   string // script content (or path to embedded script)
	Priority int    // execution order (lower = earlier)
}
