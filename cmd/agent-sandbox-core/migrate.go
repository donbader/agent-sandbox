package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/migrate"
	"github.com/spf13/cobra"
)

func migrateCmd(dir *string) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate plugins from legacy gateway format to egress format",
		Long: `Detects plugins using the deprecated contributes.gateway.services/middlewares
format and converts them to the new contributes.gateway.egress format.

Scans all plugin.yaml files referenced by installations in fleet.yaml and agent.yaml files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			projectDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve dir: %w", err)
			}

			// Find all plugin.yaml paths from installations
			paths, err := findPluginPaths(projectDir)
			if err != nil {
				return err
			}

			if len(paths) == 0 {
				fmt.Fprintln(os.Stderr, "No plugin installations found.")
				return nil
			}

			// Detect legacy plugins
			var legacyPaths []string
			for _, p := range paths {
				legacy, err := migrate.DetectLegacyGateway(p)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  warning: cannot check %s: %v\n", p, err)
					continue
				}
				if legacy {
					legacyPaths = append(legacyPaths, p)
				}
			}

			if len(legacyPaths) == 0 {
				fmt.Fprintln(os.Stderr, "All plugins already use the egress format. Nothing to migrate.")
				return nil
			}

			fmt.Fprintf(os.Stderr, "Found %d plugin(s) using legacy gateway format:\n\n", len(legacyPaths))

			// Show proposed transformations
			type transformation struct {
				path   string
				before string
				after  string
			}
			var transforms []transformation

			for _, p := range legacyPaths {
				before, after, err := migrate.TransformPlugin(p)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  error transforming %s: %v\n", p, err)
					continue
				}
				relPath, _ := filepath.Rel(projectDir, p)
				if relPath == "" {
					relPath = p
				}
				fmt.Fprintf(os.Stderr, "  %s\n", relPath)
				transforms = append(transforms, transformation{path: p, before: before, after: after})
			}
			fmt.Fprintln(os.Stderr)

			if dryRun {
				for _, t := range transforms {
					relPath, _ := filepath.Rel(projectDir, t.path)
					fmt.Fprintf(os.Stderr, "--- %s (before)\n", relPath)
					fmt.Fprintf(os.Stderr, "+++ %s (after)\n", relPath)
					printSimpleDiff(t.before, t.after)
					fmt.Fprintln(os.Stderr)
				}
				fmt.Fprintln(os.Stderr, "(dry run — no files modified)")
				return nil
			}

			// Prompt for confirmation
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) != 0 {
				// Interactive TTY — show diff and prompt
				for _, t := range transforms {
					relPath, _ := filepath.Rel(projectDir, t.path)
					fmt.Fprintf(os.Stderr, "--- %s (before)\n", relPath)
					fmt.Fprintf(os.Stderr, "+++ %s (after)\n", relPath)
					printSimpleDiff(t.before, t.after)
					fmt.Fprintln(os.Stderr)
				}

				fmt.Fprintf(os.Stderr, "Apply migration to %d file(s)? [Y/n] ", len(transforms))
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "" && answer != "y" && answer != "yes" {
					fmt.Fprintln(os.Stderr, "Migration cancelled.")
					return nil
				}
			}

			// Write transformed files
			for _, t := range transforms {
				if err := os.WriteFile(t.path, []byte(t.after), 0644); err != nil {
					return fmt.Errorf("write %s: %w", t.path, err)
				}
				relPath, _ := filepath.Rel(projectDir, t.path)
				fmt.Fprintf(os.Stderr, "  ✓ Migrated %s\n", relPath)
			}

			fmt.Fprintf(os.Stderr, "\nMigrated %d plugin(s) to egress format.\n", len(transforms))
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show proposed changes without writing files")

	return cmd
}

// findPluginPaths collects all local plugin.yaml paths from the project's installations.
func findPluginPaths(projectDir string) ([]string, error) {
	project, err := config.LoadProject(projectDir)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var paths []string

	for _, agent := range project.Agents {
		for _, inst := range agent.Config.Installations {
			pluginPath := resolvePluginYAMLPath(projectDir, agent.Dir, inst.Plugin)
			if pluginPath == "" {
				continue
			}
			if seen[pluginPath] {
				continue
			}
			if _, err := os.Stat(pluginPath); err != nil {
				continue
			}
			seen[pluginPath] = true
			paths = append(paths, pluginPath)
		}
	}

	return paths, nil
}

// resolvePluginYAMLPath resolves a plugin reference to its plugin.yaml path.
// Returns empty string for @builtin/ plugins (can't migrate those in place).
func resolvePluginYAMLPath(projectDir, agentDir, pluginRef string) string {
	if strings.HasPrefix(pluginRef, "@builtin/") {
		// Built-in plugins are in the core directory — skip for migration
		return ""
	}
	if strings.HasPrefix(pluginRef, "@fleet/") {
		relPath := strings.TrimPrefix(pluginRef, "@fleet/")
		return filepath.Join(projectDir, relPath, "plugin.yaml")
	}
	if strings.HasPrefix(pluginRef, "./") {
		return filepath.Join(agentDir, pluginRef, "plugin.yaml")
	}
	return ""
}

// printSimpleDiff prints a simple line-based diff showing removed/added lines.
func printSimpleDiff(before, after string) {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	// Simple approach: show lines unique to before as removed, lines unique to after as added
	// For a migration tool, showing full before/after is fine
	beforeSet := make(map[string]int)
	for _, l := range beforeLines {
		beforeSet[l]++
	}
	afterSet := make(map[string]int)
	for _, l := range afterLines {
		afterSet[l]++
	}

	for _, l := range beforeLines {
		if afterSet[l] > 0 {
			afterSet[l]--
		} else {
			fmt.Fprintf(os.Stderr, "\033[31m- %s\033[0m\n", l)
		}
	}

	// Reset afterSet
	afterSet = make(map[string]int)
	for _, l := range afterLines {
		afterSet[l]++
	}
	beforeSet2 := make(map[string]int)
	for _, l := range beforeLines {
		beforeSet2[l]++
	}
	for _, l := range afterLines {
		if beforeSet2[l] > 0 {
			beforeSet2[l]--
		} else {
			fmt.Fprintf(os.Stderr, "\033[32m+ %s\033[0m\n", l)
		}
	}
}
