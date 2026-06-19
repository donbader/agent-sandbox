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
	"github.com/donbader/agent-sandbox/internal/migrate"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

			// Check for legacy gateway.services format in agent.yaml and offer migration
			for _, agent := range project.Agents {
				if config.HasLegacyServices(agent.Config) {
					if err := handleMigration(agent, autoMigrate); err != nil {
						return err
					}
				}
			}

			// Check for legacy plugins (contributes.gateway.services/middlewares)
			pluginMigrated, err := handlePluginMigration(projectDir, project, autoMigrate)
			if err != nil {
				return err
			}

			// Auto-correct project-path options (./  ../ → @fleet/)
			pathCorrected, err := autoCorrectProjectPaths(projectDir, project)
			if err != nil {
				return err
			}

			// Reload project if any files were rewritten
			if pluginMigrated || pathCorrected {
				project, err = config.LoadProject(projectDir)
				if err != nil {
					return err
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
	fmt.Fprintf(os.Stderr, "   The new gateway.egress format provides whitelist/blacklist control.\n")
	fmt.Fprintf(os.Stderr, "   Run with --migrate to convert automatically.\n\n")

	// Show what the migration would look like
	rules := config.MigrateServicesToEgress(cfg.Gateway.Services)

	if autoMigrate {
		return applyMigration(agent, rules)
	}

	// Only prompt interactively if stdin is a terminal
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Not a TTY (CI, piped input) — skip migration, just warn
		fmt.Fprintf(os.Stderr, "   (non-interactive: skipping migration)\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "   Equivalent gateway.egress config:\n\n")
	migrated := config.FormatEgressYAML(rules)
	for _, line := range strings.Split(migrated, "\n") {
		fmt.Fprintf(os.Stderr, "   %s\n", line)
	}
	fmt.Fprintln(os.Stderr)

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

			// Bounds safety
			if absEnd > len(content) {
				absEnd = len(content)
			}

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

// handlePluginMigration detects legacy plugin gateway formats and prompts for migration.
// Returns true if any files were rewritten.
func handlePluginMigration(projectDir string, project *config.Project, autoMigrate bool) (bool, error) {
	paths, err := findPluginPaths(projectDir, project)
	if err != nil {
		return false, err
	}

	if len(paths) == 0 {
		return false, nil
	}

	// Detect legacy plugins
	type transformation struct {
		path   string
		before string
		after  string
	}
	var transforms []transformation

	for _, p := range paths {
		legacy, err := migrate.DetectLegacyGateway(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: cannot check %s: %v\n", p, err)
			continue
		}
		if !legacy {
			continue
		}
		before, after, err := migrate.TransformPlugin(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: cannot transform %s: %v\n", p, err)
			continue
		}
		transforms = append(transforms, transformation{path: p, before: before, after: after})
	}

	if len(transforms) == 0 {
		return false, nil
	}

	fmt.Fprintf(os.Stderr, "\n⚠️  Found %d plugin(s) using deprecated gateway.services/middlewares format.\n\n", len(transforms))

	if autoMigrate {
		for _, t := range transforms {
			if err := os.WriteFile(t.path, []byte(t.after), 0644); err != nil {
				return false, fmt.Errorf("write %s: %w", t.path, err)
			}
			relPath, _ := filepath.Rel(projectDir, t.path)
			fmt.Fprintf(os.Stderr, "   ✓ Migrated %s\n", relPath)
		}
		return true, nil
	}

	// Non-interactive: skip with warning
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		fmt.Fprintf(os.Stderr, "   (non-interactive: skipping plugin migration, use --migrate)\n")
		return false, nil
	}

	// Interactive: show diff and prompt
	for _, t := range transforms {
		relPath, _ := filepath.Rel(projectDir, t.path)
		fmt.Fprintf(os.Stderr, "--- %s (before)\n", relPath)
		fmt.Fprintf(os.Stderr, "+++ %s (after)\n", relPath)
		printSimpleDiff(t.before, t.after)
		fmt.Fprintln(os.Stderr)
	}

	fmt.Fprintf(os.Stderr, "   Migrate %d plugin(s) to egress format? [Y/n] ", len(transforms))
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Fprintf(os.Stderr, "   Skipped plugin migration.\n")
		return false, nil
	}

	for _, t := range transforms {
		if err := os.WriteFile(t.path, []byte(t.after), 0644); err != nil {
			return false, fmt.Errorf("write %s: %w", t.path, err)
		}
		relPath, _ := filepath.Rel(projectDir, t.path)
		fmt.Fprintf(os.Stderr, "   ✓ Migrated %s\n", relPath)
	}
	return true, nil
}

// autoCorrectProjectPaths silently rewrites agent.yaml installation options
// that use ./ or ../ for project-path typed options to use @fleet/ prefix.
// Returns true if any files were rewritten.
func autoCorrectProjectPaths(projectDir string, project *config.Project) (bool, error) {
	anyChanged := false

	for _, agent := range project.Agents {
		changed, err := correctAgentProjectPaths(projectDir, agent)
		if err != nil {
			return false, err
		}
		if changed {
			anyChanged = true
		}
	}

	return anyChanged, nil
}

// correctAgentProjectPaths checks a single agent's installations for project-path
// options using relative paths and rewrites them to @fleet/ prefix.
func correctAgentProjectPaths(projectDir string, agent config.Agent) (bool, error) {
	if len(agent.Config.Installations) == 0 {
		return false, nil
	}

	// Load plugin definitions to find project-path typed options
	type correction struct {
		instIdx int
		optKey  string
		newVal  string
	}
	var corrections []correction

	for i, inst := range agent.Config.Installations {
		if len(inst.Options) == 0 {
			continue
		}

		// Resolve the plugin to get its option schema
		pluginDef, err := loadPluginDef(projectDir, agent.Dir, inst.Plugin)
		if err != nil {
			continue // skip plugins we can't load (builtins handled separately)
		}

		for optName, schema := range pluginDef.Options {
			if schema.Type != "project-path" {
				continue
			}
			val, ok := inst.Options[optName]
			if !ok {
				continue
			}
			strVal, ok := val.(string)
			if !ok || strings.HasPrefix(strVal, "@fleet/") {
				continue
			}
			if !strings.HasPrefix(strVal, "./") && !strings.HasPrefix(strVal, "../") {
				continue
			}

			// Resolve relative to agent dir, then make @fleet/ relative to project root
			absPath := filepath.Join(agent.Dir, strVal)
			relToProject, err := filepath.Rel(projectDir, absPath)
			if err != nil {
				continue
			}
			relToProject = filepath.ToSlash(relToProject)
			newVal := "@fleet/" + relToProject
			corrections = append(corrections, correction{instIdx: i, optKey: optName, newVal: newVal})
		}
	}

	if len(corrections) == 0 {
		return false, nil
	}

	// Rewrite agent.yaml with corrected values
	agentYAML := filepath.Join(agent.Dir, "agent.yaml")
	data, err := os.ReadFile(agentYAML)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", agentYAML, err)
	}

	content := string(data)
	for _, c := range corrections {
		oldVal, ok := agent.Config.Installations[c.instIdx].Options[c.optKey].(string)
		if !ok {
			continue
		}
		// Replace the option value in YAML — handles both quoted and unquoted forms
		content = strings.Replace(content, oldVal, c.newVal, 1)
	}

	if content == string(data) {
		return false, nil
	}

	if err := os.WriteFile(agentYAML, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("write %s: %w", agentYAML, err)
	}
	return true, nil
}

// loadPluginDef loads a plugin's definition to inspect its option schema.
// Returns nil for @builtin/ plugins (we don't auto-correct those here).
func loadPluginDef(projectDir, agentDir, pluginRef string) (*plugin.PluginDef, error) {
	yamlPath := resolvePluginYAMLPath(projectDir, agentDir, pluginRef)
	if yamlPath == "" {
		return nil, fmt.Errorf("cannot resolve plugin %q", pluginRef)
	}
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}
	// Use a minimal parse — we only need options schema
	var raw struct {
		Options map[string]plugin.OptionSchema `yaml:"options"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &plugin.PluginDef{Options: raw.Options}, nil
}

// findPluginPaths collects all local plugin.yaml paths from the project's installations.
func findPluginPaths(projectDir string, project *config.Project) ([]string, error) {
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
