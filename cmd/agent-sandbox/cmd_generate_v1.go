package main

import (
	"fmt"
	"os"
	"path/filepath"

	sandbox "github.com/donbader/agent-sandbox"
	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/core"
	v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
	"github.com/spf13/cobra"
)

func generateV1Cmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate build artifacts from agent.yaml (v1)",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			cfg, err := config.LoadV1(projectDir)
			if err != nil {
				return err
			}

			var coreDir string
			if cfg.CoreVersion != "" {
				coreDir, err = core.Fetch(cfg.CoreVersion)
				if err != nil {
					return fmt.Errorf("fetch core %s: %w", cfg.CoreVersion, err)
				}
				fmt.Fprintf(os.Stderr, "Using core %s from %s\n", cfg.CoreVersion, coreDir)
			}

			g := v1.NewGeneratorWithCore(projectDir, coreDir)
			// When no external core dir, use the embedded gateway source
			if coreDir == "" {
				g.SetGatewayFS(sandbox.GatewaySource)
			}
			if err := g.Run(); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", projectDir)
			return nil
		},
	}
}
