package generate

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	ListenPort     int
	HTTPListenPort int
	DNSPort        int
	MITMDomains    []string
	HTTPServices   []resolve.HTTPService
	Rewriters      []resolve.RewriterConfig
	PortForwards   []PortForward
}

// buildGatewayConfigBuilder constructs a GatewayConfigBuilder from the Generator state.
func (g *Generator) buildGatewayConfigBuilder() *GatewayConfigBuilder {
	mitmDomains, _ := g.splitDomainsByScheme()

	gcb := &GatewayConfigBuilder{
		ListenPort:     g.GatewaySpec.ListenPort,
		HTTPListenPort: g.GatewaySpec.HTTPListenPort,
		DNSPort:        g.GatewaySpec.DNSPort,
		MITMDomains:    mitmDomains,
		HTTPServices:   g.collectHTTPServices(),
		Rewriters:      g.collectRewriters(),
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

// splitDomainsByScheme separates MITM domain entries into TLS (no scheme or https://)
// and HTTP (http://) groups. HTTP entries are stripped of their scheme for the config.
func (g *Generator) splitDomainsByScheme() (mitmDomains, httpDomains []string) {
	for _, d := range g.collectMITMDomains() {
		if strings.HasPrefix(d, "http://") {
			// Strip scheme, keep host (and port if present)
			parsed, err := url.Parse(d)
			if err != nil {
				// Malformed — treat as MITM domain
				mitmDomains = append(mitmDomains, d)
				continue
			}
			httpDomains = append(httpDomains, parsed.Host)
		} else if strings.HasPrefix(d, "https://") {
			// Strip scheme for MITM list
			parsed, err := url.Parse(d)
			if err != nil {
				mitmDomains = append(mitmDomains, d)
				continue
			}
			mitmDomains = append(mitmDomains, parsed.Host)
		} else {
			// No scheme — default to MITM (TLS)
			mitmDomains = append(mitmDomains, d)
		}
	}
	return mitmDomains, httpDomains
}

// collectHTTPPortsFromDomains extracts port numbers from HTTP domain entries.
// For entries like "host.containers.internal:8000", returns ["8000"].
// Entries without an explicit port default to "80".
func (g *Generator) collectHTTPPortsFromDomains() []string {
	_, httpDomains := g.splitDomainsByScheme()
	seen := map[string]bool{}
	var ports []string
	for _, d := range httpDomains {
		_, port, err := net.SplitHostPort(d)
		if err != nil {
			// No port — default to 80
			port = "80"
		}
		if !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	return ports
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
