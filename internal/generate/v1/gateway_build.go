package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
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

	// 1. Copy the pre-built gateway binary (includes custom Go middlewares if any)
	if err := g.copyGatewayBinary(gatewayDir, buildDir, resolved); err != nil {
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
// For --core mode: attempts to build from source if go is available, falling back
// to a pre-built binary at coreDir/gateway/bin/gateway-linux-<arch>.
// For embedded/release mode: extracts from gatewayFS.
// If no binary source is available, writes a placeholder script that errors at startup.
func (g *Generator) copyGatewayBinary(gatewayDir string, buildDir string, resolved map[string]*resolvedPlugin) error {
	if g.coreDir != "" {
		destPath := filepath.Join(gatewayDir, "gateway")
		arch := detectDockerArch()

		// Try building from source (dev mode)
		srcDir := filepath.Join(g.coreDir, "..")
		mainPkg := "./core/gateway/cmd/gateway/"
		if _, err := os.Stat(filepath.Join(srcDir, "core", "gateway", "cmd", "gateway", "main.go")); err == nil {
			if goPath, err := exec.LookPath("go"); err == nil {
				fmt.Fprintf(os.Stderr, "[dev] Building gateway binary (linux/%s)...\n", arch)
				cmd := exec.Command(goPath, "build", "-o", destPath, mainPkg)
				cmd.Dir = srcDir
				cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err == nil {
					return nil
				}
				fmt.Fprintf(os.Stderr, "[dev] Gateway build failed, falling back to pre-built binary\n")
			}
		}

		// Fall back to pre-built binary
		binaryPath := filepath.Join(g.coreDir, "gateway", "bin", "gateway-linux-"+arch)
		data, err := os.ReadFile(binaryPath)
		if err == nil {
			if err := os.WriteFile(destPath, data, 0755); err != nil {
				return fmt.Errorf("write gateway binary: %w", err)
			}
			return nil
		}
	}

	if g.gatewayFS != nil {
		arch := detectDockerArch()
		// Release mode: binary should be in the tarball at bin/gateway-linux-<arch>
		data, err := fs.ReadFile(g.gatewayFS, "bin/gateway-linux-"+arch)
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
	arch := detectDockerArch()
	placeholder := fmt.Sprintf("#!/bin/sh\necho 'ERROR: gateway binary not included — rebuild with: GOOS=linux GOARCH=%s go build -o core/gateway/bin/gateway-linux-%s ./core/gateway/cmd/gateway/' >&2\nexit 1\n", arch, arch)
	destPath := filepath.Join(gatewayDir, "gateway")
	return os.WriteFile(destPath, []byte(placeholder), 0755)
}

// detectDockerArch returns the target architecture for the gateway binary.
// It queries the Docker daemon first, falling back to the host's architecture.
func detectDockerArch() string {
	// Try Docker daemon architecture
	cmd := exec.Command("docker", "info", "--format", "{{.Architecture}}")
	if out, err := cmd.Output(); err == nil {
		arch := strings.TrimSpace(string(out))
		switch arch {
		case "x86_64":
			return "amd64"
		case "aarch64":
			return "arm64"
		default:
			if arch == "amd64" || arch == "arm64" {
				return arch
			}
		}
	}

	// Fall back to host architecture
	cmd = exec.Command("uname", "-m")
	if out, err := cmd.Output(); err == nil {
		arch := strings.TrimSpace(string(out))
		switch arch {
		case "x86_64":
			return "amd64"
		case "aarch64", "arm64":
			return "arm64"
		}
	}

	return "amd64" // safe default
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

		// Pass options through verbatim — ${VAR} references are resolved
		// by the gateway at startup from its own environment.
		resolvedOpts := make(map[string]any, len(inst.Options))
		for k, v := range inst.Options {
			resolvedOpts[k] = v
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

		// Add per-service middlewares (TS-based) — reserved for future use
		_ = rp.rendered.Gateway.Services

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

	// Compute a content hash of the gateway binary to bust Docker's layer cache.
	// Without this, Docker may reuse a stale COPY layer even after the binary changes.
	binaryPath := filepath.Join(gatewayDir, "gateway")
	binaryData, err := os.ReadFile(binaryPath)
	if err == nil {
		hash := sha256.Sum256(binaryData)
		hashStr := hex.EncodeToString(hash[:8]) // short hash is sufficient for cache busting
		dockerfile = strings.Replace(dockerfile, "ARG GATEWAY_HASH", "ARG GATEWAY_HASH="+hashStr, 1)
	}

	if err := os.WriteFile(filepath.Join(gatewayDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write gateway Dockerfile: %w", err)
	}
	return nil
}

// copyGatewayTypes writes gateway.d.ts into the .build/ directory so external
// projects can reference it for TypeScript autocompletion in middlewares.
func (g *Generator) copyGatewayTypes(buildDir string) error {
	data, err := g.templates.LoadRaw("gateway.d.ts")
	if err != nil {
		// Non-fatal: types file is optional for older cores
		return nil
	}
	return os.WriteFile(filepath.Join(buildDir, "gateway.d.ts"), []byte(data), 0644)
}


