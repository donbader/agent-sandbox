package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// collectPluginRoutes iterates over resolved plugins, namespaces their declared routes,
// checks for conflicts, and extracts/resolves handler file paths.
// Returns route refs ready for code generation.
func (g *Generator) collectPluginRoutes(resolved map[string]*resolvedPlugin, buildDir string) ([]RouteRef, error) {
	var routes []RouteRef
	seen := make(map[string]string) // full path → plugin name (for conflict detection)

	for ref, rp := range resolved {
		for _, route := range rp.rendered.Gateway.Routes {
			if route.Path == "" || route.Handler == "" {
				return nil, fmt.Errorf("plugin %q: route entry must have both path and handler", ref)
			}

			// Namespace: /plugins/{plugin-name}{declared-path}
			namespacedPath := "/plugins/" + rp.def.Name + normalizePath(route.Path)

			// Conflict detection
			if owner, exists := seen[namespacedPath]; exists {
				return nil, fmt.Errorf("route conflict: path %q registered by both %q and %q", namespacedPath, owner, ref)
			}
			seen[namespacedPath] = ref

			// Resolve handler file path
			handlerPath, err := g.resolveRouteHandler(rp.def.Name, route.Handler, rp.def.BaseDir, buildDir)
			if err != nil {
				return nil, fmt.Errorf("plugin %q route %q: %w", ref, route.Path, err)
			}

			routes = append(routes, RouteRef{
				Path:       namespacedPath,
				Handler:    handlerPath,
				PluginName: rp.def.Name,
			})
		}
	}

	return routes, nil
}

// resolveRouteHandler resolves a route handler file path.
// For local plugins (baseDir != ""), it's relative to the plugin directory.
// For bundled plugins, it's extracted from the bundled FS.
func (g *Generator) resolveRouteHandler(pluginName, handler, baseDir, buildDir string) (string, error) {
	if baseDir != "" {
		return filepath.Join(baseDir, handler), nil
	}

	// Bundled plugin — extract handler from FS
	if g.bundledFS == nil {
		return "", fmt.Errorf("no bundled FS available to extract handler %q", handler)
	}
	return g.extractBundledMiddleware(pluginName, handler, buildDir)
}

// CopyRouteHandlers copies route handler .go files into the gateway build context.
// Each handler is a Go template rendered with path and options, and self-registers
// via init() calling gateway.RegisterRoute() — same pattern as custom middleware.
func CopyRouteHandlers(projectDir, outDir string, routes []RouteRef, opts map[string]any, publicURL string) error {
	if len(routes) == 0 {
		return nil
	}

	destDir := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create route handler dest dir: %w", err)
	}

	// Resolve ${VAR} references in options to actual env values
	resolved := resolveEnvVars(opts)

	for _, route := range routes {
		var srcPath string
		if filepath.IsAbs(route.Handler) {
			srcPath = route.Handler
		} else {
			srcPath = filepath.Join(projectDir, route.Handler)
		}
		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read route handler %s: %w", route.Handler, err)
		}

		// Template data includes options, the namespaced path, and public_url
		data := map[string]any{
			"options":    resolved,
			"path":       route.Path,
			"public_url": publicURL,
		}

		// Template-render the handler file
		rendered, err := renderRouteHandler(srcPath, string(content), data)
		if err != nil {
			return fmt.Errorf("render route handler %s: %w", route.Handler, err)
		}

		filename := fmt.Sprintf("route_%s_%s.go", sanitizeFilename(route.PluginName), sanitizeFilename(filepath.Base(route.Handler)))
		destFile := filepath.Join(destDir, filename)
		if err := os.WriteFile(destFile, []byte(rendered), 0644); err != nil {
			return fmt.Errorf("write route handler %s: %w", destFile, err)
		}
	}

	// Copy sibling .go files from handler directories that weren't explicitly listed.
	// These are shared helpers (e.g., pkce.go) that handlers depend on.
	copiedFiles := make(map[string]bool)
	for _, route := range routes {
		var srcPath string
		if filepath.IsAbs(route.Handler) {
			srcPath = route.Handler
		} else {
			srcPath = filepath.Join(projectDir, route.Handler)
		}
		copiedFiles[filepath.Base(srcPath)] = true
	}

	// Collect unique source directories from all route handlers
	seenDirs := make(map[string]bool)
	for _, route := range routes {
		var srcPath string
		if filepath.IsAbs(route.Handler) {
			srcPath = route.Handler
		} else {
			srcPath = filepath.Join(projectDir, route.Handler)
		}
		seenDirs[filepath.Dir(srcPath)] = true
	}

	for dir := range seenDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			if copiedFiles[entry.Name()] {
				continue
			}
			siblingPath := filepath.Join(dir, entry.Name())
			content, err := os.ReadFile(siblingPath)
			if err != nil {
				continue
			}
			// Render in case sibling uses templates (fast path skips if no {{ }})
			rendered, err := renderRouteHandler(siblingPath, string(content), map[string]any{
				"options":    resolved,
				"path":       "",
				"public_url": publicURL,
			})
			if err != nil {
				return fmt.Errorf("render sibling %s: %w", entry.Name(), err)
			}
			destFile := filepath.Join(destDir, entry.Name())
			if err := os.WriteFile(destFile, []byte(rendered), 0644); err != nil {
				return fmt.Errorf("write sibling %s: %w", entry.Name(), err)
			}
			copiedFiles[entry.Name()] = true
		}
	}

	return nil
}

// renderRouteHandler executes Go templates in route handler source code.
func renderRouteHandler(name, content string, data map[string]any) (string, error) {
	if !strings.Contains(content, "{{") {
		return content, nil
	}

	funcMap := template.FuncMap{
		"toJSON": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", fmt.Errorf("toJSON: %w", err)
			}
			return string(b), nil
		},
	}

	tmpl, err := template.New(name).Funcs(funcMap).Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// normalizePath ensures a path starts with / and has no trailing slash.
func normalizePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

