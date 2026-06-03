package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PortMapping maps a host port to a container port for iptables DNAT rules.
type PortMapping struct {
	HostPort      string
	ContainerPort string
}

// EntrypointBuilder generates entrypoint shell scripts from templates.
type EntrypointBuilder struct {
	Variant           string // "agent", "gateway"
	HasGateway        bool
	HasMITM           bool
	HasHomeOverride   bool
	HasHooks          bool
	Hooks             []string // base filenames only
	User              string
	Ports             []PortMapping
	RuntimeCmd        string
	ChannelManager    bool
	CMEntryPoint      string
	GatewayListenPort int
	HTTPPorts         []string // additional ports to redirect to proxy (for HTTP services)
	CACertPath        string
	UserCommand       string // pre-computed command sequence for exec su -c
}

// buildUserCommand computes the full command string that runs inside exec su -c '...'.
func (b *EntrypointBuilder) buildUserCommand() {
	var parts []string
	if len(b.Hooks) > 0 {
		parts = append(parts, `echo "entrypoint: running hooks..."`)
		for _, hook := range b.Hooks {
			parts = append(parts, "/opt/hooks/"+hook)
		}
	}
	if b.ChannelManager {
		parts = append(parts, `echo "entrypoint: starting channel-manager..."`)
		parts = append(parts, "exec "+b.CMEntryPoint)
	} else {
		parts = append(parts, `echo "entrypoint: starting agent..."`)
		parts = append(parts, "exec "+b.RuntimeCmd)
	}
	b.UserCommand = strings.Join(parts, " && ")
}

// Render executes the appropriate template and returns the script content.
func (b *EntrypointBuilder) Render() (string, error) {
	b.buildUserCommand()

	tmplName := fmt.Sprintf("entrypoint.%s.tmpl", b.Variant)
	return renderTemplate(tmplName, b)
}

// writeEntrypoint generates entrypoint scripts using the EntrypointBuilder.
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
func (g *Generator) writeGatewayEntrypoint() error {
	b := &EntrypointBuilder{
		Variant:           "gateway",
		GatewayListenPort: g.GatewaySpec.ListenPort,
		HTTPPorts:         g.collectHTTPPorts(),
	}

	content, err := b.Render()
	if err != nil {
		return fmt.Errorf("rendering gateway entrypoint: %w", err)
	}

	path := filepath.Join(g.OutDir, "gateway-entrypoint.sh")
	return os.WriteFile(path, []byte(content), 0755)
}

// writeAgentEntrypoint generates .build/entrypoint.sh.
func (g *Generator) writeAgentEntrypoint() error {
	// Collect hooks (base filenames only)
	var hooks []string
	for _, f := range g.Features {
		for _, hook := range f.EntrypointHooks {
			hooks = append(hooks, filepath.Base(hook))
		}
	}

	// Collect port mappings
	var ports []PortMapping
	if g.Gateway && len(g.Runtime.Ports) > 0 {
		for _, p := range g.Runtime.Ports {
			host, container := parsePortMapping(p)
			ports = append(ports, PortMapping{HostPort: host, ContainerPort: container})
		}
	}

	b := &EntrypointBuilder{
		Variant:           "agent",
		HasGateway:        g.Gateway,
		HasMITM:           g.hasMITMDomains(),
		HasHomeOverride:   g.hasHomeOverride(),
		HasHooks:          len(hooks) > 0,
		Hooks:             hooks,
		User:              g.Runtime.User,
		Ports:             ports,
		RuntimeCmd:        strings.Join(g.Runtime.Cmd, " "),
		ChannelManager:    g.ChannelManager,
		CACertPath:        sandboxCACertPath,
		GatewayListenPort: g.GatewaySpec.ListenPort,
		HTTPPorts:         g.collectHTTPPorts(),
	}

	if g.ChannelManager {
		b.CMEntryPoint = g.ChannelManagerSpec.EntryPoint
	}

	content, err := b.Render()
	if err != nil {
		return fmt.Errorf("rendering agent entrypoint: %w", err)
	}

	path := filepath.Join(g.OutDir, "entrypoint.sh")
	return os.WriteFile(path, []byte(content), 0755)
}
