package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/envvar"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

// pluginsYAMLConfig is the top-level structure for plugins.yaml written into the gateway build dir.
type pluginsYAMLConfig struct {
	Plugins []pluginsYAMLEntry `yaml:"plugins"`
}

// pluginsYAMLEntry describes one plugin for the gateway's pluginloader.
type pluginsYAMLEntry struct {
	Name    string            `yaml:"name"`
	Dir     string            `yaml:"dir"`
	Options map[string]any    `yaml:"options,omitempty"`
	Gateway pluginsYAMLGW     `yaml:"gateway"`
}

// pluginsYAMLGW holds the gateway contributions for a plugin in plugins.yaml.
type pluginsYAMLGW struct {
	Middlewares []pluginsYAMLMiddleware `yaml:"middlewares,omitempty"`
	Routes      []pluginsYAMLRoute     `yaml:"routes,omitempty"`
}

type pluginsYAMLMiddleware struct {
	Script  string   `yaml:"script"`
	Domains []string `yaml:"domains,omitempty"`
}

type pluginsYAMLRoute struct {
	Path    string `yaml:"path"`
	Handler string `yaml:"handler"`
}

// writeGatewayBuild creates the .build/gateway/ directory with the pre-built binary,
// plugins.yaml, config.yaml, plugin TS source files, and a simple Dockerfile.
func (g *Generator) writeGatewayBuild(buildDir string, cfg *config.Config, contribs *plugin.Contributions, resolved map[string]*resolvedPlugin) error {
	gatewayDir := filepath.Join(buildDir, "gateway")
	if err := os.MkdirAll(gatewayDir, 0755); err != nil {
		return fmt.Errorf("create gateway dir: %w", err)
	}

	// 1. Copy the pre-built gateway binary
	if err := g.copyGatewayBinary(gatewayDir); err != nil {
		return err
	}

	// 2. Copy plugin TS source directories into gateway/plugins/<name>/
	if err := g.copyPluginSources(gatewayDir, resolved); err != nil {
		return err
	}

	// 3. Generate plugins.yaml
	if err := g.writePluginsYAML(gatewayDir, cfg, contribs, resolved); err != nil {
		return err
	}

	// 4. Copy config.yaml from buildDir into gateway dir
	configData, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	if err != nil {
		return fmt.Errorf("read config.yaml for gateway build: %w", err)
	}
	if err := os.WriteFile(filepath.Join(gatewayDir, "config.yaml"), configData, 0644); err != nil {
		return fmt.Errorf("write gateway config.yaml: %w", err)
	}

	// 5. Write the Dockerfile
	return g.writeGatewayBuildFiles(gatewayDir)
}

// copyGatewayBinary copies the pre-built gateway binary into the build context.
// For --core mode: looks for the binary at coreDir/gateway/bin/gateway-linux-amd64.
// For embedded/release mode: extracts from gatewayFS.
// If no binary source is available (e.g. tests, or local dev before first build),
// writes a placeholder script that errors at container startup with a helpful message.
func (g *Generator) copyGatewayBinary(gatewayDir string) error {
	if g.coreDir != "" {
		binaryPath := filepath.Join(g.coreDir, "gateway", "bin", "gateway-linux-amd64")
		data, err := os.ReadFile(binaryPath)
		if err == nil {
			destPath := filepath.Join(gatewayDir, "gateway")
			if err := os.WriteFile(destPath, data, 0755); err != nil {
				return fmt.Errorf("write gateway binary: %w", err)
			}
			return nil
		}
		// Binary not found in core dir — fall through to placeholder
	}

	if g.gatewayFS != nil {
		// Release mode: binary should be in the tarball at bin/gateway-linux-amd64
		data, err := fs.ReadFile(g.gatewayFS, "bin/gateway-linux-amd64")
		if err == nil {
			destPath := filepath.Join(gatewayDir, "gateway")
			if err := os.WriteFile(destPath, data, 0755); err != nil {
				return fmt.Errorf("write gateway binary: %w", err)
			}
			return nil
		}
		// Binary not in release FS — fall through to placeholder
	}

	// No binary source — write a placeholder. Generation succeeds but container will
	// fail at startup with a clear error. This supports generate-only workflows and tests.
	placeholder := "#!/bin/sh\necho 'ERROR: gateway binary not included — rebuild with: GOOS=linux GOARCH=amd64 go build -o core/gateway/bin/gateway-linux-amd64 ./core/gateway/cmd/gateway/' >&2\nexit 1\n"
	destPath := filepath.Join(gatewayDir, "gateway")
	return os.WriteFile(destPath, []byte(placeholder), 0755)
}

// copyPluginSources copies TS source directories for each resolved plugin into
// the gateway build context at gateway/plugins/<name>/.
func (g *Generator) copyPluginSources(gatewayDir string, resolved map[string]*resolvedPlugin) error {
	for _, rp := range resolved {
		srcDir := g.findPluginSrcDir(rp.def)
		if srcDir == "" {
			continue // plugin has no src/ directory (e.g. home-override)
		}

		destDir := filepath.Join(gatewayDir, "plugins", rp.def.Name, "src")
		if err := copyDir(srcDir, destDir); err != nil {
			return fmt.Errorf("copy plugin %q sources: %w", rp.def.Name, err)
		}
	}

	// Ensure plugins/ directory exists even if no plugins have sources
	pluginsDir := filepath.Join(gatewayDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		return fmt.Errorf("create plugins dir: %w", err)
	}

	return nil
}

// findPluginSrcDir locates the source directory for a plugin's TS files.
func (g *Generator) findPluginSrcDir(def *plugin.PluginDef) string {
	if def.BaseDir != "" {
		// Local plugin — look for src/ directory
		srcDir := filepath.Join(def.BaseDir, "src")
		if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
			return srcDir
		}
		return ""
	}

	// Bundled plugin — check bundled FS for src/ directory
	if g.bundledFS != nil {
		srcPath := def.Name + "/src"
		if _, err := fs.Stat(g.bundledFS, srcPath); err == nil {
			// If we have coreDir, use the actual filesystem path
			if g.coreDir != "" {
				realPath := filepath.Join(g.coreDir, "plugins", def.Name, "src")
				if info, err := os.Stat(realPath); err == nil && info.IsDir() {
					return realPath
				}
			}
			return ""
		}
	}

	// Core directory mode — look for src/ in coreDir/plugins/<name>/
	if g.coreDir != "" {
		srcDir := filepath.Join(g.coreDir, "plugins", def.Name, "src")
		if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
			return srcDir
		}
	}

	return ""
}

// writePluginsYAML generates the plugins.yaml file that tells the gateway which TS plugins to load.
func (g *Generator) writePluginsYAML(gatewayDir string, cfg *config.Config, contribs *plugin.Contributions, resolved map[string]*resolvedPlugin) error {
	var entries []pluginsYAMLEntry

	for _, inst := range cfg.Installations {
		rp, ok := resolved[inst.Plugin]
		if !ok {
			continue
		}

		// Skip plugins with no gateway TS contributions
		if !hasGatewayTSContribs(rp) {
			continue
		}

		// Resolve options (expand env vars)
		resolvedOpts := make(map[string]any, len(inst.Options))
		for k, v := range inst.Options {
			if s, ok := v.(string); ok {
				resolvedOpts[k] = envvar.Expand(s)
			} else {
				resolvedOpts[k] = v
			}
		}

		entry := pluginsYAMLEntry{
			Name:    rp.def.Name,
			Dir:     "/etc/gateway/plugins/" + rp.def.Name,
			Options: resolvedOpts,
		}

		// Add top-level middlewares from the plugin
		for _, mw := range rp.rendered.Gateway.Middlewares {
			entry.Gateway.Middlewares = append(entry.Gateway.Middlewares, pluginsYAMLMiddleware{
				Script:  mw.Script,
				Domains: mw.Domains,
			})
		}

		// Add per-service middlewares (TS-based)
		for _, svc := range rp.rendered.Gateway.Services {
			for _, mw := range svc.Middlewares {
				if mw.Custom != "" {
					// Legacy Go middleware — skip (will be removed)
					continue
				}
			}
		}

		// Add routes
		for _, route := range rp.rendered.Gateway.Routes {
			entry.Gateway.Routes = append(entry.Gateway.Routes, pluginsYAMLRoute{
				Path:    route.Path,
				Handler: route.Handler,
			})
		}

		entries = append(entries, entry)
	}

	pluginsCfg := pluginsYAMLConfig{Plugins: entries}
	data, err := yaml.Marshal(pluginsCfg)
	if err != nil {
		return fmt.Errorf("marshal plugins.yaml: %w", err)
	}

	return os.WriteFile(filepath.Join(gatewayDir, "plugins.yaml"), data, 0644)
}

// hasGatewayTSContribs returns true if the plugin contributes TS middlewares or routes.
func hasGatewayTSContribs(rp *resolvedPlugin) bool {
	if len(rp.rendered.Gateway.Middlewares) > 0 {
		return true
	}
	if len(rp.rendered.Gateway.Routes) > 0 {
		return true
	}
	return false
}

// writeGatewayBuildFiles writes the gateway Dockerfile into the gateway build directory.
func (g *Generator) writeGatewayBuildFiles(gatewayDir string) error {
	if err := os.MkdirAll(gatewayDir, 0755); err != nil {
		return err
	}
	dockerfile, err := g.templates.LoadRaw("gateway.Dockerfile.tmpl")
	if err != nil {
		return fmt.Errorf("load gateway Dockerfile template: %w", err)
	}
	if err := os.WriteFile(filepath.Join(gatewayDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write gateway Dockerfile: %w", err)
	}
	return nil
}
