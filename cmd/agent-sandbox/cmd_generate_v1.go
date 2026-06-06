package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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

			// Load .env file so secrets are available for auth-header baking.
			loadDotEnv(filepath.Join(projectDir, ".env"))

			cfg, err := config.Load(projectDir)
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
			// When no external core dir, use the embedded gateway source and bundled plugins
			if coreDir == "" {
				g.SetGatewayFS(sandbox.GatewaySource)
				pluginsFS, _ := fs.Sub(sandbox.CorePlugins, "core/plugins")
				g.SetBundledPluginsFS(pluginsFS)
			}
			if err := g.Run(); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Generated .build/ in %s\n", projectDir)
			return nil
		},
	}
}

// loadDotEnv loads key=value pairs from a .env file into the process environment.
// Only sets variables not already present in the environment (no override).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}
