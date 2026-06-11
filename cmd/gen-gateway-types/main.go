// Command gen-gateway-types extracts @ts-method and @ts-prop annotations from Go source
// files and generates a TypeScript declaration file (gateway.d.ts).
//
// Usage:
//
//	go run ./cmd/gen-gateway-types -o core/gateway/types/gateway.d.ts
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	outputPath = flag.String("o", "core/gateway/types/gateway.d.ts", "output path for generated .d.ts")
	sourceDir  = flag.String("src", "core/gateway/internal/jsruntime", "directory to scan for annotations")
)

// Annotation represents a parsed @ts-method or @ts-prop comment.
type Annotation struct {
	Kind string // "method" or "prop"
	Path string // e.g. "ctx.request.setPath" or "gw.crypto.sha256"
	Sig  string // e.g. "(newPath: string): void" or ": readonly path: string"
	File string
	Line int
}

var (
	reMethod = regexp.MustCompile(`//\s*@ts-method\s+(\S+?)(\(.*\)):\s*(.+)`)
	reProp   = regexp.MustCompile(`//\s*@ts-prop\s+(\S+?):\s*(.+)`)
)

func main() {
	flag.Parse()

	annotations, err := scanDir(*sourceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error scanning: %v\n", err)
		os.Exit(1)
	}

	if len(annotations) == 0 {
		fmt.Fprintf(os.Stderr, "no @ts-method or @ts-prop annotations found in %s\n", *sourceDir)
		os.Exit(1)
	}

	output := generate(annotations)

	// Also copy to templates dir for embedding
	templatesPath := "internal/generate/templates/gateway.d.ts"

	for _, path := range []string{*outputPath, templatesPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating dir for %s: %v\n", path, err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, []byte(output), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
			os.Exit(1)
		}
	}

	fmt.Fprintf(os.Stderr, "generated %s and %s (%d annotations)\n", *outputPath, templatesPath, len(annotations))
}

func scanDir(dir string) ([]Annotation, error) {
	var annotations []Annotation

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fileAnnotations, err := scanFile(path)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", path, err)
		}
		annotations = append(annotations, fileAnnotations...)
	}

	return annotations, nil
}

func scanFile(path string) ([]Annotation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var annotations []Annotation
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if m := reMethod.FindStringSubmatch(line); m != nil {
			annotations = append(annotations, Annotation{
				Kind: "method",
				Path: m[1],
				Sig:  m[2] + ": " + m[3],
				File: path,
				Line: lineNum,
			})
		} else if m := reProp.FindStringSubmatch(line); m != nil {
			annotations = append(annotations, Annotation{
				Kind: "prop",
				Path: m[1],
				Sig:  m[2],
				File: path,
				Line: lineNum,
			})
		}
	}

	return annotations, scanner.Err()
}

// generate builds the .d.ts content from annotations.
func generate(annotations []Annotation) string {
	var b strings.Builder

	b.WriteString("/**\n")
	b.WriteString(" * Auto-generated gateway type definitions.\n")
	b.WriteString(" * DO NOT EDIT — regenerate with: go generate ./core/gateway/...\n")
	b.WriteString(" *\n")
	b.WriteString(" * Source annotations: @ts-method and @ts-prop in core/gateway/internal/jsruntime/\n")
	b.WriteString(" */\n\n")

	// Group by top-level object path
	type member struct {
		name string
		kind string
		sig  string
	}
	type iface struct {
		members []member
	}

	interfaces := map[string]*iface{}
	var ifaceOrder []string

	for _, a := range annotations {
		parts := strings.Split(a.Path, ".")
		if len(parts) < 2 {
			continue
		}

		// Determine interface name and member name
		var ifaceName, memberName string
		switch {
		case parts[0] == "ctx" && parts[1] == "request":
			ifaceName = "GatewayRequest"
			memberName = strings.Join(parts[2:], ".")
		case parts[0] == "ctx" && parts[1] == "response":
			ifaceName = "GatewayResponse"
			memberName = strings.Join(parts[2:], ".")
		case parts[0] == "ctx":
			ifaceName = "GatewayContext"
			memberName = strings.Join(parts[1:], ".")
		case parts[0] == "gw" && parts[1] == "crypto" && len(parts) > 3:
			// Nested: gw.crypto.base64.encode → GatewayCryptoBase64
			ifaceName = "GatewayCrypto" + capitalize(parts[2])
			memberName = parts[len(parts)-1]
		case parts[0] == "gw" && parts[1] == "crypto":
			ifaceName = "GatewayCrypto"
			memberName = parts[2]
		case parts[0] == "gw" && parts[1] == "fs":
			ifaceName = "GatewayFS"
			memberName = parts[2]
		case parts[0] == "gw" && parts[1] == "http":
			ifaceName = "GatewayHTTP"
			memberName = parts[2]
		case parts[0] == "gw" && parts[1] == "secrets":
			ifaceName = "GatewaySecrets"
			memberName = parts[2]
		case parts[0] == "gw" && parts[1] == "log":
			ifaceName = "GatewayLog"
			memberName = parts[2]
		default:
			continue
		}

		if _, ok := interfaces[ifaceName]; !ok {
			interfaces[ifaceName] = &iface{}
			ifaceOrder = append(ifaceOrder, ifaceName)
		}
		interfaces[ifaceName].members = append(interfaces[ifaceName].members, member{
			name: memberName,
			kind: a.Kind,
			sig:  a.Sig,
		})
	}

	// Sort for deterministic output
	sort.Strings(ifaceOrder)

	// Emit interfaces (skip composites that are emitted separately)
	skipInLoop := map[string]bool{"GatewayContext": true, "GatewayCrypto": true}
	for _, name := range ifaceOrder {
		if skipInLoop[name] {
			continue
		}
		ifc := interfaces[name]
		b.WriteString("interface " + name + " {\n")
		for _, m := range ifc.members {
			if m.kind == "prop" {
				b.WriteString("  " + m.sig + ";\n")
			} else {
				b.WriteString("  " + m.name + m.sig + ";\n")
			}
		}
		b.WriteString("}\n\n")
	}

	// Emit composite types
	b.WriteString("interface GatewayContext {\n")
	b.WriteString("  request: GatewayRequest;\n")
	b.WriteString("  response: GatewayResponse;\n")
	// Include direct ctx methods (env, abort)
	if ifc, ok := interfaces["GatewayContext"]; ok {
		for _, m := range ifc.members {
			b.WriteString("  " + m.name + m.sig + ";\n")
		}
	}
	b.WriteString("}\n\n")

	// Re-emit GatewayCrypto with nested sub-objects
	b.WriteString("interface GatewayCrypto {\n")
	if ifc, ok := interfaces["GatewayCrypto"]; ok {
		for _, m := range ifc.members {
			b.WriteString("  " + m.name + m.sig + ";\n")
		}
	}
	if _, ok := interfaces["GatewayCryptoBase64"]; ok {
		b.WriteString("  base64: GatewayCryptoBase64;\n")
	}
	if _, ok := interfaces["GatewayCryptoBase64url"]; ok {
		b.WriteString("  base64url: GatewayCryptoBase64url;\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("interface GatewayHostAPI {\n")
	b.WriteString("  crypto: GatewayCrypto;\n")
	b.WriteString("  fs: GatewayFS;\n")
	b.WriteString("  http: GatewayHTTP;\n")
	b.WriteString("  secrets: GatewaySecrets;\n")
	b.WriteString("  log: GatewayLog;\n")
	b.WriteString("}\n\n")

	b.WriteString("type PluginOptions = Record<string, any>;\n\n")
	b.WriteString("declare const gw: GatewayHostAPI;\n\n")
	b.WriteString("type MiddlewareHandler = (ctx: GatewayContext, options: PluginOptions) => void;\n")
	b.WriteString("type RouteHandler = (ctx: GatewayContext, options: PluginOptions) => void;\n")

	return b.String()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
