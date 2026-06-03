package generate

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*
var templateFS embed.FS

// parsedTemplates holds all pre-parsed templates.
var parsedTemplates *template.Template

func init() {
	var err error
	parsedTemplates, err = template.New("").
		Funcs(templateFuncs).
		ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		panic(fmt.Sprintf("generate: parsing templates: %v", err))
	}
}

// templateFuncs provides helper functions available in all templates.
var templateFuncs = template.FuncMap{
	"quote": func(s string) string {
		return fmt.Sprintf("%q", s)
	},
	"join": func(sep string, items []string) string {
		result := ""
		for i, item := range items {
			if i > 0 {
				result += sep
			}
			result += item
		}
		return result
	},
}

// renderTemplate executes a named template with the given data and returns the result.
func renderTemplate(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := parsedTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("rendering template %s: %w", name, err)
	}
	return buf.String(), nil
}
