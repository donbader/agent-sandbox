package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/donbader/agent-sandbox/internal/migrate"
	"github.com/donbader/agent-sandbox/internal/plugin"
	"gopkg.in/yaml.v3"
)

// MigrateResult tracks whether any files were rewritten during migration.
type MigrateResult struct {
	Rewritten bool
}

// RunMigrations performs all auto-migrations and corrections on project files.
// Returns true if any files were rewritten (caller should reload the project).
func RunMigrations(projectDir string, project *config.Project, autoMigrate bool) (bool, error) {
	var rewritten bool

	// 1. Migrate legacy gateway.services in agent.yaml → gateway.egress
	for _, agent := range project.Agents {
		if config.HasLegacyServices(agent.Config) {
			if err := migrateAgentGatewayServices(agent, autoMigrate); err != nil {
				return false, err
			}
			rewritten = true
		}
	}

	// 2. Migrate legacy plugin gateway format (services/middlewares → egress)
	if migrated, err := migratePluginGatewayFormat(projectDir, project, autoMigrate); err != nil {
		return false, err
	} else if migrated {
		rewritten = true
	}

	// 3. Auto-correct project-path options (./ ../ → @fleet/)
	if corrected, err := correctProjectPaths(projectDir, project); err != nil {
		return false, err
	} else if corrected {
		rewritten = true
	}

	// 4. Auto-correct middleware script form ({script: "..."} → plain string)
	if corrected, err := correctMiddlewareScriptForm(projectDir, project); err != nil {
		return false, err
	} else if corrected {
		rewritten = true
	}

	return rewritten, nil
}

// --- Agent gateway.services → egress migration ---

func migrateAgentGatewayServices(agent config.Agent, autoMigrate bool) error {
	cfg := agent.Config

	fmt.Fprintf(os.Stderr, "\n⚠️  Agent %q uses deprecated gateway.services format.\n", cfg.Name)
	fmt.Fprintf(os.Stderr, "   The new gateway.egress format provides whitelist/blacklist control.\n")
	fmt.Fprintf(os.Stderr, "   Run with --migrate to convert automatically.\n\n")

	rules := config.MigrateServicesToEgress(cfg.Gateway.Services)

	if autoMigrate {
		return applyAgentEgressMigration(agent, rules)
	}

	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
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
		return applyAgentEgressMigration(agent, rules)
	}

	fmt.Fprintf(os.Stderr, "   Skipped. gateway.services will continue to work but is deprecated.\n")
	return nil
}

func applyAgentEgressMigration(agent config.Agent, rules []config.EgressRule) error {
	agentYAML := filepath.Join(agent.Dir, "agent.yaml")

	data, err := os.ReadFile(agentYAML)
	if err != nil {
		return fmt.Errorf("read %s: %w", agentYAML, err)
	}

	content := string(data)

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

	if idx := strings.Index(content, "gateway:"); idx >= 0 {
		gatewaySection := content[idx:]
		if svcIdx := strings.Index(gatewaySection, "  services:"); svcIdx >= 0 {
			absStart := idx + svcIdx
			restAfterSvc := content[absStart+len("  services:"):]
			endOffset := findBlockEnd(restAfterSvc, 4)
			absEnd := absStart + len("  services:") + endOffset
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

// --- Plugin gateway format migration (services/middlewares → egress) ---

func migratePluginGatewayFormat(projectDir string, project *config.Project, autoMigrate bool) (bool, error) {
	paths, err := findPluginPaths(projectDir, project)
	if err != nil {
		return false, err
	}

	if len(paths) == 0 {
		return false, nil
	}

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

	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		fmt.Fprintf(os.Stderr, "   (non-interactive: skipping plugin migration, use --migrate)\n")
		return false, nil
	}

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

// --- Project-path auto-correction (./ ../ → @fleet/) ---

func correctProjectPaths(projectDir string, project *config.Project) (bool, error) {
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

func correctAgentProjectPaths(projectDir string, agent config.Agent) (bool, error) {
	if len(agent.Config.Installations) == 0 {
		return false, nil
	}

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

		pluginDef, err := loadPluginDef(projectDir, agent.Dir, inst.Plugin)
		if err != nil {
			continue
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

// --- Middleware script form auto-correction ({script: "..."} → plain string) ---

func correctMiddlewareScriptForm(projectDir string, project *config.Project) (bool, error) {
	paths, err := findPluginPaths(projectDir, project)
	if err != nil {
		return false, err
	}

	anyChanged := false
	for _, p := range paths {
		fixed, err := migrate.FixMiddlewareScriptForm(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: cannot fix middleware format in %s: %v\n", p, err)
			continue
		}
		if fixed {
			relPath, _ := filepath.Rel(projectDir, p)
			fmt.Fprintf(os.Stderr, "   ✓ Simplified middleware format in %s\n", relPath)
			anyChanged = true
		}
	}

	return anyChanged, nil
}

// --- Shared helpers ---

func loadPluginDef(projectDir, agentDir, pluginRef string) (*plugin.PluginDef, error) {
	yamlPath := resolvePluginYAMLPath(projectDir, agentDir, pluginRef)
	if yamlPath == "" {
		return nil, fmt.Errorf("cannot resolve plugin %q", pluginRef)
	}
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Options map[string]plugin.OptionSchema `yaml:"options"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &plugin.PluginDef{Options: raw.Options}, nil
}

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

func findBlockEnd(content string, minIndent int) int {
	lines := strings.Split(content, "\n")
	offset := 0
	for i, line := range lines {
		if i == 0 {
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

func countIndent(line string) int {
	for i, c := range line {
		if c != ' ' {
			return i
		}
	}
	return len(line)
}

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
