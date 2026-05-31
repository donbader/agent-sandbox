// Package generate produces .build/ artifacts from agent config and runtime data.
package generate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sandbox "github.com/donbader/agent-sandbox"
	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
)

// Generator produces build artifacts from config and resolved runtime.
type Generator struct {
	Config   *config.AgentConfig
	Runtime  *resolve.RuntimeConfig
	Features []*resolve.FeatureContributions
	Gateway  bool   // include gateway (transparent proxy)
	Bridge   bool   // include bridge (message relay)
	Dir      string // source directory (where agent.yaml lives)
	OutDir   string // output directory (.build/)
}

// Run generates all build artifacts.
func (g *Generator) Run() error {
	if err := os.MkdirAll(g.OutDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// Resolve built-in variables in feature contributions
	g.resolveFeatureBuiltins()

	if g.Gateway {
		if err := g.writeGatewaySource(); err != nil {
			return err
		}
		if err := g.writeGatewayConfig(); err != nil {
			return err
		}
	}

	// Generate CA if any feature requires MITM
	if g.hasMITMDomains() {
		if _, _, err := GenerateCA(g.OutDir); err != nil {
			return fmt.Errorf("generating CA: %w", err)
		}
	}

	if g.Bridge {
		if err := g.writeBridgeSource(); err != nil {
			return err
		}
		if err := g.writeBridgeConfig(); err != nil {
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
// Gateway or bridge always requires an entrypoint.
func (g *Generator) needsEntrypoint() bool {
	if g.Gateway || g.Bridge {
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

	// Stage 2: build bridge (if enabled)
	if g.Bridge {
		b.WriteString("# Stage 2: build bridge\n")
		b.WriteString("FROM node:22-slim AS bridge-build\n")
		b.WriteString("WORKDIR /src\n")
		b.WriteString("COPY bridge-src/package.json bridge-src/tsconfig.json ./\n")
		b.WriteString("RUN npm install\n")
		b.WriteString("COPY bridge-src/src/ ./src/\n")
		b.WriteString("RUN npm run build\n\n")
	}

	// Runtime stage
	b.WriteString(fmt.Sprintf("# Stage %d: runtime\n", g.runtimeStageNum()))
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

	// Install CA cert if MITM is enabled
	if g.hasMITMDomains() {
		b.WriteString("# Install sandbox CA certificate\n")
		b.WriteString("COPY certs/ca.crt /usr/local/share/ca-certificates/sandbox-ca.crt\n")
		b.WriteString("COPY certs/ca.crt /etc/gateway/ca.crt\n")
		b.WriteString("COPY certs/ca.key /etc/gateway/ca.key\n")
		b.WriteString("RUN update-ca-certificates\n\n")
	}

	// Copy bridge dist if enabled
	if g.Bridge {
		b.WriteString("# Install bridge\n")
		b.WriteString("COPY --from=bridge-build /src/dist/ /opt/bridge/dist/\n")
		b.WriteString("COPY --from=bridge-build /src/node_modules/ /opt/bridge/node_modules/\n")
		b.WriteString("COPY --from=bridge-build /src/package.json /opt/bridge/package.json\n")
		b.WriteString("COPY bridge-config.json /opt/bridge/config.json\n\n")
	}

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

	// Scan for env vars (from config references + feature contributions)
	envVars := g.scanEnvVars()
	featureEnvVars := g.collectFeatureEnvVars()
	for _, v := range featureEnvVars {
		found := false
		for _, existing := range envVars {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			envVars = append(envVars, v)
		}
	}
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
	featureEnvVars := g.collectFeatureEnvVars()
	for _, v := range featureEnvVars {
		found := false
		for _, existing := range envVars {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			envVars = append(envVars, v)
		}
	}
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
	if g.Bridge {
		b.WriteString("# Start bridge (spawns agent as child process)\n")
		b.WriteString("exec node /opt/bridge/dist/index.js\n")
	} else {
		b.WriteString("# Start agent\n")
		b.WriteString(fmt.Sprintf("exec su -c '%s' %s\n", strings.Join(g.Runtime.Cmd, " "), g.Runtime.User))
	}

	path := filepath.Join(g.OutDir, "entrypoint.sh")
	return os.WriteFile(path, []byte(b.String()), 0755)
}

// writeGatewayConfig generates .build/gateway-config.yaml.
func (g *Generator) writeGatewayConfig() error {
	var b strings.Builder
	b.WriteString("# Gateway configuration (auto-generated)\n")
	b.WriteString("listen: \":8443\"\n")
	b.WriteString("dns_listen: \":5353\"\n")

	// MITM configuration
	mitmDomains := g.collectMITMDomains()
	if len(mitmDomains) > 0 {
		b.WriteString("mitm_domains:\n")
		for _, d := range mitmDomains {
			b.WriteString(fmt.Sprintf("  - %s\n", d))
		}
		b.WriteString("ca_cert: /etc/gateway/ca.crt\n")
		b.WriteString("ca_key: /etc/gateway/ca.key\n")
	}

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

	// Also collect EnvVars from feature plugin contributions
	for _, f := range g.Features {
		for _, v := range f.EnvVars {
			if !seen[v] {
				seen[v] = true
				vars = append(vars, v)
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

// hasMITMDomains returns true if any feature contributes MITM domains.
func (g *Generator) hasMITMDomains() bool {
	for _, f := range g.Features {
		if len(f.MITMDomains) > 0 {
			return true
		}
	}
	return false
}

// collectMITMDomains gathers all MITM domains from features.
func (g *Generator) collectMITMDomains() []string {
	var domains []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, d := range f.MITMDomains {
			if !seen[d] {
				seen[d] = true
				domains = append(domains, d)
			}
		}
	}
	return domains
}

// collectFeatureEnvVars gathers all env vars declared by features.
func (g *Generator) collectFeatureEnvVars() []string {
	var vars []string
	seen := map[string]bool{}
	for _, f := range g.Features {
		for _, v := range f.EnvVars {
			if !seen[v] {
				seen[v] = true
				vars = append(vars, v)
			}
		}
	}
	return vars
}

// runtimeStageNum returns the stage number for the runtime stage in the Dockerfile.
func (g *Generator) runtimeStageNum() int {
	n := 2 // gateway is always stage 1
	if g.Bridge {
		n++ // bridge adds a stage
	}
	return n
}

// writeBridgeSource writes the embedded bridge source to .build/bridge-src/.
func (g *Generator) writeBridgeSource() error {
	destDir := filepath.Join(g.OutDir, "bridge-src")

	return fs.WalkDir(sandbox.BridgeSource, "bridge", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel("bridge", path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := sandbox.BridgeSource.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	})
}

// writeBridgeConfig generates .build/bridge-config.json.
func (g *Generator) writeBridgeConfig() error {
	// Find the bridge channel and allowed chat IDs
	channel := ""
	var allowedChatIDs []string

	for _, f := range g.Features {
		if f.BridgeChannel != "" {
			channel = f.BridgeChannel
			break
		}
	}

	// Extract allowed_chat_ids from config
	if g.Config.Features != nil {
		if tgCfg, ok := g.Config.Features["telegram"]; ok {
			if ids, ok := tgCfg["allowed_chat_ids"]; ok {
				if arr, ok := ids.([]any); ok {
					for _, v := range arr {
						if s, ok := v.(string); ok {
							allowedChatIDs = append(allowedChatIDs, s)
						}
					}
				}
			}
		}
	}

	// Build agent command: run as the agent user via su
	agentCmd := fmt.Sprintf("su -c '%s' %s", strings.Join(g.Runtime.Cmd, " "), g.Runtime.User)

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString(fmt.Sprintf("  \"channel\": %q,\n", channel))
	b.WriteString(fmt.Sprintf("  \"agent_cmd\": [\"sh\", \"-c\", %q]", agentCmd))
	if len(allowedChatIDs) > 0 {
		b.WriteString(",\n  \"allowed_chat_ids\": [")
		for i, id := range allowedChatIDs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("%q", id))
		}
		b.WriteString("]")
	}
	b.WriteString("\n}\n")

	path := filepath.Join(g.OutDir, "bridge-config.json")
	return os.WriteFile(path, []byte(b.String()), 0644)
}
