// Package templates provides embedded generation templates and a loader
// that can resolve templates from either the bundled embed.FS (for core_version: latest)
// or from an external core directory (for fetched versioned cores).
package templates

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed *.tmpl *.d.ts
var Embedded embed.FS

// Loader resolves templates from an fs.FS source.
type Loader struct {
	fs fs.FS
}

// NewEmbeddedLoader creates a loader that reads from the bundled templates.
func NewEmbeddedLoader() *Loader {
	return &Loader{fs: Embedded}
}

// NewDirLoader creates a loader that reads templates from a directory on disk.
// Used when core_version points to a fetched core tarball.
func NewDirLoader(dir string) *Loader {
	return &Loader{fs: os.DirFS(dir)}
}

// Load parses the named template file and returns it ready for execution.
func (l *Loader) Load(name string) (*template.Template, error) {
	data, err := fs.ReadFile(l.fs, name)
	if err != nil {
		return nil, fmt.Errorf("read template %q: %w", name, err)
	}
	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}
	return tmpl, nil
}

// LoadRaw reads the raw content of a template file without parsing.
// Useful for static templates like gateway.Dockerfile that have no variables.
func (l *Loader) LoadRaw(name string) (string, error) {
	data, err := fs.ReadFile(l.fs, name)
	if err != nil {
		return "", fmt.Errorf("read template %q: %w", name, err)
	}
	return string(data), nil
}

// FS returns the underlying filesystem for direct access if needed.
func (l *Loader) FS() fs.FS {
	return l.fs
}

// FindLoader returns a Loader for the appropriate source:
// - If coreDir has a "templates/" subdirectory, use that (fetched core).
// - Otherwise, fall back to the embedded templates.
func FindLoader(coreDir string) *Loader {
	if coreDir != "" {
		templatesDir := filepath.Join(coreDir, "templates")
		if info, err := os.Stat(templatesDir); err == nil && info.IsDir() {
			return NewDirLoader(templatesDir)
		}
	}
	return NewEmbeddedLoader()
}
