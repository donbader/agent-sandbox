package v1

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// CopyCustomMiddleware copies custom middleware .go files into the gateway build context.
// Middleware files are treated as Go templates and rendered with the resolved plugin options
// and domain scope. This allows secrets and domain lists to be baked into the generated code.
func CopyCustomMiddleware(projectDir, outDir string, middlewareRefs []MiddlewareRef, opts map[string]any) error {
	if len(middlewareRefs) == 0 {
		return nil
	}

	destDir := filepath.Join(outDir, "gateway-src", "middlewares", "custom")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create middleware dest dir: %w", err)
	}

	// Resolve ${VAR} references in options to actual env values
	resolved := resolveEnvVars(opts)

	for _, ref := range middlewareRefs {
		var srcPath string
		if filepath.IsAbs(ref.Path) {
			srcPath = ref.Path
		} else {
			srcPath = filepath.Join(projectDir, ref.Path)
		}
		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("read middleware %s: %w", ref.Path, err)
		}

		// Template data includes options and domains (as comma-separated string for embedding in Go string literals)
		data := map[string]any{
			"options":     resolved,
			"domains":     ref.Domains,
			"domainsList": strings.Join(ref.Domains, ","),
		}

		// Template-render the middleware file
		rendered, err := renderMiddleware(srcPath, string(content), data)
		if err != nil {
			return fmt.Errorf("render middleware %s: %w", ref.Path, err)
		}

		destFile := filepath.Join(destDir, filepath.Base(ref.Path))
		if err := os.WriteFile(destFile, []byte(rendered), 0644); err != nil {
			return fmt.Errorf("write middleware %s: %w", destFile, err)
		}
	}

	return nil
}

// renderMiddleware executes Go templates in middleware source code.
// If no template delimiters are found, returns content unchanged.
func renderMiddleware(name, content string, data map[string]any) (string, error) {
	if !strings.Contains(content, "{{") {
		return content, nil
	}

	tmpl, err := template.New(name).Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// resolveEnvVars resolves ${VAR} patterns in option values to actual environment values.
func resolveEnvVars(opts map[string]any) map[string]any {
	resolved := make(map[string]any, len(opts))
	for k, v := range opts {
		if s, ok := v.(string); ok {
			resolved[k] = expandEnvVar(s)
		} else {
			resolved[k] = v
		}
	}
	return resolved
}

// expandEnvVar replaces ${VAR} with the value of the environment variable.
func expandEnvVar(s string) string {
	start := strings.Index(s, "${")
	if start == -1 {
		return s
	}
	end := strings.Index(s[start:], "}")
	if end == -1 {
		return s
	}
	varName := s[start+2 : start+end]
	envVal := os.Getenv(varName)
	return s[:start] + envVal + s[start+end+1:]
}
