package generate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/resolve"
)

// PortForward describes a port forwarding rule through the gateway to the agent.
type PortForward struct {
	HostPort      string
	ContainerPort string
	AgentName     string
}

// GatewayConfigBuilder holds data for rendering the gateway-config.yaml template.
type GatewayConfigBuilder struct {
	ListenPort   int
	DNSPort      int
	MITMDomains  []string
	Rewriters    []resolve.RewriterConfig
	PortForwards []PortForward
}

// buildGatewayConfigBuilder constructs a GatewayConfigBuilder from the Generator state.
func (g *Generator) buildGatewayConfigBuilder() *GatewayConfigBuilder {
	gcb := &GatewayConfigBuilder{
		ListenPort:  g.GatewaySpec.ListenPort,
		DNSPort:     g.GatewaySpec.DNSPort,
		MITMDomains: g.collectMITMDomains(),
		Rewriters:   g.collectRewriters(),
	}

	for _, p := range g.Runtime.Ports {
		hostPort, containerPort := parsePortMapping(p)
		gcb.PortForwards = append(gcb.PortForwards, PortForward{
			HostPort:      hostPort,
			ContainerPort: containerPort,
			AgentName:     g.Config.Name,
		})
	}

	return gcb
}

// writeGatewayConfig generates .build/gateway-config.yaml using a template.
func (g *Generator) writeGatewayConfig() error {
	gcb := g.buildGatewayConfigBuilder()

	content, err := renderTemplate("gateway-config.yaml.tmpl", gcb)
	if err != nil {
		return fmt.Errorf("rendering gateway config: %w", err)
	}

	path := filepath.Join(g.OutDir, "gateway-config.yaml")
	return os.WriteFile(path, []byte(content), 0644)
}
