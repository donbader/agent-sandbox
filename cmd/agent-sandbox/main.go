package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/generate"
	_ "github.com/donbader/agent-sandbox/internal/plugins" // register core feature plugins
	"github.com/donbader/agent-sandbox/internal/resolve"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "agent-sandbox",
		Short:   "Opinionated agent sandbox orchestrator",
		Version: version,
	}

	root.AddCommand(generateCmd())
	root.AddCommand(composeCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func generateCmd() *cobra.Command {
	var dir string
	var outDir string

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate .build/ artifacts from agent config",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}

			// Resolve runtime
			runtime, err := resolve.ResolveRuntime(dir, cfg.Runtime)
			if err != nil {
				return err
			}

			// Resolve features
			var features []*resolve.FeatureContributions
			for name, featureCfg := range cfg.Features {
				contrib, err := resolve.ResolveFeature(dir, name, featureCfg)
				if err != nil {
					return err
				}
				features = append(features, contrib)
			}

			g := &generate.Generator{
				Config:   cfg,
				Runtime:  runtime,
				Features: features,
				Gateway:  cfg.GatewayEnabled(),
				Dir:      dir,
				OutDir:   outDir,
			}

			if err := g.Run(); err != nil {
				return err
			}

			fmt.Printf("Generated artifacts in %s\n", outDir)
			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "Directory containing agent.yaml")
	cmd.Flags().StringVarP(&outDir, "output", "o", ".build", "Output directory for generated artifacts")

	return cmd
}

func composeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "compose",
		Short:              "Docker compose passthrough (auto-injects -f .build/docker-compose.yml)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			composePath := filepath.Join(".build", "docker-compose.yml")
			if _, err := os.Stat(composePath); os.IsNotExist(err) {
				return fmt.Errorf(".build/docker-compose.yml not found — run 'agent-sandbox generate' first")
			}

			composeArgs := append([]string{"-f", composePath}, args...)
			c := exec.Command("docker", append([]string{"compose"}, composeArgs...)...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			return c.Run()
		},
	}

	return cmd
}
