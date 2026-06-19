package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const builtinPrefix = "@builtin/"
const fleetPrefix = "@fleet/"

// Resolver locates and loads plugin definitions.
type Resolver struct {
	projectDir string
	fleetDir   string // project root (fleet.yaml location); empty if standalone
	bundledFS  fs.FS
}

// NewResolver creates a resolver that looks up plugins by explicit prefix.
func NewResolver(projectDir string, bundledFS fs.FS) *Resolver {
	return &Resolver{projectDir: projectDir, bundledFS: bundledFS}
}

// SetFleetDir sets the fleet/project root directory for @fleet/ path resolution.
func (r *Resolver) SetFleetDir(dir string) {
	r.fleetDir = dir
}

// Resolve finds and parses a plugin by name.
//
// Plugin name prefixes control resolution:
//   - "@builtin/name" — resolve from bundled FS
//   - "./path"        — resolve from local filesystem (relative to project dir)
//
// Bare names without a prefix are rejected.
func (r *Resolver) Resolve(name string) (*PluginDef, error) {
	// Explicit @builtin/ prefix — bundled only
	if after, ok := strings.CutPrefix(name, builtinPrefix); ok {
		return r.resolveFromBundled(after)
	}

	// Explicit @fleet/ prefix — resolve relative to fleet/project root
	if after, ok := strings.CutPrefix(name, fleetPrefix); ok {
		if r.fleetDir == "" {
			return nil, fmt.Errorf("plugin %q: @fleet/ prefix requires fleet mode", name)
		}
		return r.resolveFromDir(r.fleetDir, after, "@fleet/"+after)
	}

	// Explicit ./ prefix — local only
	if strings.HasPrefix(name, "./") {
		return r.resolveFromDir(r.projectDir, name, name)
	}

	return nil, fmt.Errorf("plugin %q: must use @builtin/%s, @fleet/<path>, or ./<path> prefix", name, name)
}

// resolveFromBundled resolves a plugin exclusively from the bundled FS.
// BaseDir is intentionally left empty — bundled plugins have no filesystem path.
func (r *Resolver) resolveFromBundled(name string) (*PluginDef, error) {
	if r.bundledFS == nil {
		return nil, fmt.Errorf("plugin %q: @builtin/ requested but no bundled plugins available", name)
	}
	bundledPath := path.Join(name, "plugin.yaml")
	data, err := fs.ReadFile(r.bundledFS, bundledPath)
	if err != nil {
		return nil, fmt.Errorf("plugin %q not found in bundled plugins", name)
	}
	return ParsePluginYAML(data)
}

// resolveFromDir resolves a plugin from a path relative to baseDir.
// label is used in error messages (e.g. the original plugin reference).
func (r *Resolver) resolveFromDir(baseDir, relPath, label string) (*PluginDef, error) {
	localDir := filepath.Join(baseDir, relPath)
	cleanDir := filepath.Clean(localDir)
	cleanBase := filepath.Clean(baseDir)
	if !strings.HasPrefix(cleanDir, cleanBase+string(filepath.Separator)) {
		return nil, fmt.Errorf("plugin path %s escapes project directory", label)
	}

	localPath := filepath.Join(cleanDir, "plugin.yaml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("plugin at %s not found (checked %s)", label, localPath)
	}
	p, err := ParsePluginYAML(data)
	if err != nil {
		return nil, err
	}
	p.BaseDir = cleanDir
	return p, nil
}
