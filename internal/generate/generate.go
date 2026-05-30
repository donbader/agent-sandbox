// Package generate produces .build/ artifacts from agent config and runtime data.
package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Generator produces build artifacts from config and resolved runtime.
type Generator struct {
	Config   *config.AgentConfig
	Runtime  *resolve.RuntimeConfig
	Features []*resolve.FeatureContributions
	Gateway  bool   // include gateway (transparent proxy)
	Dir      string // source directory (where agent.yaml lives)
	OutDir   string // output directory (.build/)
}

// Run generates all build artifacts.
func (g *Generator) Run() error {
	if err := os.MkdirAll(g.OutDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if g.Gateway {
		if err := g.writeGatewaySource(); err != nil {
			return err
		}
		if err := g.writeGatewayConfig(); err != nil {
			return err
		}
	}

	if err := g.writeDockerfile(); err != nil {
		return err
	}

	if err := g.writeCompose(); err != nil {
		return err
	}

	if err := g.writeEnvExample(); err != nil {
		return err
	}

	if err := g.writeEntrypoint(); err != nil {
		return err
	}

	if err := g.copyHooks(); err != nil {
		return err
	}

	if err := g.copyHomeOverride(); err != nil {
		return err
	}

	return nil
}

// needsEntrypoint returns true when a custom entrypoint is needed.
// Gateway always requires an entrypoint (iptables + gateway start).
func (g *Generator) needsEntrypoint() bool {
	if g.Gateway {
		return true
	}
	for _, f := range g.Features {
		if len(f.EntrypointHooks) > 0 || f.HomeOverride != "" {
			return true
		}
	}
	return false
}

// writeDockerfile generates .build/Dockerfile.
func (g *Generator) writeDockerfile() error {
	var b strings.Builder

	if g.Gateway {
		g.writeMultiStageDockerfile(&b)
	} else {
		g.writeSingleStageDockerfile(&b)
	}

	path := filepath.Join(g.OutDir, "Dockerfile")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeMultiStageDockerfile produces a Dockerfile with gateway compilation stage.
func (g *Generator) writeMultiStageDockerfile(b *strings.Builder) {
	// Stage 1: compile gateway
	b.WriteString("# Stage 1: compile gateway\n")
	b.WriteString("FROM golang:1.24-alpine AS gateway-build\n")
	b.WriteString("WORKDIR /src\n")
	b.WriteString("COPY gateway-src/ .\n")
	b.WriteString("RUN go mod tidy\n")
	b.WriteString("RUN go build -o /gateway ./cmd/gateway/\n\n")

	// Stage 2: runtime
	b.WriteString("# Stage 2: runtime\n")
	b.WriteString(fmt.Sprintf("FROM %s\n\n", g.Runtime.BaseImage))

	// Install iptables (required for transparent proxy)
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends iptables && rm -rf /var/lib/apt/lists/*\n\n")

	// Create gateway user (runs the proxy, agent cannot kill it)
	b.WriteString("RUN useradd -r -s /bin/false gateway\n\n")

	// Create agent user
	b.WriteString(fmt.Sprintf("RUN useradd -m -s /bin/bash %s\n\n", g.Runtime.User))

	// Copy gateway binary
	b.WriteString("COPY --from=gateway-build /gateway /usr/local/bin/gateway\n\n")

	// Copy gateway config
	b.WriteString("COPY gateway-config.yaml /etc/gateway/config.yaml\n\n")

	// Runtime install commands
	for _, cmd := range g.Runtime.Install {
		b.WriteString(fmt.Sprintf("RUN %s\n", cmd))
	}
	if len(g.Runtime.Install) > 0 {
		b.WriteString("\n")
	}

	// Feature install commands
	g.writeFeatureCommands(b)

	// Copy home override, hooks, entrypoint
	g.writeCopyStatements(b)

	// Entrypoint (always present with gateway)
	b.WriteString("ENTRYPOINT [\"/opt/entrypoint.sh\"]\n")
}

// writeSingleStageDockerfile produces a simple Dockerfile without gateway.
func (g *Generator) writeSingleStageDockerfile(b *strings.Builder) {
	b.WriteString(fmt.Sprintf("FROM %s\n\n", g.Runtime.BaseImage))

	// Create agent user
	b.WriteString(fmt.Sprintf("RUN useradd -m -s /bin/bash %s\n\n", g.Runtime.User))

	// Runtime install commands
	for _, cmd := range g.Runtime.Install {
		b.WriteString(fmt.Sprintf("RUN %s\n", cmd))
	}
	if len(g.Runtime.Install) > 0 {
		b.WriteString("\n")
	}

	// Feature install commands
	g.writeFeatureCommands(b)

	// Copy home override, hooks, entrypoint
	g.writeCopyStatements(b)

	// Switch to agent user
	b.WriteString(fmt.Sprintf("USER %s\n", g.Runtime.User))
	b.WriteString(fmt.Sprintf("WORKDIR /home/%s\n\n", g.Runtime.User))

	// Entrypoint or CMD
	if g.needsEntrypoint() {
		b.WriteString("ENTRYPOINT [\"/opt/entrypoint.sh\"]\n")
	} else {
		if len(g.Runtime.Cmd) > 0 {
			quoted := make([]string, len(g.Runtime.Cmd))
			for i, c := range g.Runtime.Cmd {
				quoted[i] = fmt.Sprintf("%q", c)
			}
			b.WriteString(fmt.Sprintf("CMD [%s]\n", strings.Join(quoted, ", ")))
		}
	}
}

// writeFeatureCommands writes RUN commands from features.
func (g *Generator) writeFeatureCommands(b *strings.Builder) {
	for _, f := range g.Features {
		for _, cmd := range f.Commands {
			b.WriteString(fmt.Sprintf("RUN %s\n", cmd))
		}
	}
	if g.hasFeatureCommands() {
		b.WriteString("\n")
	}
}

// writeCopyStatements writes COPY statements for home override, hooks, and entrypoint.
func (g *Generator) writeCopyStatements(b *strings.Builder) {
	if g.hasHomeOverride() {
		b.WriteString("COPY home-override/ /opt/home-override/\n\n")
	}

	if g.hasHooks() {
		b.WriteString("COPY hooks/ /opt/hooks/\n")
		b.WriteString("RUN chmod +x /opt/hooks/*\n\n")
	}

	if g.needsEntrypoint() {
		b.WriteString("COPY entrypoint.sh /opt/entrypoint.sh\n")
		b.WriteString("RUN chmod +x /opt/entrypoint.sh\n\n")
	}
}

// writeCompose generates .build/docker-compose.yml.
func (g *Generator) writeCompose() error {
	var b strings.Builder

	b.WriteString("services:\n")
	b.WriteString(fmt.Sprintf("  %s:\n", g.Config.Name))
	b.WriteString("    build:\n")
	b.WriteString("      context: .\n")
	b.WriteString("      dockerfile: Dockerfile\n")
	b.WriteString(fmt.Sprintf("    container_name: %s\n", g.Config.Name))
	b.WriteString("    restart: unless-stopped\n")

	// Gateway requires NET_ADMIN for iptables
	if g.Gateway {
		b.WriteString("    cap_add:\n")
		b.WriteString("      - NET_ADMIN\n")
	}

	// Ports from runtime
	if len(g.Runtime.Ports) > 0 {
		b.WriteString("    ports:\n")
		for _, p := range g.Runtime.Ports {
			b.WriteString(fmt.Sprintf("      - %q\n", p))
		}
	}

	// Volumes from features
	volumes := g.collectVolumes()
	if len(volumes) > 0 {
		b.WriteString("    volumes:\n")
		for _, v := range volumes {
			b.WriteString(fmt.Sprintf("      - %s\n", v))
		}
	}

	// Scan for env vars
	envVars := g.scanEnvVars()
	if len(envVars) > 0 {
		b.WriteString("    environment:\n")
		for _, v := range envVars {
			b.WriteString(fmt.Sprintf("      - %s=${%s}\n", v, v))
		}
	}

	// Declare named volumes at top level
	namedVolumes := g.collectNamedVolumes(volumes)
	if len(namedVolumes) > 0 {
		b.WriteString("\nvolumes:\n")
		for _, v := range namedVolumes {
			b.WriteString(fmt.Sprintf("  %s:\n", v))
		}
	}

	path := filepath.Join(g.OutDir, "docker-compose.yml")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeEnvExample generates .build/.env.example.
func (g *Generator) writeEnvExample() error {
	envVars := g.scanEnvVars()
	if len(envVars) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("# Environment variables for agent-sandbox\n")
	b.WriteString("# Copy to .env and fill in values\n\n")
	for _, v := range envVars {
		b.WriteString(fmt.Sprintf("%s=\n", v))
	}

	path := filepath.Join(g.OutDir, ".env.example")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeEntrypoint generates .build/entrypoint.sh.
func (g *Generator) writeEntrypoint() error {
	if !g.needsEntrypoint() {
		return nil
	}

	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -e\n\n")

	if g.Gateway {
		// iptables setup: redirect all outbound traffic through gateway
		b.WriteString("# Setup iptables (must run as root)\n")
		b.WriteString("# Redirect TCP port 443 to gateway\n")
		b.WriteString("iptables -t nat -A OUTPUT -p tcp --dport 443 -m owner ! --uid-owner gateway -j REDIRECT --to-port 8443\n")
		b.WriteString("# Redirect DNS (UDP 53) to gateway resolver\n")
		b.WriteString("iptables -t nat -A OUTPUT -p udp --dport 53 -m owner ! --uid-owner gateway -j REDIRECT --to-port 5353\n")
		b.WriteString("# Drop all other UDP (except DNS handled above)\n")
		b.WriteString("iptables -A OUTPUT -p udp ! --dport 53 -m owner ! --uid-owner gateway -j DROP\n\n")

		// Start gateway as gateway user
		b.WriteString("# Start gateway (runs as gateway user)\n")
		b.WriteString("su -s /bin/sh -c '/usr/local/bin/gateway &' gateway\n")
		b.WriteString("sleep 0.5\n\n")
	}

	// Home override: copy files from staging to home
	if g.hasHomeOverride() {
		b.WriteString("# Copy home override files\n")
		b.WriteString(fmt.Sprintf("if [ -d /opt/home-override ]; then\n  cp -rT /opt/home-override /home/%s\n  chown -R %s:%s /home/%s\nfi\n\n",
			g.Runtime.User, g.Runtime.User, g.Runtime.User, g.Runtime.User))
	}

	// Run entrypoint hooks
	if g.hasHooks() {
		b.WriteString("# Run entrypoint hooks\n")
		for _, f := range g.Features {
			for _, hook := range f.EntrypointHooks {
				hookName := filepath.Base(hook)
				b.WriteString(fmt.Sprintf("su -c '/opt/hooks/%s' %s\n", hookName, g.Runtime.User))
			}
		}
		b.WriteString("\n")
	}

	// Execute the runtime CMD as agent user
	b.WriteString("# Start agent\n")
	b.WriteString(fmt.Sprintf("exec su -c '%s' %s\n", strings.Join(g.Runtime.Cmd, " "), g.Runtime.User))

	path := filepath.Join(g.OutDir, "entrypoint.sh")
	return os.WriteFile(path, []byte(b.String()), 0755)
}

// writeGatewayConfig generates .build/gateway-config.yaml.
func (g *Generator) writeGatewayConfig() error {
	var b strings.Builder
	b.WriteString("# Gateway configuration (auto-generated)\n")
	b.WriteString("listen: \":8443\"\n")
	b.WriteString("dns_listen: \":5353\"\n")

	path := filepath.Join(g.OutDir, "gateway-config.yaml")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// copyHooks copies entrypoint hook scripts to .build/hooks/.
func (g *Generator) copyHooks() error {
	if !g.hasHooks() {
		return nil
	}

	hooksDir := filepath.Join(g.OutDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return err
	}

	for _, f := range g.Features {
		for _, hook := range f.EntrypointHooks {
			srcPath := filepath.Join(g.Dir, hook)
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("reading hook %s: %w", hook, err)
			}
			destPath := filepath.Join(hooksDir, filepath.Base(hook))
			if err := os.WriteFile(destPath, data, 0755); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyHomeOverride copies the home override directory to .build/home-override/.
func (g *Generator) copyHomeOverride() error {
	if !g.hasHomeOverride() {
		return nil
	}

	for _, f := range g.Features {
		if f.HomeOverride == "" {
			continue
		}

		srcDir := filepath.Join(g.Dir, f.HomeOverride)
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			continue
		}

		destDir := filepath.Join(g.OutDir, "home-override")
		if err := copyDir(srcDir, destDir); err != nil {
			return fmt.Errorf("copying home override: %w", err)
		}
	}

	return nil
}

// Helper methods

func (g *Generator) hasFeatureCommands() bool {
	for _, f := range g.Features {
		if len(f.Commands) > 0 {
			return true
		}
	}
	return false
}

func (g *Generator) hasHooks() bool {
	for _, f := range g.Features {
		if len(f.EntrypointHooks) > 0 {
			return true
		}
	}
	return false
}

func (g *Generator) hasHomeOverride() bool {
	for _, f := range g.Features {
		if f.HomeOverride != "" {
			return true
		}
	}
	return false
}

func (g *Generator) collectVolumes() []string {
	var volumes []string
	for _, f := range g.Features {
		volumes = append(volumes, f.Volumes...)
	}
	return volumes
}

// collectNamedVolumes extracts volume names from "name:/path" format.
func (g *Generator) collectNamedVolumes(volumes []string) []string {
	var named []string
	for _, v := range volumes {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 && !strings.HasPrefix(parts[0], "/") && !strings.HasPrefix(parts[0], ".") {
			named = append(named, parts[0])
		}
	}
	return named
}

// scanEnvVars finds all ${VAR} references in the agent config.
var envVarPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

func (g *Generator) scanEnvVars() []string {
	seen := map[string]bool{}
	var vars []string

	for _, featureCfg := range g.Config.Features {
		for _, v := range featureCfg {
			if s, ok := v.(string); ok {
				matches := envVarPattern.FindAllStringSubmatch(s, -1)
				for _, m := range matches {
					if !seen[m[1]] {
						seen[m[1]] = true
						vars = append(vars, m[1])
					}
				}
			}
		}
	}

	return vars
}

// copyDir recursively copies a directory.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, info.Mode())
	})
}
