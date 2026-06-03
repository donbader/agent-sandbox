// Package generate produces .build/ artifacts from agent config and runtime data.
package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/resolve"
)

const (
	// sandboxCACertPath is where the gateway's CA certificate is mounted in the agent container.
	// Used by: docker-compose volume mount, entrypoint CA wait loop, NODE_EXTRA_CA_CERTS export.
	sandboxCACertPath = "/usr/local/share/ca-certificates/ca.crt"

	// gatewayCertDir is where the gateway writes the CA cert (shared volume source).
	gatewayCertDir = "/shared/certs"
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

	// Clean output directory to remove stale files from previous generates.
	if err := os.RemoveAll(g.OutDir); err != nil {
		return fmt.Errorf("cleaning output dir: %w", err)
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
		if err := g.writeCommandPlugins(); err != nil {
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
	_, _ = fmt.Fprintf(&b, "FROM %s AS builder\n", g.GatewaySpec.BuildImage)
	b.WriteString("WORKDIR /src\n")
	b.WriteString("COPY gateway-src/ .\n")
	_, _ = fmt.Fprintf(&b, "RUN go mod tidy && go build -o %s ./cmd/gateway/\n\n", g.GatewaySpec.BinaryPath)

	// Runtime stage: minimal alpine with gateway binary
	b.WriteString("FROM alpine:3.20\n")
	b.WriteString("RUN apk add --no-cache ca-certificates iptables\n")
	_, _ = fmt.Fprintf(&b, "COPY --from=builder %s /usr/local/bin/gateway\n", g.GatewaySpec.BinaryPath)
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
		_, _ = fmt.Fprintf(&b, "FROM %s AS channel-manager-build\n", g.ChannelManagerSpec.BuildImage)
		b.WriteString("WORKDIR /src\n")
		b.WriteString("COPY channel-manager-src/package.json channel-manager-src/tsconfig.json ./\n")
		_, _ = fmt.Fprintf(&b, "RUN %s\n", g.ChannelManagerSpec.InstallCmd)
		b.WriteString("COPY channel-manager-src/src/ ./src/\n")
		_, _ = fmt.Fprintf(&b, "RUN %s\n\n", g.ChannelManagerSpec.BuildCmd)
	}

	// Runtime stage
	_, _ = fmt.Fprintf(&b, "FROM %s\n", g.Runtime.BaseImage)
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends iproute2 iptables ca-certificates && rm -rf /var/lib/apt/lists/*\n")
	_, _ = fmt.Fprintf(&b, "RUN useradd -m -s /bin/bash %s\n", g.Runtime.User)

	// Pre-create plugin volume directories with correct ownership so Docker
	// initializes named volumes with the agent user (not root).
	volumePaths := g.collectVolumePaths()
	if len(volumePaths) > 0 {
		_, _ = fmt.Fprintf(&b, "RUN mkdir -p %s && chown -R %s:%s %s\n",
			strings.Join(volumePaths, " "), g.Runtime.User, g.Runtime.User, strings.Join(volumePaths, " "))
	}



	// Runtime install commands (before channel-manager COPY for better layer caching —
	// these change rarely, while channel-manager source changes on every build)
	for _, cmd := range g.Runtime.Install {
		_, _ = fmt.Fprintf(&b, "RUN %s\n", cmd)
	}

	// Feature install commands
	g.writeFeatureCommands(&b)

	// Copy channel-manager dist if enabled
	if g.ChannelManager {
		b.WriteString("# Install channel-manager\n")
		_, _ = fmt.Fprintf(&b, "COPY --from=channel-manager-build %s/ /opt/channel-manager/dist/\n", g.ChannelManagerSpec.DistDir)
		b.WriteString("COPY --from=channel-manager-build /src/node_modules/ /opt/channel-manager/node_modules/\n")
		b.WriteString("COPY --from=channel-manager-build /src/package.json /opt/channel-manager/package.json\n")
		b.WriteString("COPY channel-manager-config.json /opt/channel-manager/config.json\n")
	}

	// Copy home override, hooks, entrypoint
	g.writeCopyStatements(&b)

	_, _ = fmt.Fprintf(&b, "WORKDIR /home/%s\n", g.Runtime.User)
	b.WriteString("ENTRYPOINT [\"/opt/entrypoint.sh\"]\n")

	path := filepath.Join(g.OutDir, "Dockerfile.agent")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeSingleStageDockerfile produces a simple Dockerfile without gateway.
func (g *Generator) writeSingleStageDockerfile(b *strings.Builder) {
	_, _ = fmt.Fprintf(b, "FROM %s\n\n", g.Runtime.BaseImage)

	// Create agent user
	_, _ = fmt.Fprintf(b, "RUN useradd -m -s /bin/bash %s\n\n", g.Runtime.User)

	// Runtime install commands
	for _, cmd := range g.Runtime.Install {
		_, _ = fmt.Fprintf(b, "RUN %s\n", cmd)
	}
	if len(g.Runtime.Install) > 0 {
		b.WriteString("\n")
	}

	// Feature install commands
	g.writeFeatureCommands(b)

	// Copy home override, hooks, entrypoint
	g.writeCopyStatements(b)

	// Switch to agent user
	_, _ = fmt.Fprintf(b, "USER %s\n", g.Runtime.User)
	_, _ = fmt.Fprintf(b, "WORKDIR /home/%s\n\n", g.Runtime.User)

	// Entrypoint or CMD
	if g.needsEntrypoint() {
		b.WriteString("ENTRYPOINT [\"/opt/entrypoint.sh\"]\n")
	} else {
		if len(g.Runtime.Cmd) > 0 {
			quoted := make([]string, len(g.Runtime.Cmd))
			for i, c := range g.Runtime.Cmd {
				quoted[i] = fmt.Sprintf("%q", c)
			}
			_, _ = fmt.Fprintf(b, "CMD [%s]\n", strings.Join(quoted, ", "))
		}
	}
}

// writeFeatureCommands writes RUN commands from features.
func (g *Generator) writeFeatureCommands(b *strings.Builder) {
	for _, f := range g.Features {
		for _, cmd := range f.Commands {
			_, _ = fmt.Fprintf(b, "RUN %s\n", cmd)
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
	_, _ = fmt.Fprintf(&b, "  %s-gateway:\n", g.Config.Name)
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
	_, _ = fmt.Fprintf(&b, "      - LOG_LEVEL=%s\n", g.logLevel())
	for _, v := range envVars {
		_, _ = fmt.Fprintf(&b, "      - %s=${%s}\n", v, v)
	}

	// Expose runtime ports on gateway (forwarded to agent)
	if len(g.Runtime.Ports) > 0 {
		b.WriteString("    ports:\n")
		for _, p := range g.Runtime.Ports {
			_, _ = fmt.Fprintf(&b, "      - %q\n", p)
		}
	}

	b.WriteString("    restart: unless-stopped\n")

	// Shared certs volume + healthcheck (only when MITM is configured)
	if g.hasMITMDomains() {
		b.WriteString("    volumes:\n")
		b.WriteString("      - shared-certs:/shared/certs\n")
		b.WriteString("    healthcheck:\n")
		_, _ = fmt.Fprintf(&b, "      test: [\"CMD\", \"test\", \"-f\", \"%s/ca.crt\"]\n", gatewayCertDir)
		b.WriteString("      interval: 1s\n")
		b.WriteString("      timeout: 1s\n")
		b.WriteString("      retries: 10\n")
		b.WriteString("      start_period: 2s\n")
	}

	// Agent service: internal network only, no secrets
	_, _ = fmt.Fprintf(&b, "  %s:\n", g.Config.Name)
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
	_, _ = fmt.Fprintf(&b, "      - LOG_LEVEL=%s\n", g.logLevel())
	_, _ = fmt.Fprintf(&b, "      - GATEWAY_HOST=%s-gateway\n", g.Config.Name)
	for _, env := range g.collectAgentEnv() {
		_, _ = fmt.Fprintf(&b, "      - %s\n", env)
	}

	// depends_on: use healthcheck condition when MITM requires CA readiness
	if g.hasMITMDomains() {
		_, _ = fmt.Fprintf(&b, "    depends_on:\n      %s-gateway:\n        condition: service_healthy\n", g.Config.Name)
	} else {
		_, _ = fmt.Fprintf(&b, "    depends_on:\n      - %s-gateway\n", g.Config.Name)
	}

	volumes := g.collectVolumes()
	if g.hasMITMDomains() {
		volumes = append(volumes, fmt.Sprintf("shared-certs:%s:ro", filepath.Dir(sandboxCACertPath)))
	}
	if len(volumes) > 0 {
		b.WriteString("    volumes:\n")
		for _, v := range volumes {
			_, _ = fmt.Fprintf(&b, "      - %s\n", v)
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
			_, _ = fmt.Fprintf(&b, "  %s:\n", v)
		}
	}

	path := filepath.Join(g.OutDir, "docker-compose.yml")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeSingleCompose produces a docker-compose.yml with a single agent service.
func (g *Generator) writeSingleCompose() error {
	var b strings.Builder

	b.WriteString("services:\n")
	_, _ = fmt.Fprintf(&b, "  %s:\n", g.Config.Name)
	b.WriteString("    build:\n")
	b.WriteString("      context: .\n")
	b.WriteString("      dockerfile: Dockerfile\n")
	b.WriteString("    restart: unless-stopped\n")

	// Ports from runtime
	if len(g.Runtime.Ports) > 0 {
		b.WriteString("    ports:\n")
		for _, p := range g.Runtime.Ports {
			_, _ = fmt.Fprintf(&b, "      - %q\n", p)
		}
	}

	// Volumes from features
	volumes := g.collectVolumes()
	if len(volumes) > 0 {
		b.WriteString("    volumes:\n")
		for _, v := range volumes {
			_, _ = fmt.Fprintf(&b, "      - %s\n", v)
		}
	}

	// Environment variables
	envVars := g.mergedEnvVars()
	b.WriteString("    environment:\n")
	_, _ = fmt.Fprintf(&b, "      - LOG_LEVEL=%s\n", g.logLevel())
	for _, v := range envVars {
		_, _ = fmt.Fprintf(&b, "      - %s=${%s}\n", v, v)
	}

	// Named volumes at top level
	namedVolumes := g.collectNamedVolumes(volumes)
	if len(namedVolumes) > 0 {
		b.WriteString("\nvolumes:\n")
		for _, v := range namedVolumes {
			_, _ = fmt.Fprintf(&b, "  %s:\n", v)
		}
	}

	path := filepath.Join(g.OutDir, "docker-compose.yml")
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
	_, _ = fmt.Fprintf(&b, "iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port %d\n\n", g.GatewaySpec.ListenPort)
	b.WriteString("# Start gateway\n")
	b.WriteString("exec /usr/local/bin/gateway\n")
	path := filepath.Join(g.OutDir, "gateway-entrypoint.sh")
	return os.WriteFile(path, []byte(b.String()), 0755)
}

// writeAgentEntrypoint generates .build/entrypoint.sh for the agent container.
// When Gateway is true, sets up iptables DNAT rules to redirect traffic to the gateway container.
//
// Structure: all privileged operations run as root first (networking, CA trust, volume ownership),
// then a single user switch via 'exec su' runs the rest (home override, hooks, service) as the
// agent user.
func (g *Generator) writeAgentEntrypoint() error {
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -e\n\n")

	// === ROOT PHASE: privileged operations ===

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
				_, _ = fmt.Fprintf(&b, "iptables -t nat -A PREROUTING -p tcp --dport %s -j DNAT --to-destination 127.0.0.1:%s\n", containerPort, containerPort)
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
		b.WriteString("while [ ! -f " + sandboxCACertPath + " ]; do\n")
		b.WriteString("  sleep 0.1\n")
		b.WriteString("  elapsed=$((elapsed + 1))\n")
		b.WriteString("  if [ \"$elapsed\" -ge \"$((timeout * 10))\" ]; then\n")
		b.WriteString("    echo \"entrypoint: ERROR — CA certificate not available after ${timeout}s\" >&2\n")
		b.WriteString("    exit 1\n")
		b.WriteString("  fi\n")
		b.WriteString("done\n")
		b.WriteString("update-ca-certificates 2>/dev/null\n")
		// NODE_EXTRA_CA_CERTS is needed because Node.js bundles its own CA store.
		// Other runtimes (Go, Python ssl, curl) use the system store via update-ca-certificates.
		// Exporting this unconditionally is harmless if Node isn't present.
		_, _ = fmt.Fprintf(&b, "export NODE_EXTRA_CA_CERTS=%s\n", sandboxCACertPath)
		b.WriteString("echo \"entrypoint: CA certificate trusted\"\n\n")
	}

	// Home override: copy files from staging to home (requires root to read /opt/home-override)
	if g.hasHomeOverride() {
		b.WriteString("echo \"entrypoint: applying home override...\"\n")
		_, _ = fmt.Fprintf(&b, "if [ -d /opt/home-override ]; then\n  cp -rT /opt/home-override /home/%s\n  chown -R %s:%s /home/%s\nfi\n\n",
			g.Runtime.User, g.Runtime.User, g.Runtime.User, g.Runtime.User)
	}

	// === USER PHASE: everything below runs as the agent user ===
	// Build the unprivileged command sequence that will exec under 'su'.
	var userCmds strings.Builder

	// Run entrypoint hooks
	if g.hasHooks() {
		userCmds.WriteString("echo \"entrypoint: running hooks...\" && ")
		for _, f := range g.Features {
			for _, hook := range f.EntrypointHooks {
				hookName := filepath.Base(hook)
				_, _ = fmt.Fprintf(&userCmds, "/opt/hooks/%s && ", hookName)
			}
		}
	}

	// Start the service
	if g.ChannelManager {
		userCmds.WriteString("echo \"entrypoint: starting channel-manager...\" && ")
		_, _ = fmt.Fprintf(&userCmds, "exec %s", g.ChannelManagerSpec.EntryPoint)
	} else {
		userCmds.WriteString("echo \"entrypoint: starting agent...\" && ")
		_, _ = fmt.Fprintf(&userCmds, "exec %s", strings.Join(g.Runtime.Cmd, " "))
	}

	_, _ = fmt.Fprintf(&b, "exec su -c '%s' %s\n", userCmds.String(), g.Runtime.User)

	path := filepath.Join(g.OutDir, "entrypoint.sh")
	return os.WriteFile(path, []byte(b.String()), 0755)
}

// writeGatewayConfig generates .build/gateway-config.yaml.
func (g *Generator) writeGatewayConfig() error {
	var b strings.Builder
	b.WriteString("# Gateway configuration (auto-generated)\n")
	_, _ = fmt.Fprintf(&b, "listen: \":%d\"\n", g.GatewaySpec.ListenPort)
	_, _ = fmt.Fprintf(&b, "dns_listen: \":%d\"\n", g.GatewaySpec.DNSPort)

	// MITM configuration
	mitmDomains := g.collectMITMDomains()
	if len(mitmDomains) > 0 {
		b.WriteString("mitm_domains:\n")
		for _, d := range mitmDomains {
			_, _ = fmt.Fprintf(&b, "  - %s\n", d)
		}
	}

	// Rewriter configuration
	rewriters := g.collectRewriters()
	if len(rewriters) > 0 {
		b.WriteString("rewriters:\n")
		for _, rw := range rewriters {
			_, _ = fmt.Fprintf(&b, "  - type: %s\n", rw.Type)
			if len(rw.Domains) > 0 {
				b.WriteString("    domains:\n")
				for _, d := range rw.Domains {
					_, _ = fmt.Fprintf(&b, "      - %s\n", d)
				}
			}
			if rw.EnvVar != "" {
				_, _ = fmt.Fprintf(&b, "    env_var: %s\n", rw.EnvVar)
			}
			if rw.Header != "" {
				_, _ = fmt.Fprintf(&b, "    header: \"%s\"\n", rw.Header)
			}
			if rw.ValueFormat != "" {
				_, _ = fmt.Fprintf(&b, "    value_format: \"%s\"\n", rw.ValueFormat)
			}
			if rw.TokenFile != "" {
				_, _ = fmt.Fprintf(&b, "    token_file: \"%s\"\n", rw.TokenFile)
			}
		}
	}

	// Port forwards: expose runtime ports through gateway to agent
	if len(g.Runtime.Ports) > 0 {
		b.WriteString("port_forwards:\n")
		for _, p := range g.Runtime.Ports {
			hostPort, containerPort := parsePortMapping(p)
			_, _ = fmt.Fprintf(&b, "  - listen: \":%s\"\n", hostPort)
			_, _ = fmt.Fprintf(&b, "    target: \"%s:%s\"\n", g.Config.Name, containerPort)
		}
	}

	path := filepath.Join(g.OutDir, "gateway-config.yaml")
	return os.WriteFile(path, []byte(b.String()), 0644)
}


