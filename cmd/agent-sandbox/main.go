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
	var dir string

	root := &cobra.Command{
		Use:              "agent-sandbox",
		Short:            "Opinionated agent sandbox orchestrator",
		Version:          version,
		TraverseChildren: true,
	}

	root.PersistentFlags().StringVarP(&dir, "dir", "C", ".", "Project directory containing agent.yaml")

	root.AddCommand(generateCmd(&dir))
	root.AddCommand(composeCmd(&dir))

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func generateCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate .build/ artifacts from agent config",
		RunE: func(cmd *cobra.Command, args []string) error {
			outDir := filepath.Join(*dir, ".build")

			cfg, err := config.Load(*dir)
			if err != nil {
				return err
			}

			// Resolve runtime
			runtime, err := resolve.ResolveRuntime(*dir, cfg.Runtime)
			if err != nil {
				return fmt.Errorf("resolving runtime %q: %w", cfg.Runtime, err)
			}

			// Resolve features
			var features []*resolve.FeatureContributions
			hasBridge := false
			for i, entry := range cfg.Features {
				instanceName := fmt.Sprintf("features[%d]", i)
				if entry.Name != "" {
					instanceName = entry.Name
				}
				contrib, err := resolve.ResolveFeature(*dir, entry.Plugin, instanceName, entry.Config)
				if err != nil {
					return fmt.Errorf("resolving feature %q (plugin %q): %w", instanceName, entry.Plugin, err)
				}
				features = append(features, contrib)
				if contrib.BridgeChannel != "" {
					hasBridge = true
				}
			}

			g := &generate.Generator{
				Config:   cfg,
				Runtime:  runtime,
				Features: features,
				Gateway:  cfg.GatewayEnabled(),
				Bridge:   hasBridge,
				GatewaySpec: generate.GatewaySpec{
					BuildImage: "golang:1.24-alpine",
					BinaryPath: "/gateway",
					ListenPort: 8443,
					DNSPort:    53,
				},
				BridgeSpec: generate.BridgeSpec{
					BuildImage: "node:22-slim",
					InstallCmd: "npm install",
					BuildCmd:   "npm run build",
					DistDir:    "/src/dist",
					EntryPoint: "node /opt/bridge/dist/index.js",
				},
				Dir:    *dir,
				OutDir: outDir,
			}

			if err := g.Run(); err != nil {
				return err
			}

			fmt.Printf("Generated artifacts in %s\n", outDir)
			return nil
		},
	}

	return cmd
}

func composeCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "compose",
		Short:              "Docker compose passthrough (auto-injects -f .build/docker-compose.yml)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			composePath := filepath.Join(*dir, ".build", "docker-compose.yml")
			if _, err := os.Stat(composePath); os.IsNotExist(err) {
				return fmt.Errorf("%s not found — run 'agent-sandbox generate' first", composePath)
			}

			composeArgs := []string{"-f", composePath}
			// Auto-inject --env-file if .env exists in project dir
			envPath := filepath.Join(*dir, ".env")
			if _, err := os.Stat(envPath); err == nil {
				composeArgs = append(composeArgs, "--env-file", envPath)
			}
			composeArgs = append(composeArgs, args...)
			c := exec.Command("docker", append([]string{"compose"}, composeArgs...)...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			return c.Run()
		},
	}

	return cmd
}
