package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/generate/templates"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// Generator orchestrates v1 build artifact generation.
type Generator struct {
	projectDir string
	bundledFS  fs.FS
	gatewayFS  fs.FS
	coreDir    string
	templates  *templates.Loader
}

type resolvedPlugin struct {
	def      *plugin.PluginDef
	rendered *plugin.Contributions
}

// NewGenerator creates a v1 generator for the given project directory.
func NewGenerator(projectDir string, bundledFS fs.FS) *Generator {
	return &Generator{
		projectDir: projectDir,
		bundledFS:  bundledFS,
		templates:  templates.NewEmbeddedLoader(),
	}
}

// NewGeneratorWithCore creates a v1 generator that reads bundled plugins from a specific core directory.
func NewGeneratorWithCore(projectDir, coreDir string) *Generator {
	var bundled fs.FS
	if coreDir != "" {
		pluginsDir := filepath.Join(coreDir, "plugins")
		bundled = os.DirFS(pluginsDir)
	}
	return &Generator{
		projectDir: projectDir,
		bundledFS:  bundled,
		coreDir:    coreDir,
		templates:  templates.FindLoader(coreDir),
	}
}

// SetGatewayFS sets the embedded filesystem containing gateway source code.
func (g *Generator) SetGatewayFS(gwFS fs.FS) {
	g.gatewayFS = gwFS
}

// SetBundledPluginsFS sets the embedded filesystem containing bundled plugin definitions.
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
func (g *Generator) RunWithConfig(cfg *config.Config, agentDir string) error {
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	merged, err := g.generateAgentArtifacts(cfg, agentDir, buildDir)
	if err != nil {
		return err
	}

	compose, err := BuildCompose(cfg, merged, g.projectDir)
	if err != nil {
		return fmt.Errorf("build compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	if err := g.extractGatewaySource(buildDir); err != nil {
		return fmt.Errorf("extract gateway source: %w", err)
	}

	// Copy the runtime config into gateway-src so the Docker build can COPY it into the image.
	// For single-agent, the baked-in config IS the runtime config (no volume mount override).
	gatewaySrcDir := filepath.Join(buildDir, "gateway-src")
	if err := os.MkdirAll(gatewaySrcDir, 0755); err != nil {
		return fmt.Errorf("create gateway-src dir: %w", err)
	}
	runtimeConfig, err := os.ReadFile(filepath.Join(buildDir, "config.yaml"))
	if err != nil {
		return fmt.Errorf("read runtime config for gateway image: %w", err)
	}
	if err := os.WriteFile(filepath.Join(gatewaySrcDir, "config.yaml"), runtimeConfig, 0644); err != nil {
		return fmt.Errorf("write gateway image config: %w", err)
	}

	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}
	return nil
}

// RunFleet executes the generation pipeline for a multi-agent fleet.
func (g *Generator) RunFleet(agents []config.FleetAgent) error {
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

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

	var entries []ComposeAgentEntry
	for _, b := range builds {
		entries = append(entries, ComposeAgentEntry{Config: b.cfg, Contribs: b.merged, BuildDir: b.buildDir})
	}

	compose, err := BuildFleetCompose(entries, g.projectDir)
	if err != nil {
		return fmt.Errorf("build fleet compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	if err := g.extractGatewaySource(buildDir); err != nil {
		return fmt.Errorf("extract gateway source: %w", err)
	}

	// Write placeholder config.yaml for gateway Docker build (per-agent config is volume-mounted at runtime).
	gatewaySrcDir := filepath.Join(buildDir, "gateway-src")
	if err := os.MkdirAll(gatewaySrcDir, 0755); err != nil {
		return fmt.Errorf("create gateway-src dir: %w", err)
	}
	placeholder := []byte("# Placeholder — per-agent config mounted at runtime\nlisten: \":8443\"\ndns_listen: \":53\"\n")
	if err := os.WriteFile(filepath.Join(gatewaySrcDir, "config.yaml"), placeholder, 0644); err != nil {
		return fmt.Errorf("write gateway placeholder config: %w", err)
	}

	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}
	return nil
}

// generateAgentArtifacts resolves plugins, generates Dockerfile + entrypoint + gateway config.
func (g *Generator) generateAgentArtifacts(cfg *config.Config, agentDir, buildDir string) (*plugin.Contributions, error) {
	resolver := plugin.NewResolver(agentDir, g.bundledFS)
	var allContribs []*plugin.Contributions
	resolved := make(map[string]*resolvedPlugin)

	for _, inst := range cfg.Installations {
		pluginDef, err := resolver.Resolve(inst.Plugin, inst.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin %q: %w", inst.Plugin, err)
		}

		if err := g.resolveAssetPaths(pluginDef, buildDir); err != nil {
			return nil, fmt.Errorf("resolve assets for plugin %q: %w", inst.Plugin, err)
		}

		rendered, err := plugin.RenderContributions(pluginDef, inst.Options)
		if err != nil {
			return nil, fmt.Errorf("render plugin %q: %w", inst.Plugin, err)
		}

		if pluginDef.BaseDir != "" {
			for i, svc := range rendered.Gateway.Services {
				for j, mw := range svc.Middlewares {
					if mw.Custom != "" {
						rendered.Gateway.Services[i].Middlewares[j].Custom = filepath.Join(pluginDef.BaseDir, mw.Custom)
					}
				}
			}
			for name, svc := range rendered.Sidecar.Services {
				if svc.Build != "" && !filepath.IsAbs(svc.Build) {
					svc.Build = filepath.Join(pluginDef.BaseDir, svc.Build)
					rendered.Sidecar.Services[name] = svc
				}
			}
		} else if g.bundledFS != nil {
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

	if err := validateRequires(resolved); err != nil {
		return nil, err
	}

	merged := plugin.MergeContributions(allContribs...)

	relBuildDir, err := filepath.Rel(g.projectDir, buildDir)
	if err != nil {
		return nil, fmt.Errorf("compute relative build dir: %w", err)
	}
	entrypointPath := filepath.Join(relBuildDir, "entrypoint.sh")

	dockerfile, err := RenderDockerfile(g.templates, cfg, merged, entrypointPath)
	if err != nil {
		return nil, fmt.Errorf("build dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return nil, fmt.Errorf("write Dockerfile: %w", err)
	}

	entrypoint, err := RenderEntrypointScript(g.templates, merged.Runtime.PreEntrypoint)
	if err != nil {
		return nil, fmt.Errorf("build entrypoint: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "entrypoint.sh"), []byte(entrypoint), 0755); err != nil {
		return nil, fmt.Errorf("write entrypoint.sh: %w", err)
	}

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

func collectAllOptions(cfg *config.Config) map[string]any {
	opts := make(map[string]any)
	for _, inst := range cfg.Installations {
		for k, v := range inst.Options {
			opts[k] = v
		}
	}
	return opts
}

func (g *Generator) extractGatewaySource(buildDir string) error {
	gatewayDest := filepath.Join(buildDir, "gateway-src")

	if g.coreDir != "" {
		gatewaySrc := filepath.Join(g.coreDir, "gateway")
		if _, err := os.Stat(gatewaySrc); err != nil {
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
		for _, name := range []string{"go.mod", "go.sum"} {
			var data []byte
			var found bool
			if d, err := os.ReadFile(filepath.Join(g.coreDir, name)); err == nil {
				data, found = d, true
			}
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
		return g.writeGatewayBuildFiles(gatewayDest)
	}

	if g.gatewayFS == nil {
		return nil
	}
	if err := extractFS(g.gatewayFS, ".", gatewayDest); err != nil {
		return err
	}
	return g.writeGatewayBuildFiles(gatewayDest)
}

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

func copyDir(src, dst string) error {
	return extractFS(os.DirFS(src), ".", dst)
}

func validateRequires(resolved map[string]*resolvedPlugin) error {
	installed := make(map[string]bool)
	for _, rp := range resolved {
		installed[rp.def.Name] = true
	}
	for ref, rp := range resolved {
		for _, req := range rp.def.Requires {
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

func (g *Generator) resolveAssetPaths(p *plugin.PluginDef, buildDir string) error {
	if len(p.Assets) == 0 {
		return nil
	}
	p.AssetPaths = make(map[string]string, len(p.Assets))

	for _, assetName := range p.Assets {
		name := strings.TrimSuffix(assetName, "/")
		if p.BaseDir != "" {
			p.AssetPaths[name] = filepath.Join(p.BaseDir, name)
		} else {
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
			p.AssetPaths[name] = ".build/plugins/" + p.Name + "/" + name
		}
	}
	return nil
}
