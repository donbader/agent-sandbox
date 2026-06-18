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
// If source is non-empty, it's a remote plugin (future).
func (r *Resolver) Resolve(name string, source string) (*PluginDef, error) {
	// Remote (future — source field)
	if source != "" {
		return nil, fmt.Errorf("remote plugin resolution not yet implemented: %s", source)
	}

	// Explicit @builtin/ prefix — bundled only
	if after, ok := strings.CutPrefix(name, builtinPrefix); ok {
		pluginName := after
		return r.resolveFromBundled(pluginName)
	}

	// Explicit @fleet/ prefix — resolve relative to fleet/project root
	if after, ok := strings.CutPrefix(name, fleetPrefix); ok {
		if r.fleetDir == "" {
			return nil, fmt.Errorf("plugin %q: @fleet/ prefix requires fleet mode", name)
		}
		return r.resolveFromFleet(after)
	}

	// Explicit ./ prefix — local only
	if strings.HasPrefix(name, "./") {
		return r.resolveFromLocal(name)
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

// resolveFromLocal resolves a plugin from a local path relative to the project dir.
// Rejects paths that escape the project directory.
func (r *Resolver) resolveFromLocal(relPath string) (*PluginDef, error) {
	localDir := filepath.Join(r.projectDir, relPath)
	// Prevent path traversal outside project dir
	cleanDir := filepath.Clean(localDir)
	cleanProject := filepath.Clean(r.projectDir)
	if !strings.HasPrefix(cleanDir, cleanProject+string(filepath.Separator)) {
		return nil, fmt.Errorf("plugin path %q escapes project directory", relPath)
	}

	localPath := filepath.Join(cleanDir, "plugin.yaml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("plugin at %q not found (checked %s)", relPath, localPath)
	}
	p, err := ParsePluginYAML(data)
	if err != nil {
		return nil, err
	}
	p.BaseDir = cleanDir
	return p, nil
}

// resolveFromFleet resolves a plugin from a path relative to the fleet/project root.
// Used for @fleet/ prefixed plugin references in fleet mode.
func (r *Resolver) resolveFromFleet(relPath string) (*PluginDef, error) {
	localDir := filepath.Join(r.fleetDir, relPath)
	cleanDir := filepath.Clean(localDir)
	cleanFleet := filepath.Clean(r.fleetDir)
	if !strings.HasPrefix(cleanDir, cleanFleet+string(filepath.Separator)) {
		return nil, fmt.Errorf("plugin path @fleet/%s escapes project directory", relPath)
	}

	localPath := filepath.Join(cleanDir, "plugin.yaml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("plugin at @fleet/%s not found (checked %s)", relPath, localPath)
	}
	p, err := ParsePluginYAML(data)
	if err != nil {
		return nil, err
	}
	p.BaseDir = cleanDir
	return p, nil
}
