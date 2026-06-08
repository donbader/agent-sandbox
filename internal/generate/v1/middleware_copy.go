package v1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/donbader/agent-sandbox/internal/envvar"
)

// CopyCustomMiddleware copies custom middleware .go files into the gateway build context.
// Middleware files are treated as Go templates and rendered with the resolved plugin options
// and domain scope. This allows secrets and domain lists to be baked into the generated code.
func CopyCustomMiddleware(projectDir, outDir string, middlewareRefs []MiddlewareRef, opts map[string]any) error {
	if len(middlewareRefs) == 0 {
		return nil
	}

	destDir := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom")
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

// GenerateAuthHeaderMiddleware generates self-registering .go files for auth-header entries.
// Each entry becomes a compiled-in middleware that injects a header with a baked-in secret.
func GenerateAuthHeaderMiddleware(outDir string, entries []AuthHeaderEntry) error {
	if len(entries) == 0 {
		return nil
	}

	destDir := filepath.Join(outDir, "gateway-src", "core", "gateway", "middlewares", "custom")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create middleware dest dir: %w", err)
	}

	for i, entry := range entries {
		secret := os.Getenv(entry.EnvVar)
		if secret == "" {
			continue // Skip — env var not set at generate time
		}

		// Compute header value with substitutions
		headerValue := entry.ValueFormat
		if headerValue == "" {
			headerValue = "${value}"
		}
		headerValue = strings.ReplaceAll(headerValue, "${base64_basic}",
			base64.StdEncoding.EncodeToString([]byte("x-access-token:"+secret)))
		headerValue = strings.ReplaceAll(headerValue, "${value}", secret)

		src := authHeaderTemplate(entry.Domain, entry.Header, headerValue, secret, i)

		filename := fmt.Sprintf("auth_header_%s_%d.go", sanitizeFilename(entry.Domain), i)
		destFile := filepath.Join(destDir, filename)
		if err := os.WriteFile(destFile, []byte(src), 0644); err != nil {
			return fmt.Errorf("write auth-header middleware: %w", err)
		}
	}

	return nil
}

// authHeaderTemplate generates Go source for a self-registering auth-header middleware.
func authHeaderTemplate(domain, header, headerValue, secret string, idx int) string {
	var buf bytes.Buffer
	buf.WriteString("package custom\n\n")
	buf.WriteString("import \"github.com/donbader/agent-sandbox/core/sdk/gateway\"\n\n")
	buf.WriteString("func init() {\n")
	fmt.Fprintf(&buf, "\tsecret := %q\n", secret)
	buf.WriteString("\tgateway.RegisterSecret(secret)\n\n")
	buf.WriteString("\tgateway.RegisterMiddleware(gateway.MiddlewareDef{\n")
	fmt.Fprintf(&buf, "\t\tName:    \"auth-header:%s:%d\",\n", domain, idx)
	fmt.Fprintf(&buf, "\t\tDomains: []string{%q},\n", domain)
	buf.WriteString("\t\tFunc: func(ctx *gateway.MiddlewareContext) error {\n")
	fmt.Fprintf(&buf, "\t\t\tctx.Request.Header.Set(%q, %q)\n", header, headerValue)
	buf.WriteString("\t\t\treturn nil\n")
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
	buf.WriteString("}\n")
	return buf.String()
}

// sanitizeFilename replaces dots and special chars for use in filenames.
func sanitizeFilename(s string) string {
	r := strings.NewReplacer(".", "_", "/", "_", ":", "_")
	return r.Replace(s)
}

// renderMiddleware executes Go templates in middleware source code.
// If no template delimiters are found, returns content unchanged.
func renderMiddleware(name, content string, data map[string]any) (string, error) {
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

// resolveEnvVars resolves ${VAR} patterns in option values to actual environment values.
func resolveEnvVars(opts map[string]any) map[string]any {
	resolved := make(map[string]any, len(opts))
	for k, v := range opts {
		if s, ok := v.(string); ok {
			resolved[k] = envvar.Expand(s)
		} else {
			resolved[k] = v
		}
	}
	return resolved
}
