// Package envvar provides utilities for parsing ${VAR} environment variable references.
package envvar

import (
	"os"
	"strings"
)

// Extract finds the first ${VAR_NAME} pattern in a string and returns the variable name.
// Returns "" if no pattern is found.
func Extract(s string) string {
	start := strings.Index(s, "${")
	if start == -1 {
		return ""
	}
	end := strings.Index(s[start+2:], "}")
	if end == -1 {
		return ""
	}
	return s[start+2 : start+2+end]
}

// Expand replaces the first ${VAR} in s with the value of that environment variable.
// If no ${VAR} pattern exists, returns s unchanged.
func Expand(s string) string {
	start := strings.Index(s, "${")
	if start == -1 {
		return s
	}
	end := strings.Index(s[start+2:], "}")
	if end == -1 {
		return s
	}
	varName := s[start+2 : start+2+end]
	envVal := os.Getenv(varName)
	return s[:start] + envVal + s[start+2+end+1:]
}

// ParseTemplate extracts the env var name and produces a value format template.
// The ${VAR} portion is replaced with ${value} in the returned format string.
//
// Examples:
//
//	"Bearer ${TOKEN}" → envVar="TOKEN", valueFormat="Bearer ${value}"
//	"${API_KEY}"      → envVar="API_KEY", valueFormat="${value}"
//	"static-value"    → envVar="", valueFormat=""
func ParseTemplate(value string) (envVar, valueFormat string) {
	start := strings.Index(value, "${")
	if start == -1 {
		return "", ""
	}
	end := strings.Index(value[start+2:], "}")
	if end == -1 {
		return "", ""
	}

	envVar = value[start+2 : start+2+end]
	valueFormat = value[:start] + "${value}" + value[start+2+end+1:]
	return envVar, valueFormat
}
