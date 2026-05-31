package resolve

// FeaturePlugin defines the interface that feature plugins implement.
// Each plugin owns its contribution extraction logic.
type FeaturePlugin interface {
	// Name returns the plugin's identifier (matches feature.yaml name field).
	Name() string

	// Resolve takes user config from agent.yaml and returns what this plugin
	// contributes to the build (Dockerfile commands, entrypoint hooks, volumes, etc).
	Resolve(projectDir string, userConfig map[string]any) (*FeatureContributions, error)
}

// FeatureContributions holds what a feature adds to the build.
type FeatureContributions struct {
	Commands        []string // RUN commands for Dockerfile
	EntrypointHooks []string // scripts to run on container start (source paths)
	Volumes         []string // named volumes (e.g., "name:/path")
	HomeOverride    string   // directory to copy into home on start
	MITMDomains     []string // domains the gateway should MITM (terminate TLS)
	BridgeChannel   string   // bridge channel type (e.g., "telegram")
	EnvVars         []string // environment variables (added to .env.example and compose)
}

// registry holds registered feature plugins.
var registry = map[string]FeaturePlugin{}

// RegisterFeature registers a feature plugin. Called by plugin init() functions.
func RegisterFeature(p FeaturePlugin) {
	registry[p.Name()] = p
}
