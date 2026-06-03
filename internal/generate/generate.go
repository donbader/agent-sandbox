// Package generate produces .build/ artifacts from agent config and runtime data.
package generate

import (
	"fmt"
	"os"

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

	if err := g.validateOutput(); err != nil {
		return err
	}

	return nil
}

