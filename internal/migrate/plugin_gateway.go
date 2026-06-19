// Package migrate handles conversion of legacy plugin formats.
package migrate

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// middlewareScriptRe matches `- script: "..." or - script: '...' or - script: ...` entries
// under a middlewares: key, for auto-correction to plain strings.
var middlewareScriptRe = regexp.MustCompile(`(?m)^(\s+)- script:\s*["']?([^"'\n]+?)["']?\s*$`)

// DetectLegacyGateway checks if a plugin.yaml uses the old gateway format.
// Returns true if the contributes section has gateway.services or top-level
// gateway.middlewares but no gateway.egress.
func DetectLegacyGateway(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return detectLegacyGatewayBytes(data)
}

// detectLegacyGatewayBytes checks raw YAML bytes for legacy gateway format.
func detectLegacyGatewayBytes(data []byte) (bool, error) {
	var raw struct {
		Contributes struct {
			Gateway struct {
				Services    yaml.Node `yaml:"services"`
				Middlewares yaml.Node `yaml:"middlewares"`
				Egress      yaml.Node `yaml:"egress"`
			} `yaml:"gateway"`
		} `yaml:"contributes"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		// If we can't parse (e.g. Go templates), fall back to string detection
		return detectLegacyGatewayString(string(data)), nil
	}

	hasServices := raw.Contributes.Gateway.Services.Kind != 0
	hasMiddlewares := raw.Contributes.Gateway.Middlewares.Kind != 0
	hasEgress := raw.Contributes.Gateway.Egress.Kind != 0

	if hasEgress {
		return false, nil
	}
	return hasServices || hasMiddlewares, nil
}

// detectLegacyGatewayString uses string matching for files with Go templates
// that prevent clean YAML parsing.
func detectLegacyGatewayString(content string) bool {
	inContributes := false
	inGateway := false
	hasServices := false
	hasMiddlewares := false
	hasEgress := false

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "contributes:" {
			inContributes = true
			continue
		}
		if inContributes && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" && !strings.HasPrefix(trimmed, "{{") {
			inContributes = false
			inGateway = false
		}
		if inContributes && (trimmed == "gateway:" || strings.HasPrefix(trimmed, "gateway:")) {
			inGateway = true
			continue
		}
		if inGateway {
			if trimmed == "services:" || strings.HasPrefix(trimmed, "services:") {
				hasServices = true
			}
			if trimmed == "middlewares:" || strings.HasPrefix(trimmed, "middlewares:") {
				hasMiddlewares = true
			}
			if trimmed == "egress:" || strings.HasPrefix(trimmed, "egress:") {
				hasEgress = true
			}
		}
	}

	if hasEgress {
		return false
	}
	return hasServices || hasMiddlewares
}

// LegacyService represents the old contributes.gateway.services entry.
type LegacyService struct {
	URL string `yaml:"url"`
}

// LegacyMiddleware represents the old contributes.gateway.middlewares entry.
type LegacyMiddleware struct {
	Script  string   `yaml:"script"`
	Domains []string `yaml:"domains"`
}

// TransformPlugin converts a legacy plugin.yaml to the new egress format.
// Returns before/after content strings for display and writing.
func TransformPlugin(path string) (before, after string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}

	before = string(data)
	after, err = transformPluginContent(before)
	if err != nil {
		return before, "", err
	}
	return before, after, nil
}

func transformPluginContent(content string) (string, error) {
	// Parse the contributes.gateway section to extract services and middlewares
	var raw struct {
		Contributes struct {
			Gateway struct {
				Services    []LegacyService    `yaml:"services"`
				Middlewares []LegacyMiddleware `yaml:"middlewares"`
			} `yaml:"gateway"`
		} `yaml:"contributes"`
	}
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return "", fmt.Errorf("parse plugin.yaml: %w", err)
	}

	services := raw.Contributes.Gateway.Services
	middlewares := raw.Contributes.Gateway.Middlewares

	if len(services) == 0 && len(middlewares) == 0 {
		return content, nil
	}

	// Build egress rules from services + middlewares
	egressYAML := buildEgressBlock(services, middlewares)

	// Replace the old gateway section content with new egress format
	result := replaceGatewayBlock(content, egressYAML)
	return result, nil
}

// buildEgressBlock constructs the YAML egress block from legacy services + middlewares.
func buildEgressBlock(services []LegacyService, middlewares []LegacyMiddleware) string {
	type egressEntry struct {
		host        string
		middlewares []LegacyMiddleware
	}

	// Map domains to their middlewares
	domainMiddlewares := make(map[string][]LegacyMiddleware)
	var globalMiddlewares []LegacyMiddleware

	for _, mw := range middlewares {
		if len(mw.Domains) == 0 {
			globalMiddlewares = append(globalMiddlewares, mw)
		} else {
			for _, d := range mw.Domains {
				domainMiddlewares[d] = append(domainMiddlewares[d], LegacyMiddleware{Script: mw.Script})
			}
		}
	}

	// Build entries from services
	var entries []egressEntry
	for _, svc := range services {
		host := extractHost(svc.URL)
		if host == "" {
			continue
		}
		entry := egressEntry{host: host}
		// Attach domain-specific middlewares
		if mws, ok := domainMiddlewares[host]; ok {
			entry.middlewares = append(entry.middlewares, mws...)
		}
		// Attach global middlewares
		entry.middlewares = append(entry.middlewares, globalMiddlewares...)
		entries = append(entries, entry)
	}

	// If there are middlewares with domains that don't match any service, create entries for them
	for domain, mws := range domainMiddlewares {
		found := false
		for _, e := range entries {
			if e.host == domain {
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, egressEntry{host: domain, middlewares: mws})
		}
	}

	// Format as YAML
	var sb strings.Builder
	sb.WriteString("    egress:\n")
	for _, entry := range entries {
		fmt.Fprintf(&sb, "      - hosts: [%q]\n", entry.host)
		if len(entry.middlewares) > 0 {
			sb.WriteString("        middlewares:\n")
			for _, mw := range entry.middlewares {
				fmt.Fprintf(&sb, "          - %q\n", mw.Script)
			}
		}
	}
	return sb.String()
}

// replaceGatewayBlock replaces the services/middlewares lines under contributes.gateway
// with the new egress block, preserving other fields like routes, namespaced_volumes, etc.
func replaceGatewayBlock(content string, egressBlock string) string {
	lines := strings.Split(content, "\n")
	var result []string

	inContributes := false
	inGateway := false
	inServices := false
	inMiddlewares := false
	gatewayIndent := 0
	egressInserted := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track contributes: section
		if trimmed == "contributes:" {
			inContributes = true
			result = append(result, line)
			continue
		}

		// Exited contributes
		if inContributes && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" && !strings.HasPrefix(trimmed, "{{") {
			inContributes = false
			inGateway = false
		}

		// Track gateway: under contributes
		if inContributes && (trimmed == "gateway:" || strings.HasPrefix(trimmed, "gateway:")) {
			indent := countLeadingSpaces(line)
			if indent > 0 { // must be indented under contributes
				inGateway = true
				gatewayIndent = indent
				result = append(result, line)
				continue
			}
		}

		if inGateway {
			lineIndent := countLeadingSpaces(line)

			// Detect services: or middlewares: at gateway child level
			if trimmed == "services:" || strings.HasPrefix(trimmed, "services:") {
				if lineIndent > gatewayIndent {
					inServices = true
					inMiddlewares = false
					// Insert egress block before skipping services
					if !egressInserted {
						result = append(result, egressBlock)
						egressInserted = true
					}
					continue
				}
			}
			if trimmed == "middlewares:" || strings.HasPrefix(trimmed, "middlewares:") {
				if lineIndent > gatewayIndent {
					inMiddlewares = true
					inServices = false
					// Insert egress block if not yet done
					if !egressInserted {
						result = append(result, egressBlock)
						egressInserted = true
					}
					continue
				}
			}

			// Skip lines that belong to services or middlewares blocks
			if inServices || inMiddlewares {
				if trimmed == "" {
					// Blank line might end the block or be within it
					// Look ahead to see if next non-blank line is still deeper
					if i+1 < len(lines) {
						nextTrimmed := strings.TrimSpace(lines[i+1])
						nextIndent := countLeadingSpaces(lines[i+1])
						if nextTrimmed == "" || nextIndent > gatewayIndent+2 {
							continue // still in the block
						}
					}
					inServices = false
					inMiddlewares = false
					continue
				}
				if lineIndent > gatewayIndent+2 {
					continue // still in services/middlewares content
				}
				// We've exited the services/middlewares block
				inServices = false
				inMiddlewares = false
				// Fall through to handle this line normally
			}
		}

		result = append(result, line)
	}

	// Remove trailing empty string that strings.Split may add
	output := strings.Join(result, "\n")
	// Clean up any double blank lines that may result
	for strings.Contains(output, "\n\n\n") {
		output = strings.ReplaceAll(output, "\n\n\n", "\n\n")
	}
	return output
}

func extractHost(rawURL string) string {
	if strings.Contains(rawURL, "://") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	if idx := strings.LastIndex(rawURL, ":"); idx > 0 {
		return rawURL[:idx]
	}
	return rawURL
}

func countLeadingSpaces(line string) int {
	for i, c := range line {
		if c != ' ' {
			return i
		}
	}
	return len(line)
}

// DetectMiddlewareScriptForm checks if a plugin.yaml uses the object form
// `- script: "./path.ts"` instead of the plain string form `- "./path.ts"`.
func DetectMiddlewareScriptForm(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return middlewareScriptRe.Match(data), nil
}

// FixMiddlewareScriptForm rewrites `- script: "./path.ts"` to `- "./path.ts"`.
// Returns true if the file was modified.
func FixMiddlewareScriptForm(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	content := string(data)
	fixed := middlewareScriptRe.ReplaceAllString(content, `${1}- "${2}"`)

	if fixed == content {
		return false, nil
	}

	if err := os.WriteFile(path, []byte(fixed), 0644); err != nil {
		return false, err
	}
	return true, nil
}
