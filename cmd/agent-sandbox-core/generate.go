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
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate build artifacts from fleet.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			coreDir := coreRoot
			if coreDir == "." {
				fmt.Fprintf(os.Stderr, "Warning: could not detect core root from binary location.\n")
			}

			dotenv.Load(filepath.Join(projectDir, ".env"))

			project, err := config.LoadProject(projectDir)
			if err != nil {
				return err
			}

			g := v1.NewGeneratorWithCore(projectDir, coreDir)
			g.SetCoreVersion(version)
			if err := g.RunProject(project); err != nil {
				return err
			}

			_ = ensureSchemaComment(filepath.Join(projectDir, "fleet.yaml"), ".build/fleet-schema.json")
			for _, agent := range project.Agents {
				agentYAML := filepath.Join(agent.Dir, "agent.yaml")
				relSchema, relErr := filepath.Rel(agent.Dir, filepath.Join(projectDir, ".build", "schema.json"))
				if relErr != nil {
					relSchema = ".build/schema.json"
				}
				_ = ensureSchemaComment(agentYAML, relSchema)
			}

			fmt.Fprintf(os.Stderr, "Generated .build/ for %d agent(s) in %s\n", len(project.Agents), projectDir)
			return nil
		},
	}
	return cmd
}
