package main

import (
	"fmt"
	"os"
	"path/filepath"

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
				return fmt.Errorf("cannot load agent.yaml or fleet.yaml:\n  agent: %w\n  fleet: %v", loadErr, fleetErr)
			}

			return generateFleet(agents, projectDir)
		},
	}
}

func generateSingleAgent(cfg *config.Config, projectDir string) error {
	coreDir, err := fetchCore(cfg.CoreVersion)
	if err != nil {
		return err
	}

	g := v1.NewGeneratorWithCore(projectDir, coreDir)
	if err := g.RunWithConfig(cfg, projectDir); err != nil {
		return err
	}

	_ = ensureSchemaComment(filepath.Join(projectDir, "agent.yaml"), ".build/schema.json")
	fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", projectDir)
	return nil
}

func generateFleet(agents []config.FleetAgent, projectDir string) error {
	// Fleet uses first agent's core_version (or "latest" if not set)
	version := "latest"
	if len(agents) > 0 && agents[0].Config.CoreVersion != "" {
		version = agents[0].Config.CoreVersion
	}

	coreDir, err := fetchCore(version)
	if err != nil {
		return err
	}

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

// fetchCore resolves a core version and returns the cache directory.
// "latest" queries GitHub for the newest core-v* release.
// Any other value fetches that specific version.
func fetchCore(version string) (string, error) {
	if version == "" || version == "latest" {
		dir, err := core.FetchLatest()
		if err != nil {
			return "", fmt.Errorf("fetch latest core: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Using latest core from %s\n", dir)
		return dir, nil
	}

	dir, err := core.Fetch(version)
	if err != nil {
		return "", fmt.Errorf("fetch core %s: %w", version, err)
	}
	fmt.Fprintf(os.Stderr, "Using core %s from %s\n", version, dir)
	return dir, nil
}
