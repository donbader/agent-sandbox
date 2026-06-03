package generate

import (
	"fmt"
	"os"
	"path/filepath"
)

// ComposeBuilder holds data for rendering docker-compose templates.
type ComposeBuilder struct {
	Variant        string   // "single", "gateway"
	AgentName      string
	GatewayName    string
	LogLevel       string
	Ports          []string
	Volumes        []string
	NamedVolumes   []string
	EnvVars        []string
	AgentEnv       []string
	HasMITM        bool
	GatewayCertDir string
}

// buildComposeBuilder constructs a ComposeBuilder from the Generator state.
func (g *Generator) buildComposeBuilder() *ComposeBuilder {
	cb := &ComposeBuilder{
		AgentName: g.Config.Name,
		LogLevel:  g.logLevel(),
		Ports:     g.Runtime.Ports,
		EnvVars:   g.mergedEnvVars(),
	}

	if g.Gateway {
		cb.Variant = "gateway"
		cb.GatewayName = g.Config.Name + "-gateway"
		cb.AgentEnv = g.collectAgentEnv()
		cb.HasMITM = g.hasMITMDomains()
		cb.GatewayCertDir = gatewayCertDir

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
