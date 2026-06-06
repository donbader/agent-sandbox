package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// Generator orchestrates v1 build artifact generation.
type Generator struct {
	projectDir string
	bundledFS  fs.FS
	gatewayFS  fs.FS
	coreDir    string
}

type resolvedPlugin struct {
	def      *plugin.PluginDef
	rendered *plugin.Contributions
}

// NewGenerator creates a v1 generator for the given project directory.
func NewGenerator(projectDir string, bundledFS fs.FS) *Generator {
	return &Generator{projectDir: projectDir, bundledFS: bundledFS}
}

// NewGeneratorWithCore creates a v1 generator that reads bundled plugins from a specific core directory.
func NewGeneratorWithCore(projectDir, coreDir string) *Generator {
	var bundled fs.FS
	if coreDir != "" {
		pluginsDir := filepath.Join(coreDir, "plugins")
		bundled = os.DirFS(pluginsDir)
	}
	return &Generator{projectDir: projectDir, bundledFS: bundled, coreDir: coreDir}
}

// SetGatewayFS sets the embedded filesystem containing gateway source code.
func (g *Generator) SetGatewayFS(gwFS fs.FS) {
	g.gatewayFS = gwFS
}

// SetBundledPluginsFS sets the embedded filesystem containing bundled plugin definitions.
// Used when no external core directory is specified.
func (g *Generator) SetBundledPluginsFS(pluginsFS fs.FS) {
	if g.bundledFS == nil {
		g.bundledFS = pluginsFS
	}
}

// Run executes the full generation pipeline for a single-agent project.
func (g *Generator) Run() error {
	cfg, err := config.Load(g.projectDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	return g.RunWithConfig(cfg, g.projectDir)
}

// RunWithConfig executes the generation pipeline for a pre-loaded config.
// agentDir is the directory containing the agent's config and local plugins.
func (g *Generator) RunWithConfig(cfg *config.Config, agentDir string) error {
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	merged, err := g.generateAgentArtifacts(cfg, agentDir, buildDir)
	if err != nil {
		return err
	}

	// Generate docker-compose.yml (single-agent mode)
	compose, err := BuildCompose(cfg, merged, g.projectDir)
	if err != nil {
		return fmt.Errorf("build compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	// Extract gateway source
	if err := g.extractGatewaySource(buildDir); err != nil {
		return fmt.Errorf("extract gateway source: %w", err)
	}

	// Generate JSON Schema
	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}

	return nil
}

// RunFleet executes the generation pipeline for a multi-agent fleet.
// All agents share a single .build/ directory with a unified docker-compose.yml.
func (g *Generator) RunFleet(agents []config.FleetAgent) error {
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	// Generate per-agent artifacts (Dockerfile, entrypoint, gateway config)
	type agentBuild struct {
		cfg      *config.Config
		merged   *plugin.Contributions
		buildDir string
	}
	var builds []agentBuild

	for _, agent := range agents {
		agentBuildDir := filepath.Join(buildDir, agent.Config.Name)
		if err := os.MkdirAll(agentBuildDir, 0755); err != nil {
			return fmt.Errorf("create build dir for %s: %w", agent.Config.Name, err)
		}

		merged, err := g.generateAgentArtifacts(agent.Config, agent.Dir, agentBuildDir)
		if err != nil {
			return fmt.Errorf("generate %s: %w", agent.Config.Name, err)
		}

		builds = append(builds, agentBuild{cfg: agent.Config, merged: merged, buildDir: agentBuildDir})
	}

	// Generate unified docker-compose.yml
	var entries []ComposeAgentEntry
	for _, b := range builds {
		entries = append(entries, ComposeAgentEntry{
			Config:   b.cfg,
			Contribs: b.merged,
			BuildDir: b.buildDir,
		})
	}

	compose, err := BuildFleetCompose(entries, g.projectDir)
	if err != nil {
		return fmt.Errorf("build fleet compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	// Extract shared gateway source (once for all agents)
	if err := g.extractGatewaySource(buildDir); err != nil {
		return fmt.Errorf("extract gateway source: %w", err)
	}

	// Generate JSON Schema
	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}

	return nil
}

// generateAgentArtifacts resolves plugins, generates Dockerfile + entrypoint + gateway config.
// Returns merged contributions for compose generation.
func (g *Generator) generateAgentArtifacts(cfg *config.Config, agentDir, buildDir string) (*plugin.Contributions, error) {
	resolver := plugin.NewResolver(agentDir, g.bundledFS)
	var allContribs []*plugin.Contributions
	resolved := make(map[string]*resolvedPlugin)

	for _, inst := range cfg.Installations {
		pluginDef, err := resolver.Resolve(inst.Plugin, inst.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin %q: %w", inst.Plugin, err)
		}

		// Resolve asset paths before rendering so {{ asset "X" }} works in templates
		if err := g.resolveAssetPaths(pluginDef, buildDir); err != nil {
			return nil, fmt.Errorf("resolve assets for plugin %q: %w", inst.Plugin, err)
		}

		rendered, err := plugin.RenderContributions(pluginDef, inst.Options)
		if err != nil {
			return nil, fmt.Errorf("render plugin %q: %w", inst.Plugin, err)
		}

		// Resolve middleware and sidecar paths relative to the plugin's base directory
		if pluginDef.BaseDir != "" {
			for i, svc := range rendered.Gateway.Services {
				for j, mw := range svc.Middlewares {
					if mw.Custom != "" {
						rendered.Gateway.Services[i].Middlewares[j].Custom = filepath.Join(pluginDef.BaseDir, mw.Custom)
					}
				}
			}
			// Resolve sidecar build paths relative to plugin directory
			for name, svc := range rendered.Sidecar.Services {
				if svc.Build != "" && !filepath.IsAbs(svc.Build) {
					svc.Build = filepath.Join(pluginDef.BaseDir, svc.Build)
					rendered.Sidecar.Services[name] = svc
				}
			}
		} else if g.bundledFS != nil {
			// Bundled plugin: extract middleware files from embedded FS to buildDir
			for i, svc := range rendered.Gateway.Services {
				for j, mw := range svc.Middlewares {
					if mw.Custom != "" {
						extractedPath, err := g.extractBundledMiddleware(pluginDef.Name, mw.Custom, buildDir)
						if err != nil {
							return nil, fmt.Errorf("extract middleware %q from plugin %q: %w", mw.Custom, inst.Plugin, err)
						}
						rendered.Gateway.Services[i].Middlewares[j].Custom = extractedPath
					}
				}
			}
		}

		resolved[inst.Plugin] = &resolvedPlugin{def: pluginDef, rendered: rendered}
		allContribs = append(allContribs, rendered)
	}

	// 4. Validate plugin dependencies
	if err := validateRequires(resolved); err != nil {
		return nil, err
	}

	merged := plugin.MergeContributions(allContribs...)

	// Generate Dockerfile + entrypoint.sh (transparent proxy bootstrap)
	dockerfile, err := BuildDockerfile(cfg, merged)
	if err != nil {
		return nil, fmt.Errorf("build dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return nil, fmt.Errorf("write Dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "entrypoint.sh"), []byte(EntrypointScript(merged.Runtime.PreEntrypoint)), 0755); err != nil {
		return nil, fmt.Errorf("write entrypoint.sh: %w", err)
	}

	// Build gateway config + copy middleware
	gwCfg := BuildGatewayConfig(cfg, merged)
	if err := WriteGatewayRuntimeConfig(buildDir, gwCfg); err != nil {
		return nil, fmt.Errorf("write gateway runtime config: %w", err)
	}
	if len(gwCfg.Middlewares) > 0 {
		allOpts := collectAllOptions(cfg)
		if err := CopyCustomMiddleware(g.projectDir, buildDir, gwCfg.Middlewares, allOpts); err != nil {
			return nil, fmt.Errorf("copy middleware: %w", err)
		}
	}
	if len(gwCfg.AuthHeaders) > 0 {
		if err := GenerateAuthHeaderMiddleware(buildDir, gwCfg.AuthHeaders); err != nil {
			return nil, fmt.Errorf("generate auth-header middleware: %w", err)
		}
	}

	return merged, nil
}

// collectAllOptions merges all installation options into a single map.
func collectAllOptions(cfg *config.Config) map[string]any {
	opts := make(map[string]any)
	for _, inst := range cfg.Installations {
		for k, v := range inst.Options {
			opts[k] = v
		}
	}
	return opts
}

// extractGatewaySource copies the gateway source tree into .build/gateway-src/.
func (g *Generator) extractGatewaySource(buildDir string) error {
	gatewayDest := filepath.Join(buildDir, "gateway-src")

	// If coreDir is set, copy from local filesystem
	if g.coreDir != "" {
		gatewaySrc := filepath.Join(g.coreDir, "gateway")
		if _, err := os.Stat(gatewaySrc); err != nil {
			// No gateway source in core dir — skip
			return nil
		}
		if err := os.MkdirAll(filepath.Join(gatewayDest, "core"), 0755); err != nil {
			return err
		}
		if err := copyDir(gatewaySrc, filepath.Join(gatewayDest, "core", "gateway")); err != nil {
			return err
		}
		sdkSrc := filepath.Join(g.coreDir, "sdk")
		if _, err := os.Stat(sdkSrc); err == nil {
			if err := copyDir(sdkSrc, filepath.Join(gatewayDest, "core", "sdk")); err != nil {
				return err
			}
		}
		// Copy go.mod and go.sum — check coreDir root first, then project root
		for _, name := range []string{"go.mod", "go.sum"} {
			var data []byte
			var found bool
			// Try coreDir (for self-contained core distributions)
			if d, err := os.ReadFile(filepath.Join(g.coreDir, name)); err == nil {
				data, found = d, true
			}
			// Fallback: project root (local development)
			if !found {
				if d, err := os.ReadFile(filepath.Join(g.projectDir, name)); err == nil {
					data, found = d, true
				}
			}
			if found {
				if err := os.WriteFile(filepath.Join(gatewayDest, name), data, 0644); err != nil {
					return fmt.Errorf("write %s: %w", name, err)
				}
			}
		}
		return writeGatewayBuildFiles(gatewayDest)
	}

	// Otherwise, extract from embedded FS
	if g.gatewayFS == nil {
		// No gateway source available — skip (tests may not provide it)
		return nil
	}

	if err := extractFS(g.gatewayFS, ".", gatewayDest); err != nil {
		return err
	}

	return writeGatewayBuildFiles(gatewayDest)
}

// writeGatewayBuildFiles generates the Dockerfile for the gateway build context.
// go.mod and go.sum are expected to already exist (from embedded FS or coreDir copy).
func writeGatewayBuildFiles(gatewayDir string) error {
	if err := os.MkdirAll(gatewayDir, 0755); err != nil {
		return err
	}

	// Dockerfile for gateway
	dockerfile := `FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY core/ core/
RUN go build -o /gateway ./core/gateway/cmd/gateway/

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget
COPY --from=builder /gateway /usr/local/bin/gateway
COPY config.yaml /etc/gateway/config.yaml
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=3 CMD wget --spider -q http://localhost:8080/health || exit 1
CMD ["gateway"]
`
	if err := os.WriteFile(filepath.Join(gatewayDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write gateway Dockerfile: %w", err)
	}

	return nil
}

// extractFS extracts all files from an fs.FS to a destination directory.
func extractFS(srcFS fs.FS, root, dest string) error {
	return fs.WalkDir(srcFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		destPath := filepath.Join(dest, path)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := fs.ReadFile(srcFS, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	})
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	return extractFS(os.DirFS(src), ".", dst)
}

// validateRequires checks that all plugin dependencies are satisfied.
func validateRequires(resolved map[string]*resolvedPlugin) error {
	// Build set of installed plugin names (by their PluginDef.Name)
	installed := make(map[string]bool)
	for _, rp := range resolved {
		installed[rp.def.Name] = true
	}

	for ref, rp := range resolved {
		for _, req := range rp.def.Requires {
			// Check by plugin def name (strip @builtin/ prefix for comparison)
			reqName := req
			if len(reqName) > 9 && reqName[:9] == "@builtin/" {
				reqName = reqName[9:]
			}
			if !installed[reqName] {
				return fmt.Errorf("plugin %q requires %q — add it to installations", ref, req)
			}
		}
	}
	return nil
}

// resolveAssetPaths extracts declared assets and populates pluginDef.AssetPaths.
//
// For bundled plugins: extracts from embedded FS to .build/plugins/<name>/<asset>/
// For local plugins: resolves relative to plugin's BaseDir
//
// After this, {{ asset "X" }} in templates resolves to the correct Docker COPY path.
func (g *Generator) resolveAssetPaths(p *plugin.PluginDef, buildDir string) error {
	if len(p.Assets) == 0 {
		return nil
	}

	p.AssetPaths = make(map[string]string, len(p.Assets))

	for _, assetName := range p.Assets {
		// Trim trailing slash
		name := strings.TrimSuffix(assetName, "/")

		if p.BaseDir != "" {
			// Local plugin: asset is relative to plugin directory
			p.AssetPaths[name] = filepath.Join(p.BaseDir, name)
		} else {
			// Bundled plugin: extract from embedded FS to .build/plugins/<plugin>/<asset>/
			if g.bundledFS == nil {
				return fmt.Errorf("plugin %q declares asset %q but no bundled FS available", p.Name, name)
			}

			srcPath := p.Name + "/" + name
			dstPath := filepath.Join(buildDir, "plugins", p.Name, name)

			subFS, err := fs.Sub(g.bundledFS, srcPath)
			if err != nil {
				return fmt.Errorf("asset %q not found in bundled plugin %q", name, p.Name)
			}

			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}
			if err := extractFS(subFS, ".", dstPath); err != nil {
				return fmt.Errorf("extract asset %q: %w", name, err)
			}

			// Path relative to Docker build context (project root)
			p.AssetPaths[name] = ".build/plugins/" + p.Name + "/" + name
		}
	}

	return nil
}
