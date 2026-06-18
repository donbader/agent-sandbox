package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/dotenv"
	v1 "github.com/donbader/agent-sandbox/internal/generate/v1"
	"github.com/spf13/cobra"
)

func generateCmd(dir *string) *cobra.Command {
	var autoMigrate bool

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

			// Check for legacy gateway.services format and offer migration
			for _, agent := range project.Agents {
				if config.HasLegacyServices(agent.Config) {
					if err := handleMigration(agent, autoMigrate); err != nil {
						return err
					}
				}
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

	cmd.Flags().BoolVar(&autoMigrate, "migrate", false, "Automatically migrate legacy gateway.services to gateway.egress format")

	return cmd
}

// handleMigration detects legacy gateway.services and prompts the user to migrate.
func handleMigration(agent config.Agent, autoMigrate bool) error {
	cfg := agent.Config

	fmt.Fprintf(os.Stderr, "\n⚠️  Agent %q uses deprecated gateway.services format.\n", cfg.Name)
	fmt.Fprintf(os.Stderr, "   The new gateway.egress format provides whitelist/blacklist control.\n\n")

	// Show what the migration would look like
	rules := config.MigrateServicesToEgress(cfg.Gateway.Services)
	fmt.Fprintf(os.Stderr, "   Equivalent gateway.egress config:\n\n")
	migrated := config.FormatEgressYAML(rules)
	for _, line := range strings.Split(migrated, "\n") {
		fmt.Fprintf(os.Stderr, "   %s\n", line)
	}
	fmt.Fprintln(os.Stderr)

	if autoMigrate {
		return applyMigration(agent, rules)
	}

	fmt.Fprintf(os.Stderr, "   Migrate %s? [Y/n] ", filepath.Join(agent.Dir, "agent.yaml"))
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "" || answer == "y" || answer == "yes" {
		return applyMigration(agent, rules)
	}

	fmt.Fprintf(os.Stderr, "   Skipped. gateway.services will continue to work but is deprecated.\n")
	return nil
}

// applyMigration rewrites the agent.yaml to use gateway.egress instead of gateway.services.
func applyMigration(agent config.Agent, rules []config.EgressRule) error {
	agentYAML := filepath.Join(agent.Dir, "agent.yaml")

	data, err := os.ReadFile(agentYAML)
	if err != nil {
		return fmt.Errorf("read %s: %w", agentYAML, err)
	}

	content := string(data)

	// Build replacement YAML block
	var sb strings.Builder
	sb.WriteString("  egress:\n")
	for _, rule := range rules {
		sb.WriteString("    - hosts: [")
		for i, h := range rule.Hosts {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%q", h)
		}
		sb.WriteString("]\n")
		if rule.Deny {
			sb.WriteString("      deny: true\n")
		}
		if len(rule.Headers) > 0 {
			sb.WriteString("      headers:\n")
			for k, v := range rule.Headers {
				fmt.Fprintf(&sb, "        %s: %s\n", k, v)
			}
		}
	}

	// Try to replace the services block with egress
	// Look for "  services:" under "gateway:"
	if idx := strings.Index(content, "gateway:"); idx >= 0 {
		// Find the "  services:" line after "gateway:"
		gatewaySection := content[idx:]
		if svcIdx := strings.Index(gatewaySection, "  services:"); svcIdx >= 0 {
			// Find the end of the services block (next top-level or same-indent key)
			absStart := idx + svcIdx
			restAfterSvc := content[absStart+len("  services:"):]

			// Find next line that starts at indent level <= 2 (same level as "  services:")
			endOffset := findBlockEnd(restAfterSvc, 4) // services entries are at 4+ spaces
			absEnd := absStart + len("  services:") + endOffset

			newContent := content[:absStart] + sb.String() + content[absEnd:]
			if err := os.WriteFile(agentYAML, []byte(newContent), 0644); err != nil {
				return fmt.Errorf("write %s: %w", agentYAML, err)
			}
			fmt.Fprintf(os.Stderr, "   ✓ Migrated %s to gateway.egress format.\n", agentYAML)
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "   ⚠ Could not auto-rewrite file. Please manually update %s\n", agentYAML)
	return nil
}

// findBlockEnd finds the end of an indented YAML block.
// Returns the byte offset where the block ends (first line with indent < minIndent).
func findBlockEnd(content string, minIndent int) int {
	lines := strings.Split(content, "\n")
	offset := 0
	for i, line := range lines {
		if i == 0 {
			// First line is the rest of "  services:" line
			offset += len(line) + 1
			continue
		}
		if line == "" {
			offset += 1
			continue
		}
		indent := countIndent(line)
		if indent < minIndent && strings.TrimSpace(line) != "" {
			return offset
		}
		offset += len(line) + 1
	}
	return offset
}

// countIndent counts leading spaces.
func countIndent(line string) int {
	for i, c := range line {
		if c != ' ' {
			return i
		}
	}
	return len(line)
}
