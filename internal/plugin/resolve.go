package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Resolver locates and loads plugin definitions.
type Resolver struct {
	projectDir string
	bundledFS  fs.FS
}

// NewResolver creates a resolver that checks local plugins/ dir first, then bundled FS.
func NewResolver(projectDir string, bundledFS fs.FS) *Resolver {
	return &Resolver{projectDir: projectDir, bundledFS: bundledFS}
}

// Resolve finds and parses a plugin by name. If source is non-empty, it's a remote plugin (future).
func (r *Resolver) Resolve(name string, source string) (*PluginDef, error) {
	// 1. Check local plugins/<name>/plugin.yaml
	localDir := filepath.Join(r.projectDir, "plugins", name)
	localPath := filepath.Join(localDir, "plugin.yaml")
	if data, err := os.ReadFile(localPath); err == nil {
		p, err := ParsePluginYAML(data)
		if err != nil {
			return nil, err
		}
		p.BaseDir = localDir
		return p, nil
	}

	// 2. Check bundled FS
	if r.bundledFS != nil {
		bundledPath := filepath.Join(name, "plugin.yaml")
		if data, err := fs.ReadFile(r.bundledFS, bundledPath); err == nil {
			return ParsePluginYAML(data)
		}
	}

	// 3. Remote (future — source field)
	if source != "" {
		return nil, fmt.Errorf("remote plugin resolution not yet implemented: %s", source)
	}

	return nil, fmt.Errorf("plugin %q not found (checked: local plugins/%s/, bundled)", name, name)
}
