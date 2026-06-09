package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/dotenv"
	v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
	"github.com/spf13/cobra"
)

func generateCmd(dir *string) *cobra.Command {
	var coreFlag string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate build artifacts from agent.yaml or fleet.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			// Resolve core directory: --core flag overrides, otherwise use coreRoot.
			coreDir := coreRoot
			if coreFlag != "" {
				abs, err := filepath.Abs(coreFlag)
				if err != nil {
					return fmt.Errorf("resolve --core path: %w", err)
				}
				if _, err := os.Stat(abs); err != nil {
					return fmt.Errorf("--core path does not exist: %s", abs)
				}
				coreDir = abs
				fmt.Fprintf(os.Stderr, "Using local core: %s\n", abs)
			}

			if coreDir == "." && coreFlag == "" {
				fmt.Fprintf(os.Stderr, "Warning: could not detect core root from binary location. Use --core to specify.\n")
			}

			// Load .env file so secrets are available for auth-header baking.
			dotenv.Load(filepath.Join(projectDir, ".env"))

			// Try single-agent first, then fleet mode
			cfg, loadErr := config.Load(projectDir)
			if loadErr == nil {
				return generateSingleAgent(cfg, projectDir, coreDir)
			}

			// Try fleet mode
			_, agents, fleetErr := config.LoadFleetAgents(projectDir)
			if fleetErr != nil {
				return fmt.Errorf("cannot load agent.yaml or fleet.yaml:\n  agent: %w\n  fleet: %v", loadErr, fleetErr)
			}

			return generateFleet(agents, projectDir, coreDir)
		},
	}

	cmd.Flags().StringVar(&coreFlag, "core", "", "Path to local core directory (overrides auto-detected core root)")
	return cmd
}

func generateSingleAgent(cfg *config.Config, projectDir, coreDir string) error {
	g := v1.NewGeneratorWithCore(projectDir, coreDir)
	if err := g.RunWithConfig(cfg, projectDir); err != nil {
		return err
	}

	_ = ensureSchemaComment(filepath.Join(projectDir, "agent.yaml"), ".build/schema.json")
	fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", projectDir)
	return nil
}

func generateFleet(agents []config.FleetAgent, projectDir, coreDir string) error {
	g := v1.NewGeneratorWithCore(projectDir, coreDir)
	if err := g.RunFleet(agents); err != nil {
		return err
	}

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
