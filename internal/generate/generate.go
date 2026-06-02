// Package generate produces .build/ artifacts from agent config and runtime data.
package generate

import (
	"encoding/json"
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
	Config      *config.AgentConfig
	Runtime     *resolve.RuntimeConfig
	Features    []*resolve.FeatureContributions
	Gateway     bool        // include gateway (transparent proxy)
	ChannelManager bool        // include channel manager (message relay)
	SkipEnvExample bool       // skip per-agent .env.example (fleet mode writes one at root)
	GatewaySpec GatewaySpec // injected build spec
	ChannelManagerSpec  ChannelManagerSpec  // injected build spec
	Dir         string      // source directory (where agent.yaml lives)
	OutDir      string      // output directory (.build/)
}

// validate checks for misconfigurations before generating artifacts.
func (g *Generator) validate() error {
	if g.Config == nil {
		return fmt.Errorf("generator: Config is nil")
	}
	if g.Runtime == nil {
		return fmt.Errorf("generator: Runtime is nil")
	}
	if g.Runtime.BaseImage == "" {
		return fmt.Errorf("generator: runtime has no base_image")
	}
	if g.Dir == "" {
		return fmt.Errorf("generator: Dir (source directory) is empty")
	}
	if g.OutDir == "" {
		return fmt.Errorf("generator: OutDir (output directory) is empty")
	}

	if g.Gateway {
		if g.GatewaySpec.BuildImage == "" {
			return fmt.Errorf("generator: Gateway is enabled but GatewaySpec.BuildImage is empty")
		}
		if g.GatewaySpec.BinaryPath == "" {
			return fmt.Errorf("generator: Gateway is enabled but GatewaySpec.BinaryPath is empty")
		}
		if g.GatewaySpec.ListenPort == 0 {
			return fmt.Errorf("generator: Gateway is enabled but GatewaySpec.ListenPort is 0")
		}
		if g.GatewaySpec.DNSPort == 0 {
			return fmt.Errorf("generator: Gateway is enabled but GatewaySpec.DNSPort is 0")
		}
	}

	if g.ChannelManager {
		if g.ChannelManagerSpec.BuildImage == "" {
			return fmt.Errorf("generator: Bridge is enabled but ChannelManagerSpec.BuildImage is empty")
		}
		if g.ChannelManagerSpec.EntryPoint == "" {
			return fmt.Errorf("generator: Bridge is enabled but ChannelManagerSpec.EntryPoint is empty")
		}
	}

	// Check for features that need gateway but gateway is disabled
	for _, f := range g.Features {
		if len(f.MITMDomains) > 0 && !g.Gateway {
			return fmt.Errorf("feature %q requires MITM domains %v but gateway is disabled", f.Name, f.MITMDomains)
		}
	}

	// Check for features that need channel-manager but channel-manager is disabled
	for _, f := range g.Features {
		if f.ChannelName != "" && !g.ChannelManager {
			return fmt.Errorf("feature %q declares ChannelName %q but channel-manager is disabled", f.Name, f.ChannelName)
		}
	}

	// Check that channel-manager has at least one channel
	if g.ChannelManager {
		hasChannel := false
		for _, f := range g.Features {
			if f.ChannelName != "" {
				hasChannel = true
				break
			}
		}
		if !hasChannel {
			return fmt.Errorf("channel-manager is enabled but no feature declares a ChannelName")
		}
	}

	return nil
}

// Run generates all build artifacts.
func (g *Generator) Run() error {
	if err := g.validate(); err != nil {
		return err
	}

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



	if g.ChannelManager {
		if err := g.writeChannelManagerSource(); err != nil {
			return err
		}
		if err := g.writeChannelConfig(); err != nil {
			return err
		}
	}

	if err := g.writeDockerfile(); err != nil {
		return err
	}

	if err := g.writeCompose(); err != nil {
		return err
	}

	if !g.SkipEnvExample {
		if err := g.writeEnvExample(); err != nil {
			return err
		}
	}

	if err := g.writeSchema(); err != nil {
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
// Gateway or channel-manager always requires an entrypoint.
func (g *Generator) needsEntrypoint() bool {
	if g.Gateway || g.ChannelManager {
		return true
	}
	for _, f := range g.Features {
		if len(f.EntrypointHooks) > 0 || f.HomeOverride != "" {
			return true
		}
	}
	return false
}

// writeDockerfile generates Dockerfile artifacts.
// When Gateway is true, produces Dockerfile.gateway and Dockerfile.agent.
// When Gateway is false, produces a single Dockerfile.
func (g *Generator) writeDockerfile() error {
	if g.Gateway {
		if err := g.writeGatewayDockerfile(); err != nil {
			return err
		}
		return g.writeAgentDockerfile()
	}
	var b strings.Builder
	g.writeSingleStageDockerfile(&b)
	path := filepath.Join(g.OutDir, "Dockerfile")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeGatewayDockerfile produces Dockerfile.gateway: builds the gateway binary
// and packages it into a minimal alpine image.
func (g *Generator) writeGatewayDockerfile() error {
	var b strings.Builder

	// Build stage: compile gateway binary
	b.WriteString(fmt.Sprintf("FROM %s AS builder\n", g.GatewaySpec.BuildImage))
	b.WriteString("WORKDIR /src\n")
	b.WriteString("COPY gateway-src/ .\n")
	b.WriteString(fmt.Sprintf("RUN go mod tidy && go build -o %s ./cmd/gateway/\n\n", g.GatewaySpec.BinaryPath))

	// Runtime stage: minimal alpine with gateway binary
	b.WriteString("FROM alpine:3.20\n")
	b.WriteString("RUN apk add --no-cache ca-certificates iptables\n")
	b.WriteString(fmt.Sprintf("COPY --from=builder %s /usr/local/bin/gateway\n", g.GatewaySpec.BinaryPath))
	b.WriteString("COPY gateway-config.yaml /etc/gateway/config.yaml\n")

	if g.hasMITMDomains() {
		b.WriteString("RUN mkdir -p /shared/certs /etc/gateway/private && chmod 700 /etc/gateway/private\n")
	}

	b.WriteString("COPY gateway-entrypoint.sh /opt/entrypoint.sh\n")
	b.WriteString("RUN chmod +x /opt/entrypoint.sh\n")
	b.WriteString("ENTRYPOINT [\"/opt/entrypoint.sh\"]\n")

	path := filepath.Join(g.OutDir, "Dockerfile.gateway")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeAgentDockerfile produces Dockerfile.agent: builds the channel manager (if enabled)
// and packages the runtime with iptables for traffic redirection to the gateway container.
func (g *Generator) writeAgentDockerfile() error {
	var b strings.Builder

	// Bridge build stage (if enabled)
	if g.ChannelManager {
		b.WriteString(fmt.Sprintf("FROM %s AS channel-manager-build\n", g.ChannelManagerSpec.BuildImage))
		b.WriteString("WORKDIR /src\n")
		b.WriteString("COPY channel-manager-src/package.json channel-manager-src/tsconfig.json ./\n")
		b.WriteString(fmt.Sprintf("RUN %s\n", g.ChannelManagerSpec.InstallCmd))
		b.WriteString("COPY channel-manager-src/src/ ./src/\n")
		b.WriteString(fmt.Sprintf("RUN %s\n\n", g.ChannelManagerSpec.BuildCmd))
	}

	// Runtime stage
	b.WriteString(fmt.Sprintf("FROM %s\n", g.Runtime.BaseImage))
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends iproute2 iptables ca-certificates && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString(fmt.Sprintf("RUN useradd -m -s /bin/bash %s\n", g.Runtime.User))



	// Runtime install commands (before channel-manager COPY for better layer caching —
	// these change rarely, while channel-manager source changes on every build)
	for _, cmd := range g.Runtime.Install {
		b.WriteString(fmt.Sprintf("RUN %s\n", cmd))
	}

	// Feature install commands
	g.writeFeatureCommands(&b)

	// Copy channel-manager dist if enabled
	if g.ChannelManager {
		b.WriteString("# Install channel-manager\n")
		b.WriteString(fmt.Sprintf("COPY --from=channel-manager-build %s/ /opt/channel-manager/dist/\n", g.ChannelManagerSpec.DistDir))
		b.WriteString("COPY --from=channel-manager-build /src/node_modules/ /opt/channel-manager/node_modules/\n")
		b.WriteString("COPY --from=channel-manager-build /src/package.json /opt/channel-manager/package.json\n")
		b.WriteString("COPY channel-manager-config.json /opt/channel-manager/config.json\n")
	}

	// Copy home override, hooks, entrypoint
	g.writeCopyStatements(&b)

	b.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", g.Runtime.User))
	b.WriteString("ENTRYPOINT [\"/opt/entrypoint.sh\"]\n")

	path := filepath.Join(g.OutDir, "Dockerfile.agent")
	return os.WriteFile(path, []byte(b.String()), 0644)
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
// When Gateway is true, produces a two-service compose with an internal network.
func (g *Generator) writeCompose() error {
	if g.Gateway {
		return g.writeGatewayCompose()
	}
	return g.writeSingleCompose()
}

// writeGatewayCompose produces a docker-compose.yml with separate gateway and agent services.
// Secrets (env vars) go only to the gateway service; the agent has no access to them.
func (g *Generator) writeGatewayCompose() error {
	var b strings.Builder

	b.WriteString("services:\n")

	// Gateway service: internet access + secrets
	b.WriteString(fmt.Sprintf("  %s-gateway:\n", g.Config.Name))
	b.WriteString("    build:\n")
	b.WriteString("      context: .\n")
	b.WriteString("      dockerfile: Dockerfile.gateway\n")
	b.WriteString("    networks:\n")
	b.WriteString("      internal:\n")
	b.WriteString("      default:\n")
	b.WriteString("    cap_add:\n")
	b.WriteString("      - NET_ADMIN\n")
	b.WriteString("    sysctls:\n")
	b.WriteString("      - net.ipv4.ip_forward=1\n")

	envVars := g.mergedEnvVars()
	b.WriteString("    environment:\n")
	b.WriteString(fmt.Sprintf("      - LOG_LEVEL=%s\n", g.logLevel()))
	for _, v := range envVars {
		b.WriteString(fmt.Sprintf("      - %s=${%s}\n", v, v))
	}

	// Expose runtime ports on gateway (forwarded to agent)
	if len(g.Runtime.Ports) > 0 {
		b.WriteString("    ports:\n")
		for _, p := range g.Runtime.Ports {
			b.WriteString(fmt.Sprintf("      - %q\n", p))
		}
	}

	b.WriteString("    restart: unless-stopped\n")

	// Shared certs volume + healthcheck (only when MITM is configured)
	if g.hasMITMDomains() {
		b.WriteString("    volumes:\n")
		b.WriteString("      - shared-certs:/shared/certs\n")
		b.WriteString("    healthcheck:\n")
		b.WriteString("      test: [\"CMD\", \"test\", \"-f\", \"/shared/certs/ca.crt\"]\n")
		b.WriteString("      interval: 1s\n")
		b.WriteString("      timeout: 1s\n")
		b.WriteString("      retries: 10\n")
		b.WriteString("      start_period: 2s\n")
	}

	// Agent service: internal network only, no secrets
	b.WriteString(fmt.Sprintf("  %s:\n", g.Config.Name))
	b.WriteString("    build:\n")
	b.WriteString("      context: .\n")
	b.WriteString("      dockerfile: Dockerfile.agent\n")
	b.WriteString("    networks:\n")
	b.WriteString("      internal:\n")
	b.WriteString("    cap_add:\n")
	b.WriteString("      - NET_ADMIN\n")
	b.WriteString("    sysctls:\n")
	b.WriteString("      - net.ipv4.conf.all.route_localnet=1\n")
	b.WriteString("    environment:\n")
	b.WriteString(fmt.Sprintf("      - LOG_LEVEL=%s\n", g.logLevel()))
	b.WriteString(fmt.Sprintf("      - GATEWAY_HOST=%s-gateway\n", g.Config.Name))
	for _, env := range g.collectAgentEnv() {
		b.WriteString(fmt.Sprintf("      - %s\n", env))
	}

	// depends_on: use healthcheck condition when MITM requires CA readiness
	if g.hasMITMDomains() {
		b.WriteString(fmt.Sprintf("    depends_on:\n      %s-gateway:\n        condition: service_healthy\n", g.Config.Name))
	} else {
		b.WriteString(fmt.Sprintf("    depends_on:\n      - %s-gateway\n", g.Config.Name))
	}

	volumes := g.collectVolumes()
	if g.hasMITMDomains() {
		volumes = append(volumes, "shared-certs:/usr/local/share/ca-certificates:ro")
	}
	if len(volumes) > 0 {
		b.WriteString("    volumes:\n")
		for _, v := range volumes {
			b.WriteString(fmt.Sprintf("      - %s\n", v))
		}
	}
	b.WriteString("    restart: unless-stopped\n")

	// Internal network definition
	b.WriteString("\nnetworks:\n")
	b.WriteString("  internal:\n")
	b.WriteString("    internal: true\n")

	// Named volumes at top level
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

// writeSingleCompose produces a docker-compose.yml with a single agent service.
func (g *Generator) writeSingleCompose() error {
	var b strings.Builder

	b.WriteString("services:\n")
	b.WriteString(fmt.Sprintf("  %s:\n", g.Config.Name))
	b.WriteString("    build:\n")
	b.WriteString("      context: .\n")
	b.WriteString("      dockerfile: Dockerfile\n")
	b.WriteString("    restart: unless-stopped\n")

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

	// Environment variables
	envVars := g.mergedEnvVars()
	b.WriteString("    environment:\n")
	b.WriteString(fmt.Sprintf("      - LOG_LEVEL=%s\n", g.logLevel()))
	for _, v := range envVars {
		b.WriteString(fmt.Sprintf("      - %s=${%s}\n", v, v))
	}

	// Named volumes at top level
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

// writeEnvExample generates .env.example at the project root (next to agent.yaml).
func (g *Generator) writeEnvExample() error {
	envVars := g.mergedEnvVars()
	if len(envVars) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("# Environment variables for agent-sandbox\n")
	b.WriteString("# Copy to .env and fill in values\n\n")
	for _, v := range envVars {
		b.WriteString(fmt.Sprintf("%s=\n", v))
	}

	path := filepath.Join(g.Dir, ".env.example")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeEntrypoint generates entrypoint scripts.
// When Gateway is true, writes both gateway-entrypoint.sh and entrypoint.sh (agent).
// When Gateway is false, writes only entrypoint.sh if needed.
func (g *Generator) writeEntrypoint() error {
	if g.Gateway {
		if err := g.writeGatewayEntrypoint(); err != nil {
			return err
		}
		return g.writeAgentEntrypoint()
	}
	if !g.needsEntrypoint() {
		return nil
	}
	return g.writeAgentEntrypoint()
}

// writeGatewayEntrypoint generates .build/gateway-entrypoint.sh.
// Enables IP forwarding and sets up iptables PREROUTING to redirect port 443 to the proxy.
func (g *Generator) writeGatewayEntrypoint() error {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("set -e\n\n")
	b.WriteString("# Redirect incoming port 443 to proxy (port 8443)\n")
	b.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port %d\n\n", g.GatewaySpec.ListenPort))
	b.WriteString("# Start gateway\n")
	b.WriteString("exec /usr/local/bin/gateway\n")
	path := filepath.Join(g.OutDir, "gateway-entrypoint.sh")
	return os.WriteFile(path, []byte(b.String()), 0755)
}

// writeAgentEntrypoint generates .build/entrypoint.sh for the agent container.
// When Gateway is true, sets up iptables DNAT rules to redirect traffic to the gateway container.
func (g *Generator) writeAgentEntrypoint() error {
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -e\n\n")

	if g.Gateway {
		// Resolve gateway IP dynamically via Docker DNS (before iptables redirects DNS)
		b.WriteString("echo \"entrypoint: resolving gateway...\"\n")
		b.WriteString("GATEWAY_IP=$(getent hosts $GATEWAY_HOST | awk '{print $1}')\n")
		b.WriteString("if [ -z \"$GATEWAY_IP\" ]; then\n  echo \"entrypoint: ERROR — cannot resolve $GATEWAY_HOST\" >&2\n  exit 1\nfi\n")
		b.WriteString("echo \"entrypoint: gateway at $GATEWAY_IP\"\n\n")

		// Switch DNS to gateway resolver (Docker embedded DNS can't forward on internal network)
		b.WriteString("echo \"entrypoint: switching DNS to gateway...\"\n")
		b.WriteString("echo \"nameserver $GATEWAY_IP\" > /etc/resolv.conf\n\n")

		// ip route: set default route via gateway (all traffic goes through gateway)
		b.WriteString("echo \"entrypoint: setting default route via gateway...\"\n")
		b.WriteString("ip route replace default via $GATEWAY_IP\n\n")

		// Port forwards: redirect inbound ports to localhost (services bind to 127.0.0.1)
		if len(g.Runtime.Ports) > 0 {
			b.WriteString("echo \"entrypoint: setting up port forwards...\"\n")
			for _, p := range g.Runtime.Ports {
				_, containerPort := parsePortMapping(p)
				b.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p tcp --dport %s -j DNAT --to-destination 127.0.0.1:%s\n", containerPort, containerPort))
			}
			b.WriteString("\n")
		}
	}

	// Wait for CA cert and install trust (when MITM is configured)
	if g.hasMITMDomains() {
		b.WriteString("# Wait for sandbox CA certificate from gateway (shared volume)\n")
		b.WriteString("echo \"entrypoint: waiting for sandbox CA certificate...\"\n")
		b.WriteString("timeout=30\n")
		b.WriteString("elapsed=0\n")
		b.WriteString("while [ ! -f /usr/local/share/ca-certificates/ca.crt ]; do\n")
		b.WriteString("  sleep 0.1\n")
		b.WriteString("  elapsed=$((elapsed + 1))\n")
		b.WriteString("  if [ \"$elapsed\" -ge \"$((timeout * 10))\" ]; then\n")
		b.WriteString("    echo \"entrypoint: ERROR — CA certificate not available after ${timeout}s\" >&2\n")
		b.WriteString("    exit 1\n")
		b.WriteString("  fi\n")
		b.WriteString("done\n")
		b.WriteString("update-ca-certificates 2>/dev/null\n")
		b.WriteString("export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/ca.crt\n")
		b.WriteString("echo \"entrypoint: CA certificate trusted\"\n\n")
	}

	// Home override: copy files from staging to home
	if g.hasHomeOverride() {
		b.WriteString("echo \"entrypoint: applying home override...\"\n")
		b.WriteString(fmt.Sprintf("if [ -d /opt/home-override ]; then\n  cp -rT /opt/home-override /home/%s\n  chown -R %s:%s /home/%s\nfi\n\n",
			g.Runtime.User, g.Runtime.User, g.Runtime.User, g.Runtime.User))
	}

	// Run entrypoint hooks
	if g.hasHooks() {
		b.WriteString("echo \"entrypoint: running hooks...\"\n")
		for _, f := range g.Features {
			for _, hook := range f.EntrypointHooks {
				hookName := filepath.Base(hook)
				b.WriteString(fmt.Sprintf("su -c '/opt/hooks/%s' %s\n", hookName, g.Runtime.User))
			}
		}
		b.WriteString("\n")
	}

	// Execute the runtime CMD as agent user
	if g.ChannelManager {
		b.WriteString("echo \"entrypoint: starting channel-manager...\"\n")
		b.WriteString(fmt.Sprintf("exec %s\n", g.ChannelManagerSpec.EntryPoint))
	} else {
		b.WriteString("echo \"entrypoint: starting agent...\"\n")
		b.WriteString(fmt.Sprintf("exec su -c '%s' %s\n", strings.Join(g.Runtime.Cmd, " "), g.Runtime.User))
	}

	path := filepath.Join(g.OutDir, "entrypoint.sh")
	return os.WriteFile(path, []byte(b.String()), 0755)
}

// writeGatewayConfig generates .build/gateway-config.yaml.
func (g *Generator) writeGatewayConfig() error {
	var b strings.Builder
	b.WriteString("# Gateway configuration (auto-generated)\n")
	b.WriteString(fmt.Sprintf("listen: \":%d\"\n", g.GatewaySpec.ListenPort))
	b.WriteString(fmt.Sprintf("dns_listen: \":%d\"\n", g.GatewaySpec.DNSPort))

	// MITM configuration
	mitmDomains := g.collectMITMDomains()
	if len(mitmDomains) > 0 {
		b.WriteString("mitm_domains:\n")
		for _, d := range mitmDomains {
			b.WriteString(fmt.Sprintf("  - %s\n", d))
		}
	}

	// Rewriter configuration
	rewriters := g.collectRewriters()
	if len(rewriters) > 0 {
		b.WriteString("rewriters:\n")
		for _, rw := range rewriters {
			b.WriteString(fmt.Sprintf("  - type: %s\n", rw.Type))
			if len(rw.Domains) > 0 {
				b.WriteString("    domains:\n")
				for _, d := range rw.Domains {
					b.WriteString(fmt.Sprintf("      - %s\n", d))
				}
			}
			if rw.EnvVar != "" {
				b.WriteString(fmt.Sprintf("    env_var: %s\n", rw.EnvVar))
			}
			if rw.Header != "" {
				b.WriteString(fmt.Sprintf("    header: \"%s\"\n", rw.Header))
			}
			if rw.ValueFormat != "" {
				b.WriteString(fmt.Sprintf("    value_format: \"%s\"\n", rw.ValueFormat))
			}
		}
	}

	// Port forwards: expose runtime ports through gateway to agent
	if len(g.Runtime.Ports) > 0 {
		b.WriteString("port_forwards:\n")
		for _, p := range g.Runtime.Ports {
			hostPort, containerPort := parsePortMapping(p)
			b.WriteString(fmt.Sprintf("  - listen: \":%s\"\n", hostPort))
			b.WriteString(fmt.Sprintf("    target: \"%s:%s\"\n", g.Config.Name, containerPort))
		}
	}

	path := filepath.Join(g.OutDir, "gateway-config.yaml")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// collectRewriters gathers all rewriter configs from features.
func (g *Generator) collectRewriters() []resolve.RewriterConfig {
	var rewriters []resolve.RewriterConfig
	for _, f := range g.Features {
		rewriters = append(rewriters, f.Rewriters...)
	}
	return rewriters
}

// collectAgentEnv gathers agent-side environment variables from features.
// These are dummy/non-secret values set in the agent container (e.g., GH_TOKEN=dummy).
func (g *Generator) collectAgentEnv() []string {
	var envs []string
	for _, f := range g.Features {
		envs = append(envs, f.AgentEnv...)
	}
	return envs
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

// logLevel returns the configured log level, defaulting to "info".
func (g *Generator) logLevel() string {
	if g.Config.LogLevel != "" {
		return g.Config.LogLevel
	}
	return "info"
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

// scanEnvVars finds all ${VAR} references in the agent config (recursively).
var envVarPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// ScanConfigEnvVars finds all ${VAR} references in the given feature entries.
// Exported for use by fleet-level .env.example generation.
func ScanConfigEnvVars(features []config.FeatureEntry) []string {
	sources := map[string][]string{}
	var order []string

	for i, entry := range features {
		source := fmt.Sprintf("features[%d]", i)
		if entry.Name != "" {
			source = entry.Name
		}
		for key, v := range entry.Config {
			scanValueWithSource(v, envVarPattern, sources, &order,
				fmt.Sprintf("feature:%s.%s", source, key))
		}
	}

	return order
}

func (g *Generator) scanEnvVars() []string {
	sources := map[string][]string{} // var name → list of sources
	var order []string               // preserve insertion order

	// Scan config for ${VAR} references
	for i, entry := range g.Config.Features {
		source := fmt.Sprintf("features[%d]", i)
		if entry.Name != "" {
			source = entry.Name
		}
		for key, v := range entry.Config {
			scanValueWithSource(v, envVarPattern, sources, &order,
				fmt.Sprintf("feature:%s.%s", source, key))
		}
	}

	// Warn about env vars defined in multiple places
	for _, name := range order {
		if len(sources[name]) > 1 {
			fmt.Fprintf(os.Stderr, "warning: env var %s defined in multiple places: %s\n",
				name, strings.Join(sources[name], ", "))
		}
	}

	return order
}

// scanValueWithSource recursively walks a value and extracts ${VAR} references,
// tracking the source location for conflict warnings.
func scanValueWithSource(v any, pattern *regexp.Regexp, sources map[string][]string, order *[]string, source string) {
	switch val := v.(type) {
	case string:
		matches := pattern.FindAllStringSubmatch(val, -1)
		for _, m := range matches {
			name := m[1]
			if _, exists := sources[name]; !exists {
				*order = append(*order, name)
			}
			sources[name] = append(sources[name], source)
		}
	case []any:
		for _, item := range val {
			scanValueWithSource(item, pattern, sources, order, source)
		}
	case map[string]any:
		for k, item := range val {
			scanValueWithSource(item, pattern, sources, order, source+"."+k)
		}
	}
}


// parsePortMapping splits a "host:container" port string.
// If no colon, both host and container are the same port.
func parsePortMapping(port string) (hostPort, containerPort string) {
	parts := strings.SplitN(port, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return port, port
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

// mergedEnvVars returns all env vars from config ${VAR} references, deduplicated and in stable order.
func (g *Generator) mergedEnvVars() []string {
	return g.scanEnvVars()
}

// writeChannelManagerSource writes the embedded channel-manager source to .build/channel-manager-src/,
// copies plugin channel implementations, and generates the channel registry.
func (g *Generator) writeChannelManagerSource() error {
	destDir := filepath.Join(g.OutDir, "channel-manager-src")

	// 1. Copy channel-manager core source
	err := fs.WalkDir(sandbox.ChannelManagerSource, "channel-manager", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel("channel-manager", path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		data, err := sandbox.ChannelManagerSource.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destPath, data, 0644)
	})
	if err != nil {
		return err
	}

	// 2. Copy plugin channel implementations and build registry
	channelDir := filepath.Join(destDir, "src", "channel")
	if err := os.MkdirAll(channelDir, 0755); err != nil {
		return err
	}

	var channels []string
	for _, f := range g.Features {
		if f.ChannelName == "" {
			continue
		}
		name := f.ChannelName
		channelRoot := fmt.Sprintf("internal/plugins/%s/channel", name)

		// Copy all .ts files from plugin's channel/ directory to channel-manager-src/src/channel/
		// channel.ts → src/channel/<name>.ts (special case)
		// other files placed as siblings: delivery/foo.ts → src/channel/delivery/foo.ts
		// Note: if multiple plugins contribute same-named files, last one wins.
		err := fs.WalkDir(sandbox.CorePlugins, channelRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".ts") {
				return nil
			}
			// Skip test files
			if strings.HasSuffix(path, ".test.ts") {
				return nil
			}

			data, err := sandbox.CorePlugins.ReadFile(path)
			if err != nil {
				return err
			}

			// Determine destination path
			relPath, relErr := filepath.Rel(channelRoot, path)
			if relErr != nil {
				return fmt.Errorf("computing relative path for %s: %w", path, relErr)
			}
			var destPath string
			if relPath == "channel.ts" {
				// channel.ts → src/channel/<name>.ts
				destPath = filepath.Join(channelDir, name+".ts")
			} else {
				// other files → src/channel/<relPath> (sibling of channel file)
				destPath = filepath.Join(channelDir, relPath)
			}

			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			return os.WriteFile(destPath, data, 0644)
		})
		if err != nil {
			return fmt.Errorf("plugin %q: copying channel files: %w", name, err)
		}
		channels = append(channels, name)
	}

	// 3. Generate channels.gen.ts registry
	return g.writeChannelRegistry(channelDir, channels)
}

// writeChannelRegistry generates src/channel/channels.gen.ts with imports for all plugin channels.
func (g *Generator) writeChannelRegistry(channelDir string, channels []string) error {
	var b strings.Builder
	b.WriteString("// Auto-generated by agent-sandbox generate. Do not edit.\n")
	b.WriteString("import type { Channel, ChannelFactory } from \"./types.js\";\n")
	b.WriteString("import type { AcpAgent } from \"../acp-client.js\";\n\n")

	for _, name := range channels {
		camelName := "create" + strings.ToUpper(name[:1]) + name[1:] + "Channel"
		b.WriteString(fmt.Sprintf("import %s from \"./%s.js\";\n", camelName, name))
	}

	b.WriteString("\nexport const channels: Record<string, ChannelFactory> = {\n")
	for _, name := range channels {
		camelName := "create" + strings.ToUpper(name[:1]) + name[1:] + "Channel"
		b.WriteString(fmt.Sprintf("  %s: %s,\n", name, camelName))
	}
	b.WriteString("};\n")

	b.WriteString("\nexport function loadChannel(\n")
	b.WriteString("  name: string,\n")
	b.WriteString("  config: Record<string, unknown>,\n")
	b.WriteString("  agent: AcpAgent\n")
	b.WriteString("): Channel {\n")
	b.WriteString("  const factory = channels[name];\n")
	b.WriteString("  if (!factory) {\n")
	b.WriteString("    throw new Error(`Unknown channel: ${name}. Available: ${Object.keys(channels).join(\", \") || \"none\"}`);\n")
	b.WriteString("  }\n")
	b.WriteString("  return factory(config, agent);\n")
	b.WriteString("}\n")

	path := filepath.Join(channelDir, "channels.gen.ts")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeChannelConfig generates .build/channel-manager-config.json.
func (g *Generator) writeChannelConfig() error {
	channel := ""
	for _, f := range g.Features {
		if f.ChannelName != "" {
			channel = f.ChannelName
			break
		}
	}

	// Build ACP command: run as the agent user via su
	acpCmd := fmt.Sprintf("su -c '%s' %s", strings.Join(g.Runtime.AcpCmd, " "), g.Runtime.User)

	// Build config map for JSON marshaling
	config := map[string]any{
		"channel":     channel,
		"acp_command": []string{"sh", "-c", acpCmd},
	}

	// Pass plugin-specific config to channel-manager (generic — no plugin knowledge here)
	for _, f := range g.Features {
		for k, v := range f.ChannelConfig {
			config[k] = v
		}
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling channel-manager config: %w", err)
	}

	path := filepath.Join(g.OutDir, "channel-manager-config.json")
	return os.WriteFile(path, append(data, '\n'), 0644)
}
