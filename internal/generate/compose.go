package generate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/runtime"
)

// ComposeBuilder holds data for rendering docker-compose templates.
type ComposeBuilder struct {
	Variant          string   // "single", "gateway"
	AgentName        string
	GatewayName      string
	LogLevel         string
	Ports            []string // runtime ports (on gateway in gateway mode, on agent in single mode)
	AgentPorts       []string // feature-contributed ports (always on agent)
	Volumes          []string
	NamedVolumes     []string
	EnvVars          []string
	AgentEnv         []string
	HasMITM          bool
	GatewayCertDir   string
	ExternalNetworks []string
	Podman           bool
	Capabilities     []string // additional capabilities from features
}

// buildComposeBuilder constructs a ComposeBuilder from the Generator state.
func (g *Generator) buildComposeBuilder() *ComposeBuilder {
	rt := runtime.DetectOrDefault()

	// Merge runtime ports with feature-contributed ports
	runtimePorts := append([]string{}, g.Runtime.Ports...)
	featurePorts := g.collectFeaturePorts()

	cb := &ComposeBuilder{
		AgentName:    g.Config.Name,
		LogLevel:     g.logLevel(),
		Ports:        append(runtimePorts, featurePorts...),
		AgentPorts:   featurePorts,
		EnvVars:      g.mergedEnvVars(),
		Podman:       rt.Runtime == runtime.Podman,
		Capabilities: g.collectCapabilities(),
	}

	if g.Gateway {
		cb.Variant = "gateway"
		cb.GatewayName = g.Config.Name + "-gateway"
		cb.Ports = runtimePorts // gateway only gets runtime ports
		cb.AgentEnv = g.collectAgentEnv()
		cb.HasMITM = g.hasMITMDomains()
		cb.GatewayCertDir = gatewayCertDir
		cb.ExternalNetworks = g.collectExternalNetworks()

		// Agent volumes: feature volumes + shared-certs (when MITM)
		volumes := g.collectVolumes()
		if cb.HasMITM {
			volumes = append(volumes, fmt.Sprintf("shared-certs:%s:ro", filepath.Dir(sandboxCACertPath)))
		}
		cb.Volumes = volumes

		// Named volumes: from agent volumes + gateway's shared-certs
		allVolumes := volumes
		if cb.HasMITM {
			allVolumes = append([]string{"shared-certs:/shared/certs"}, allVolumes...)
		}
		cb.NamedVolumes = g.collectNamedVolumes(allVolumes)
	} else {
		cb.Variant = "single"
		cb.Volumes = g.collectVolumes()
		cb.NamedVolumes = g.collectNamedVolumes(cb.Volumes)
	}

	return cb
}

// writeCompose generates .build/docker-compose.yml using templates.
func (g *Generator) writeCompose() error {
	cb := g.buildComposeBuilder()

	tmplName := "docker-compose.single.tmpl"
	if cb.Variant == "gateway" {
		tmplName = "docker-compose.gateway.tmpl"
	}

	content, err := renderTemplate(tmplName, cb)
	if err != nil {
		return fmt.Errorf("rendering compose: %w", err)
	}

	path := filepath.Join(g.OutDir, "docker-compose.yml")
	return os.WriteFile(path, []byte(content), 0644)
}
