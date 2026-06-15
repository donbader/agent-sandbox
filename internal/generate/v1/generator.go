package v1

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/generate/templates"
	"github.com/donbader/agent-sandbox/internal/plugin"
)

// envVarRefRe matches shell-style ${VAR} or $VAR references in rendered output.
var envVarRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// Generator orchestrates v1 build artifact generation.
type Generator struct {
	projectDir string
	bundledFS  fs.FS
	gatewayFS  fs.FS
	coreDir    string
	templates  *templates.Loader
	presets    map[string]*Preset
}

// AgentResult holds the per-agent generation output.
type AgentResult struct {
	Config   *config.Config
	Contribs *plugin.Contributions
	BuildDir string // absolute path to the agent's build output directory
	Resolved map[string]*resolvedPlugin
}

type resolvedPlugin struct {
	def      *plugin.PluginDef
	rendered *plugin.Contributions
	options  map[string]any // user-provided options (with defaults applied)
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
	var presets map[string]*Preset
	if coreDir != "" {
		var err error
		presets, err = LoadPresets(coreDir)
		if err != nil {
			presets = nil // fall through — will error at generate time if preset is used
		}
	}
	return &Generator{
		projectDir: projectDir,
		bundledFS:  bundled,
		coreDir:    coreDir,
		templates:  templates.FindLoader(coreDir),
		presets:    presets,
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

// SetPresets sets the runtime presets map (used when no core directory is available, e.g. tests).
func (g *Generator) SetPresets(presets map[string]*Preset) {
	if g.presets == nil {
		g.presets = presets
	}
}

// RunProject executes the full generation pipeline for any project.
func (g *Generator) RunProject(project *config.Project) error {
	buildDir := filepath.Join(g.projectDir, ".build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("create .build dir: %w", err)
	}

	var entries []ComposeAgentEntry
	for _, agent := range project.Agents {
		agentBuildDir := filepath.Join(buildDir, agent.Name)
		if err := os.MkdirAll(agentBuildDir, 0755); err != nil {
			return fmt.Errorf("create build dir for %s: %w", agent.Name, err)
		}

		result, err := g.generateAgent(agent.Config, agent.Dir, agentBuildDir)
		if err != nil {
			return fmt.Errorf("generate %s: %w", agent.Name, err)
		}

		if err := g.writeGatewayBuild(agentBuildDir, result.Config, result.Contribs, result.Resolved); err != nil {
			return fmt.Errorf("write gateway build for %s: %w", agent.Name, err)
		}

		entries = append(entries, ComposeAgentEntry{
			Config:   result.Config,
			Contribs: result.Contribs,
			BuildDir: agentBuildDir,
		})
	}

	compose, err := BuildProjectCompose(entries, g.projectDir)
	if err != nil {
		return fmt.Errorf("build compose: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}

	if err := generateSchema(buildDir); err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}
	if err := g.copyGatewayTypes(buildDir); err != nil {
		return fmt.Errorf("copy gateway types: %w", err)
	}
	return nil
}

// generateAgent is the shared per-agent generation logic used by both Run() and RunFleet().
// It resolves plugins, generates Dockerfile + entrypoint + gateway config.
// All user-facing relative paths (e.g. home_directory) are resolved from agentDir
// and transformed to be relative to projectDir (the Docker build context).
func (g *Generator) generateAgent(cfg *config.Config, agentDir, buildDir string) (*AgentResult, error) {
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

		rendered, err := plugin.RenderContributions(pluginDef, inst.Options, plugin.RenderContext{
			Self: plugin.ConfigToMap(cfg),
		})
		if err != nil {
			return nil, fmt.Errorf("render plugin %q: %w", inst.Plugin, err)
		}

		// Warn about ${VAR} references in baked output that won't be expanded at runtime.
		warnUnresolvedVars(inst.Plugin, rendered)

		if pluginDef.BaseDir != "" {
			for name, svc := range rendered.Sidecar.Services {
				if svc.Build != "" && !filepath.IsAbs(svc.Build) {
					svc.Build = filepath.Join(pluginDef.BaseDir, svc.Build)
					rendered.Sidecar.Services[name] = svc
				}
			}
		}

		resolved[inst.Plugin] = &resolvedPlugin{def: pluginDef, rendered: rendered, options: inst.Options}
		allContribs = append(allContribs, rendered)
	}

	if err := validateRequires(resolved); err != nil {
		return nil, err
	}

	merged := plugin.MergeContributions(allContribs...)

	// Resolve user-facing relative paths to be relative to projectDir (Docker build context).
	// Paths in plugin contributions (extra_builds COPY, volumes) are relative to agentDir,
	// but Docker needs them relative to projectDir.
	g.resolveAgentPaths(merged, agentDir)

	relBuildDir, err := filepath.Rel(g.projectDir, buildDir)
	if err != nil {
		return nil, fmt.Errorf("compute relative build dir: %w", err)
	}
	entrypointPath := filepath.Join(relBuildDir, "entrypoint.sh")

	dockerfile, err := RenderDockerfile(g.templates, cfg, merged, entrypointPath, g.presets)
	if err != nil {
		return nil, fmt.Errorf("build dockerfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return nil, fmt.Errorf("write Dockerfile: %w", err)
	}

	entrypoint, err := RenderEntrypointScript(g.templates, merged.Runtime.PreEntrypoint, cfg.Runtime.CWD)
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

	return &AgentResult{Config: cfg, Contribs: merged, BuildDir: buildDir, Resolved: resolved}, nil
}

// resolveAgentPaths transforms relative paths in plugin contributions from agentDir-relative
// to projectDir-relative (Docker build context). This is a no-op when agentDir == projectDir.
func (g *Generator) resolveAgentPaths(contribs *plugin.Contributions, agentDir string) {
	if agentDir == g.projectDir {
		return
	}

	relAgent, err := filepath.Rel(g.projectDir, agentDir)
	if err != nil {
		return
	}

	// Transform extra_builds: rewrite COPY source paths that start with ./
	for i, line := range contribs.Runtime.ExtraBuilds {
		contribs.Runtime.ExtraBuilds[i] = rewriteCopyPath(line, relAgent)
	}

	// Transform volume bind-mount sources that start with ./
	for i, vol := range contribs.Runtime.Volumes {
		contribs.Runtime.Volumes[i] = rewriteVolumePath(vol, relAgent)
	}
}

// rewriteCopyPath rewrites Dockerfile COPY instructions to prefix relative source paths
// with the agent's relative directory.
// "COPY ./home /home/agent/" → "COPY ./agent-001/home /home/agent/"
func rewriteCopyPath(line, relAgent string) string {
	// Match "COPY ./something ..." pattern
	if !strings.HasPrefix(line, "COPY ./") {
		return line
	}
	// Split "COPY <src> <dst>"
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return line
	}
	src := parts[1]
	if strings.HasPrefix(src, "./") {
		parts[1] = "./" + filepath.Join(relAgent, src[2:])
	}
	return strings.Join(parts, " ")
}

// rewriteVolumePath rewrites bind-mount volume sources that start with ./ to be
// relative to projectDir instead of agentDir.
// "./home:/home/agent" → "./agent-001/home:/home/agent"
func rewriteVolumePath(vol, relAgent string) string {
	// Named volumes (no leading ./ or /) are left unchanged
	if !strings.HasPrefix(vol, "./") {
		return vol
	}
	colonIdx := strings.Index(vol, ":")
	if colonIdx < 0 {
		return vol
	}
	src := vol[:colonIdx]
	rest := vol[colonIdx:]
	if strings.HasPrefix(src, "./") {
		src = "./" + filepath.Join(relAgent, src[2:])
	}
	return src + rest
}

func extractFS(srcFS fs.FS, root, dest string) error {
	return extractFSWithExclude(srcFS, root, dest, nil)
}

// extractFSWithExclude copies an fs.FS tree to dest, skipping entries whose
// top-level directory name matches any pattern in exclude.
func extractFSWithExclude(srcFS fs.FS, root, dest string, exclude []string) error {
	return fs.WalkDir(srcFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if shouldExclude(path, exclude) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
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

// copyDirWithExclude copies a directory tree, skipping entries matching exclude patterns.
func copyDirWithExclude(src, dst string, exclude []string) error {
	return extractFSWithExclude(os.DirFS(src), ".", dst, exclude)
}

// shouldExclude returns true if any path segment matches an exclude pattern.
func shouldExclude(path string, exclude []string) bool {
	if len(exclude) == 0 {
		return false
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		for _, pattern := range exclude {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
	}
	return false
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

	for _, asset := range p.Assets {
		name := strings.TrimSuffix(asset.Path, "/")
		if p.BaseDir != "" {
			if len(asset.Exclude) > 0 {
				// Copy with excludes into build dir so Docker context is clean
				srcPath := filepath.Join(p.BaseDir, name)
				dstPath := filepath.Join(buildDir, "plugins", p.Name, name)
				if err := copyDirWithExclude(srcPath, dstPath, asset.Exclude); err != nil {
					return fmt.Errorf("copy asset %q with excludes: %w", name, err)
				}
				relPath, err := filepath.Rel(g.projectDir, dstPath)
				if err != nil {
					relPath = dstPath
				}
				p.AssetPaths[name] = relPath
			} else {
				p.AssetPaths[name] = filepath.Join(p.BaseDir, name)
			}
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
			if err := extractFSWithExclude(subFS, ".", dstPath, asset.Exclude); err != nil {
				return fmt.Errorf("extract asset %q: %w", name, err)
			}
			// AssetPaths must be relative to projectDir (the Docker build context).
			relPath, err := filepath.Rel(g.projectDir, dstPath)
			if err != nil {
				relPath = dstPath
			}
			p.AssetPaths[name] = relPath
		}
	}
	return nil
}

// warnUnresolvedVars checks rendered plugin contributions for ${VAR} references
// that will be baked literally into the Docker image and won't be expanded at runtime.
// This catches a common mistake where users put env var references in plugin options
// expecting them to be resolved, but they pass through the template engine verbatim.
func warnUnresolvedVars(pluginName string, contribs *plugin.Contributions) {
	if contribs == nil {
		return
	}

	// Check extra_builds lines (baked into Dockerfile at generate time)
	for _, line := range contribs.Runtime.ExtraBuilds {
		// Skip lines that are Dockerfile ENV or ARG directives — those are intentionally
		// setting up variables for later RUN commands within the same build stage.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ENV ") || strings.HasPrefix(trimmed, "ARG ") {
			continue
		}
		matches := envVarRefRe.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			varName := m[1]
			if varName == "" {
				varName = m[2]
			}
			fmt.Fprintf(os.Stderr, "Warning: plugin %q extra_builds contains ${%s} which will be baked literally into the Docker image.\n", pluginName, varName)
			fmt.Fprintf(os.Stderr, "  Hint: plugin options are rendered at generate time — use a literal value instead.\n")
		}
	}
}
