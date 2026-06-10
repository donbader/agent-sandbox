package v1

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

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
				// Collect custom Go middlewares from resolved plugins
				customMws := g.collectCustomMiddlewares(resolved)

				buildSrcDir := srcDir
				if len(customMws) > 0 {
					// Copy source to temp dir to avoid polluting the source tree
					tmpDir, err := os.MkdirTemp("", "gateway-build-*")
					if err != nil {
						return fmt.Errorf("create temp dir for gateway build: %w", err)
					}
					defer func() { _ = os.RemoveAll(tmpDir) }()

					// Copy only what's needed for the build
					for _, dir := range []string{"core", "go.mod", "go.sum"} {
						srcPath := filepath.Join(srcDir, dir)
						dstPath := filepath.Join(tmpDir, dir)
						info, err := os.Stat(srcPath)
						if err != nil {
							continue
						}
						if info.IsDir() {
							if err := copyDir(srcPath, dstPath); err != nil {
								return fmt.Errorf("copy %s to temp: %w", dir, err)
							}
						} else {
							data, err := os.ReadFile(srcPath)
							if err != nil {
								return fmt.Errorf("read %s: %w", dir, err)
							}
							if err := os.WriteFile(dstPath, data, 0644); err != nil {
								return fmt.Errorf("write %s: %w", dir, err)
							}
						}
					}

					// Generate wrapped middleware files
					customDir := filepath.Join(tmpDir, "core", "gateway", "middlewares", "custom")
					if err := os.MkdirAll(customDir, 0755); err != nil {
						return fmt.Errorf("create custom middleware dir: %w", err)
					}
				for _, mw := range customMws {
					wrapped := generateMiddlewareWrapper(mw)
					if err := os.WriteFile(filepath.Join(customDir, mw.fileName), []byte(wrapped), 0644); err != nil {
						return fmt.Errorf("write custom middleware %s: %w", mw.fileName, err)
					}
				}

				// Add blank import
				importFile := filepath.Join(tmpDir, "core", "gateway", "cmd", "gateway", "custom_import_.go")
				importContent := "// Code generated by agent-sandbox generate. DO NOT EDIT.\npackage main\n\nimport _ \"github.com/donbader/agent-sandbox/core/gateway/middlewares/custom\"\n"
				if err := os.WriteFile(importFile, []byte(importContent), 0644); err != nil {
					return fmt.Errorf("write custom import file: %w", err)
				}

					buildSrcDir = tmpDir
				}

				fmt.Fprintf(os.Stderr, "[dev] Building gateway binary (linux/%s)...\n", arch)
				cmd := exec.Command(goPath, "build", "-o", destPath, mainPkg)
				cmd.Dir = buildSrcDir
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

// customMiddlewareInfo holds everything needed to generate a wrapped middleware file.
type customMiddlewareInfo struct {
	name     string   // middleware name (derived from file name)
	fileName string   // output .go file name
	body     string   // rendered handler body
	domains  []string // domains from the service URL
}

// collectCustomMiddlewares finds all custom Go middlewares in resolved plugins,
// reads and renders their body templates, and returns the info needed to generate wrappers.
func (g *Generator) collectCustomMiddlewares(resolved map[string]*resolvedPlugin) []customMiddlewareInfo {
	var result []customMiddlewareInfo

	for _, rp := range resolved {
		if rp.rendered == nil {
			continue
		}
		for _, svc := range rp.rendered.Gateway.Services {
			domain := extractDomain(svc.URL)
			for _, mw := range svc.Middlewares {
				if mw.Custom == "" {
					continue
				}

				// Read the middleware body file
				var bodyPath string
				if rp.def.BaseDir != "" {
					bodyPath = filepath.Join(rp.def.BaseDir, mw.Custom)
				} else if g.coreDir != "" {
					bodyPath = filepath.Join(g.coreDir, "plugins", rp.def.Name, mw.Custom)
				}
				if bodyPath == "" {
					continue
				}

				bodyBytes, err := os.ReadFile(bodyPath)
				if err != nil {
					continue
				}

				// Strip build tags and leading comments (the body is just handler logic)
				body := stripMiddlewarePreamble(string(bodyBytes))

				// Render the body as a Go template
				renderedBody := renderMiddlewareBody(body, rp, svc)

				// Derive name from file name
				baseName := filepath.Base(mw.Custom)
				name := strings.TrimSuffix(baseName, ".go")

				domains := []string{}
				if domain != "" {
					domains = append(domains, domain)
				}

				result = append(result, customMiddlewareInfo{
					name:     name,
					fileName: baseName,
					body:     renderedBody,
					domains:  domains,
				})
			}
		}
	}
	return result
}

// renderMiddlewareBody renders the middleware body template with plugin options and service context.
func renderMiddlewareBody(body string, rp *resolvedPlugin, svc plugin.GatewayService) string {
	tmpl, err := template.New("mw").Parse(body)
	if err != nil {
		return body // return raw if template fails
	}

	// Build options: start with schema defaults, overlay user-provided values
	opts := make(map[string]any)
	if rp.def != nil && rp.def.Options != nil {
		for name, schema := range rp.def.Options {
			if schema.Default != nil {
				opts[name] = schema.Default
			}
		}
	}
	for k, v := range rp.options {
		opts[k] = v
	}

	// Resolve env var references in option values (e.g. ${TELEGRAM_BOT_TOKEN} → actual value)
	for k, v := range opts {
		if s, ok := v.(string); ok {
			opts[k] = envvar.Expand(s)
		}
	}

	domain := extractDomain(svc.URL)
	domains := []string{}
	if domain != "" {
		domains = append(domains, domain)
	}

	data := map[string]any{
		"options": opts,
		"domains": domains,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return body
	}
	return buf.String()
}

// generateMiddlewareWrapper generates a complete Go file that wraps a middleware body
// in proper init() + RegisterMiddleware + BindDomains calls.
func generateMiddlewareWrapper(mw customMiddlewareInfo) string {
	domainsStr := ""
	for i, d := range mw.domains {
		if i > 0 {
			domainsStr += ", "
		}
		domainsStr += `"` + d + `"`
	}

	return fmt.Sprintf(`// Code generated by agent-sandbox generate. DO NOT EDIT.
package custom

import (
	"strings"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

// Ensure strings is used (middleware body may reference it).
var _ = strings.Contains

func init() {
	gateway.RegisterMiddleware(%q, func(ctx *gateway.MiddlewareContext) error {
		%s
	})
	gateway.BindDomains(%q, []string{%s})
}
`, mw.name, mw.body, mw.name, domainsStr)
}

// stripMiddlewarePreamble removes build tags and leading comments from a middleware body file.
// The body file may start with //go:build ignore and comment lines describing template variables.
func stripMiddlewarePreamble(src string) string {
	lines := strings.Split(src, "\n")
	start := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			start = i + 1
			continue
		}
		break
	}
	if start >= len(lines) {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}
