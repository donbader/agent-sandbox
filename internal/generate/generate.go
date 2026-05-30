// Package generate produces .build/ artifacts from agent config and plugin contributions.
package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/sdk"
)

// Generator produces build artifacts from config and plugin contributions.
type Generator struct {
	Config  *config.AgentConfig
	Runtime sdk.RuntimePlugin
	Dir     string // source directory (where agent.yaml lives)
	OutDir  string // output directory (.build/)
}

// Run generates all build artifacts.
func (g *Generator) Run() error {
	ctx := sdk.ContributeContext{
		AgentName: g.Config.Name,
		Config:    nil,
		FleetDir:  g.Dir,
	}

	contrib, err := g.Runtime.Contribute(ctx)
	if err != nil {
		return fmt.Errorf("runtime plugin %q: %w", g.Runtime.Name(), err)
	}

	if err := os.MkdirAll(g.OutDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if err := g.writeDockerfile(contrib); err != nil {
		return err
	}

	if err := g.writeCompose(contrib); err != nil {
		return err
	}

	if err := g.writeEnvExample(); err != nil {
		return err
	}

	return nil
}

// writeDockerfile generates .build/Dockerfile.
func (g *Generator) writeDockerfile(contrib *sdk.RuntimeContributions) error {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("FROM %s\n\n", contrib.BaseImage))

	// Create agent user
	b.WriteString("RUN useradd -m -s /bin/bash agent\n\n")

	// Runtime commands (install agent CLI)
	for _, cmd := range contrib.Commands {
		b.WriteString(fmt.Sprintf("RUN %s\n", cmd))
	}
	b.WriteString("\n")

	// Switch to agent user
	b.WriteString("USER agent\n")
	b.WriteString("WORKDIR /home/agent\n\n")

	// CMD
	if len(contrib.Cmd) > 0 {
		quoted := make([]string, len(contrib.Cmd))
		for i, c := range contrib.Cmd {
			quoted[i] = fmt.Sprintf("%q", c)
		}
		b.WriteString(fmt.Sprintf("CMD [%s]\n", strings.Join(quoted, ", ")))
	}

	path := filepath.Join(g.OutDir, "Dockerfile")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeCompose generates .build/docker-compose.yml.
func (g *Generator) writeCompose(contrib *sdk.RuntimeContributions) error {
	var b strings.Builder

	b.WriteString("services:\n")
	b.WriteString(fmt.Sprintf("  %s:\n", g.Config.Name))
	b.WriteString("    build:\n")
	b.WriteString("      context: .\n")
	b.WriteString("      dockerfile: Dockerfile\n")
	b.WriteString(fmt.Sprintf("    container_name: %s\n", g.Config.Name))
	b.WriteString("    restart: unless-stopped\n")

	// Scan for env vars
	envVars := g.scanEnvVars()
	if len(envVars) > 0 {
		b.WriteString("    environment:\n")
		for _, v := range envVars {
			b.WriteString(fmt.Sprintf("      - %s=${%s}\n", v, v))
		}
	}

	path := filepath.Join(g.OutDir, "docker-compose.yml")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// writeEnvExample generates .build/.env.example.
func (g *Generator) writeEnvExample() error {
	envVars := g.scanEnvVars()
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

	return vars
}
