// Package pluginloader reads plugin configuration and registers TS handlers with the gateway.
package pluginloader

// PluginConfig represents a single plugin's configuration as resolved at generate-time.
type PluginConfig struct {
	Name    string         `yaml:"name"`
	Dir     string         `yaml:"dir"`     // Absolute path to plugin directory
	Options map[string]any `yaml:"options"` // Resolved plugin options
	Gateway GatewayContrib `yaml:"gateway"`
}

// GatewayContrib describes what a plugin contributes to the gateway.
type GatewayContrib struct {
	Middlewares []MiddlewareEntry `yaml:"middlewares"`
	Routes      []RouteEntry      `yaml:"routes"`
}

// MiddlewareEntry declares a TS middleware handler scoped to domains.
type MiddlewareEntry struct {
	Script  string   `yaml:"script"`  // Relative path to .ts file
	Domains []string `yaml:"domains"` // Domain scope (empty = all)
}

// RouteEntry declares a TS route handler at a path.
type RouteEntry struct {
	Path    string `yaml:"path"`    // Route path (namespaced at load time)
	Handler string `yaml:"handler"` // Relative path to .ts file
}

// PluginsConfig is the top-level config for all plugins loaded by the gateway.
type PluginsConfig struct {
	Plugins []PluginConfig `yaml:"plugins"`
}
