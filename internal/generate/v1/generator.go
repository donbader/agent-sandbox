package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// Generator orchestrates v1 build artifact generation.
type Generator struct {
	projectDir string
	bundledFS  fs.FS
	coreDir    string
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

// Run executes the full generation pipeline.
func (g *Generator) Run() error {
	// 1. Load config
	cfg, err := config.LoadV1(g.projectDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 2. Resolve and render plugins
	resolver := plugin.NewResolver(g.projectDir, g.bundledFS)
	var allContribs []*plugin.Contributions

	for _, inst := range cfg.Installations {
		pluginDef, err := resolver.Resolve(inst.Plugin, inst.Source)
		if err != nil {
			return fmt.Errorf("resolve plugin %q: %w", inst.Plugin, err)
		}

		rendered, err := plugin.RenderContributions(pluginDef, inst.Options)
		if err != nil {
			return fmt.Errorf("render plugin %q: %w", inst.Plugin, err)
		}

		// Resolve middleware paths relative to the plugin's base directory
		if pluginDef.BaseDir != "" {
			for i, svc := range rendered.Gateway.Services {
				for j, mw := range svc.Middlewares {
					if mw.Custom != "" {
						rendered.Gateway.Services[i].Middlewares[j].Custom = filepath.Join(pluginDef.BaseDir, mw.Custom)
					}
				}
			}
		}

		allContribs = append(allContribs, rendered)
	}

	merged := plugin.MergeContributions(allContribs...)

	// 3. Create output directory
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	// 4. Generate Dockerfile
	dockerfile, err := BuildDockerfile(cfg, merged)
	if err != nil {
		return fmt.Errorf("build dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}

	// 5. Generate docker-compose.yaml
	compose, err := BuildCompose(cfg, merged)
	if err != nil {
		return fmt.Errorf("build compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yaml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yaml: %w", err)
	}

	// 6. Build gateway config + copy middleware
	gwCfg := BuildGatewayConfig(cfg, merged)
	if len(gwCfg.Middlewares) > 0 {
		if err := CopyCustomMiddleware(g.projectDir, buildDir, gwCfg.Middlewares); err != nil {
			return fmt.Errorf("copy middleware: %w", err)
		}
	}

	return nil
}
