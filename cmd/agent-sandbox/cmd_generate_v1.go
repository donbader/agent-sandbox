package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	sandbox "github.com/donbader/agent-sandbox"
	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/core"
	"github.com/donbader/agent-sandbox/internal/dotenv"
	v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
	"github.com/spf13/cobra"
)

func generateV1Cmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate build artifacts from agent.yaml or fleet.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			// Load .env file so secrets are available for auth-header baking.
			dotenv.Load(filepath.Join(projectDir, ".env"))

			// Try single-agent first, then fleet mode
			cfg, loadErr := config.Load(projectDir)
			if loadErr == nil {
				return generateSingleAgent(cfg, projectDir)
			}

			// Try fleet mode
			_, agents, fleetErr := config.LoadFleetAgents(projectDir)
			if fleetErr != nil {
				// Neither agent.yaml nor fleet.yaml found
				return fmt.Errorf("cannot load agent.yaml or fleet.yaml:\n  agent: %w\n  fleet: %v", loadErr, fleetErr)
			}

			return generateFleet(agents, projectDir)
		},
	}
}

func generateSingleAgent(cfg *config.Config, projectDir string) error {
	var coreDir string
	var err error
	if cfg.CoreVersion != "" {
		coreDir, err = core.Fetch(cfg.CoreVersion)
		if err != nil {
			return fmt.Errorf("fetch core %s: %w", cfg.CoreVersion, err)
		}
		fmt.Fprintf(os.Stderr, "Using core %s from %s\n", cfg.CoreVersion, coreDir)
	}

	g := v1.NewGeneratorWithCore(projectDir, coreDir)
	if coreDir == "" {
		g.SetGatewayFS(sandbox.GatewaySource)
		pluginsFS, _ := fs.Sub(sandbox.CorePlugins, "core/plugins")
		g.SetBundledPluginsFS(pluginsFS)
	}
	if err := g.RunWithConfig(cfg, projectDir); err != nil {
		return err
	}

	// Inject schema comment into agent.yaml
	_ = ensureSchemaComment(filepath.Join(projectDir, "agent.yaml"), ".build/schema.json")

	fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", projectDir)
	return nil
}

func generateFleet(agents []config.FleetAgent, projectDir string) error {
	// Fleet mode: use embedded core (core_version per-agent not yet supported in fleet)
	g := v1.NewGeneratorWithCore(projectDir, "")
	g.SetGatewayFS(sandbox.GatewaySource)
	pluginsFS, _ := fs.Sub(sandbox.CorePlugins, "core/plugins")
	g.SetBundledPluginsFS(pluginsFS)

	if err := g.RunFleet(agents); err != nil {
		return err
	}

	// Inject schema comments into fleet.yaml and per-agent agent.yaml files
	_ = ensureSchemaComment(filepath.Join(projectDir, "fleet.yaml"), ".build/fleet-schema.json")
	for _, agent := range agents {
		agentYAML := filepath.Join(agent.Dir, "agent.yaml")
		relSchema, err := filepath.Rel(agent.Dir, filepath.Join(projectDir, ".build", "schema.json"))
		if err != nil {
			relSchema = ".build/schema.json"
		}
		_ = ensureSchemaComment(agentYAML, relSchema)
	}

	fmt.Fprintf(os.Stderr, "Generated .build/ for %d agents in %s\n", len(agents), projectDir)
	return nil
}
