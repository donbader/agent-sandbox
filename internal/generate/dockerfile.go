package generate

import (
	"os"
	"path/filepath"
)

// DockerfileBuilder holds pre-computed data for rendering Dockerfile templates.
type DockerfileBuilder struct {
	Variant         string   // "single", "gateway", "agent"
	BaseImage       string
	User            string
	Install         []string
	FeatureCmds     []string
	HasEntrypoint   bool
	HasHomeOverride bool
	HasHooks        bool
	Cmd             []string
	VolumePaths     []string

	// Gateway variant fields
	GatewayBuildImage string
	GatewayBinaryPath string
	HasMITM           bool

	// Agent variant fields (with gateway)
	ChannelManager bool
	CMBuildImage   string
	CMInstallCmd   string
	CMBuildCmd     string
	CMDistDir      string
}

// NewDockerfileBuilder creates a builder pre-populated from Generator state.
func NewDockerfileBuilder(g *Generator, variant string) *DockerfileBuilder {
	b := &DockerfileBuilder{
		Variant:         variant,
		BaseImage:       g.Runtime.BaseImage,
		User:            g.Runtime.User,
		Install:         g.Runtime.Install,
		HasEntrypoint:   g.needsEntrypoint(),
		HasHomeOverride: g.hasHomeOverride(),
		HasHooks:        g.hasHooks() || g.hasRootHooks(),
		Cmd:             g.Runtime.Cmd,
		VolumePaths:     g.collectVolumePaths(),
	}

	for _, f := range g.Features {
		b.FeatureCmds = append(b.FeatureCmds, f.Commands...)
	}

	switch variant {
	case "gateway":
		b.GatewayBuildImage = g.GatewaySpec.BuildImage
		b.GatewayBinaryPath = g.GatewaySpec.BinaryPath
		b.HasMITM = g.hasMITMDomains()
	case "agent":
		b.ChannelManager = g.ChannelManager
		if g.ChannelManager {
			b.CMBuildImage = g.ChannelManagerSpec.BuildImage
			b.CMInstallCmd = g.ChannelManagerSpec.InstallCmd
			b.CMBuildCmd = g.ChannelManagerSpec.BuildCmd
			b.CMDistDir = g.ChannelManagerSpec.DistDir
		}
	}

	return b
}

// Filename returns the output filename for this Dockerfile variant.
func (b *DockerfileBuilder) Filename() string {
	switch b.Variant {
	case "gateway":
		return "Dockerfile.gateway"
	case "agent":
		return "Dockerfile.agent"
	default:
		return "Dockerfile"
	}
}

// Render executes the appropriate template and returns the rendered content.
func (b *DockerfileBuilder) Render() (string, error) {
	tmplName := "Dockerfile." + b.Variant + ".tmpl"
	return renderTemplate(tmplName, b)
}

// writeDockerfile generates Dockerfile artifacts.
// When Gateway is true, produces Dockerfile.gateway and Dockerfile.agent.
// When Gateway is false, produces a single Dockerfile.
func (g *Generator) writeDockerfile() error {
	if g.Gateway {
		for _, variant := range []string{"gateway", "agent"} {
			b := NewDockerfileBuilder(g, variant)
			content, err := b.Render()
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(g.OutDir, b.Filename()), []byte(content), 0644); err != nil {
				return err
			}
		}
		return nil
	}
	b := NewDockerfileBuilder(g, "single")
	content, err := b.Render()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(g.OutDir, b.Filename()), []byte(content), 0644)
}
