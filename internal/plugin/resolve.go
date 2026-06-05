package plugin

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const builtinPrefix = "@builtin/"

// Resolver locates and loads plugin definitions.
type Resolver struct {
	projectDir string
	bundledFS  fs.FS
}

// NewResolver creates a resolver that checks local plugins/ dir first, then bundled FS.
func NewResolver(projectDir string, bundledFS fs.FS) *Resolver {
	return &Resolver{projectDir: projectDir, bundledFS: bundledFS}
}

// Resolve finds and parses a plugin by name.
//
// Plugin name prefixes control resolution:
//   - "@builtin/name" — resolve only from bundled FS
//   - "./path"        — resolve only from local filesystem (relative to project dir)
//   - "name"          — legacy: local plugins/<name>/ first, then bundled (warns on shadow)
//
// If source is non-empty, it's a remote plugin (future).
func (r *Resolver) Resolve(name string, source string) (*PluginDef, error) {
	// Remote (future — source field)
	if source != "" {
		return nil, fmt.Errorf("remote plugin resolution not yet implemented: %s", source)
	}

	// Explicit @builtin/ prefix — bundled only
	if strings.HasPrefix(name, builtinPrefix) {
		pluginName := strings.TrimPrefix(name, builtinPrefix)
		return r.resolveFromBundled(pluginName)
	}

	// Explicit ./ prefix — local only
	if strings.HasPrefix(name, "./") {
		return r.resolveFromLocal(name)
	}

	// No prefix — legacy fallback: local first, then bundled, warn on shadow
	return r.resolveLegacy(name)
}

// resolveFromBundled resolves a plugin exclusively from the bundled FS.
func (r *Resolver) resolveFromBundled(name string) (*PluginDef, error) {
	if r.bundledFS == nil {
		return nil, fmt.Errorf("plugin %q: @builtin/ requested but no bundled plugins available", name)
	}
	bundledPath := filepath.Join(name, "plugin.yaml")
	data, err := fs.ReadFile(r.bundledFS, bundledPath)
	if err != nil {
		return nil, fmt.Errorf("plugin %q not found in bundled plugins", name)
	}
	return ParsePluginYAML(data)
}

// resolveFromLocal resolves a plugin from a local path relative to the project dir.
func (r *Resolver) resolveFromLocal(relPath string) (*PluginDef, error) {
	localDir := filepath.Join(r.projectDir, relPath)
	localPath := filepath.Join(localDir, "plugin.yaml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("plugin at %q not found (checked %s)", relPath, localPath)
	}
	p, err := ParsePluginYAML(data)
	if err != nil {
		return nil, err
	}
	p.BaseDir = localDir
	return p, nil
}

// resolveLegacy uses the original resolution order: local plugins/<name>/ → bundled.
// Emits a warning when a local plugin shadows a bundled one.
func (r *Resolver) resolveLegacy(name string) (*PluginDef, error) {
	// 1. Check local plugins/<name>/plugin.yaml
	localDir := filepath.Join(r.projectDir, "plugins", name)
	localPath := filepath.Join(localDir, "plugin.yaml")
	if data, err := os.ReadFile(localPath); err == nil {
		p, err := ParsePluginYAML(data)
		if err != nil {
			return nil, err
		}
		p.BaseDir = localDir

		// Warn if this shadows a bundled plugin
		if r.bundledFS != nil {
			bundledPath := filepath.Join(name, "plugin.yaml")
			if _, berr := fs.ReadFile(r.bundledFS, bundledPath); berr == nil {
				log.Printf("WARNING: local plugin %q shadows bundled plugin — use @builtin/%s to force bundled", name, name)
			}
		}
		return p, nil
	}

	// 2. Check bundled FS
	if r.bundledFS != nil {
		bundledPath := filepath.Join(name, "plugin.yaml")
		if data, err := fs.ReadFile(r.bundledFS, bundledPath); err == nil {
			return ParsePluginYAML(data)
		}
	}

	return nil, fmt.Errorf("plugin %q not found (checked: local plugins/%s/, bundled)", name, name)
}
