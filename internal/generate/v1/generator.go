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
	gatewayFS  fs.FS
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

// SetGatewayFS sets the embedded filesystem containing gateway source code.
func (g *Generator) SetGatewayFS(gwFS fs.FS) {
	g.gatewayFS = gwFS
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
		}

		allContribs = append(allContribs, rendered)
	}

	merged := plugin.MergeContributions(allContribs...)

	// 3. Create output directory
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	// 4. Generate Dockerfile + entrypoint.sh (transparent proxy bootstrap)
	dockerfile, err := BuildDockerfile(cfg, merged)
	if err != nil {
		return fmt.Errorf("build dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "entrypoint.sh"), []byte(EntrypointScript()), 0755); err != nil {
		return fmt.Errorf("write entrypoint.sh: %w", err)
	}

	// 5. Generate docker-compose.yml
	compose, err := BuildCompose(cfg, merged, g.projectDir)
	if err != nil {
		return fmt.Errorf("build compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	// 6. Extract gateway source into .build/gateway-src/
	if err := g.extractGatewaySource(buildDir); err != nil {
		return fmt.Errorf("extract gateway source: %w", err)
	}

	// 7. Build gateway config + copy middleware
	gwCfg := BuildGatewayConfig(cfg, merged)
	if err := WriteGatewayRuntimeConfig(buildDir, gwCfg); err != nil {
		return fmt.Errorf("write gateway runtime config: %w", err)
	}
	if len(gwCfg.Middlewares) > 0 {
		if err := CopyCustomMiddleware(g.projectDir, buildDir, gwCfg.Middlewares); err != nil {
			return fmt.Errorf("copy middleware: %w", err)
		}
	}

	return nil
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
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	})
}
