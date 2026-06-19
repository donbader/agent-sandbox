package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/spf13/cobra"
)

var version = "dev"

// coreRoot is resolved at init time from the binary's own location.
// It points to the directory containing this binary and its sibling assets
// (gateway/, plugins/, presets/, templates/).
var coreRoot string

func init() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
		if err == nil {
			coreRoot = filepath.Dir(exe)
		}
	}
	if coreRoot == "" {
		coreRoot = "."
	}
}

func main() {
	var dir string

	root := &cobra.Command{
		Use:              "agent-sandbox-core",
		Short:            "Opinionated agent sandbox orchestrator (core binary)",
		Version:          version,
		TraverseChildren: true,
	}

	root.PersistentFlags().StringVarP(&dir, "dir", "C", ".", "Project directory containing agent.yaml")

	root.AddCommand(generateCmd(&dir))
	root.AddCommand(composeCmd(&dir))
	root.AddCommand(auditCmd(&dir))
	root.AddCommand(migrateCmd(&dir))
	root.AddCommand(initCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ensureSchemaComment ensures the yaml-language-server schema comment is correct
// in the given YAML file. Inserts or replaces the first line if needed.
func ensureSchemaComment(yamlPath string, schemaRelPath string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return err
	}

	expected := fmt.Sprintf("# yaml-language-server: $schema=%s", schemaRelPath)
	lines := strings.SplitAfter(string(data), "\n")

	if len(lines) > 0 && strings.TrimSpace(lines[0]) == expected {
		return nil // already correct
	}

	// Check if first line is an existing schema comment that needs replacing
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# yaml-language-server: $schema=") {
		lines[0] = expected + "\n"
	} else {
		lines = append([]string{expected + "\n"}, lines...)
	}

	return os.WriteFile(yamlPath, []byte(strings.Join(lines, "")), 0644)
}

func composeCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "compose",
		Short:              "Compose passthrough (auto-injects -f .build/docker-compose.yml)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			composePath := filepath.Join(*dir, ".build", "docker-compose.yml")
			if _, err := os.Stat(composePath); os.IsNotExist(err) {
				return fmt.Errorf("%s not found — run 'agent-sandbox generate' first", composePath)
			}

			// Use the project folder name as the compose project name.
			absDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve project dir: %w", err)
			}
			projectName := filepath.Base(absDir)

			composeArgs := []string{"-f", composePath, "--project-name", projectName}
			// Auto-inject --env-file if .env exists in project dir
			envPath := filepath.Join(*dir, ".env")
			if _, err := os.Stat(envPath); err == nil {
				composeArgs = append(composeArgs, "--env-file", envPath)
			}
			composeArgs = append(composeArgs, args...)

			runtime := runtimeBinary(*dir)
			c := exec.Command(runtime, append([]string{"compose"}, composeArgs...)...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			return c.Run()
		},
	}

	return cmd
}

// runtimeBinary determines the container runtime CLI to use.
// Priority: AGENT_SANDBOX_RUNTIME env var > first agent's runtime_engine > "docker"
func runtimeBinary(dir string) string {
	if rt := os.Getenv("AGENT_SANDBOX_RUNTIME"); rt != "" {
		return rt
	}
	project, err := config.LoadProject(dir)
	if err == nil && len(project.Agents) > 0 && project.Agents[0].Config.RuntimeEngine != "" {
		return project.Agents[0].Config.RuntimeEngineBinary()
	}
	return "docker"
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new agent-sandbox project (interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat("fleet.yaml"); err == nil {
				return fmt.Errorf("fleet.yaml already exists in this directory")
			}

			reader := bufio.NewReader(os.Stdin)

			agentCountStr := prompt(reader, "How many agents? [1]: ")
			agentCount := 1
			if agentCountStr != "" {
				if _, err := fmt.Sscanf(agentCountStr, "%d", &agentCount); err != nil || agentCount < 1 {
					return fmt.Errorf("invalid agent count: %q (must be a positive integer)", agentCountStr)
				}
			}

			rt := selectRuntime(reader)

			// Determine agent names
			var agentNames []string
			if agentCount == 1 {
				dirName := filepath.Base(mustCwd())
				name := prompt(reader, fmt.Sprintf("Agent name [%s]: ", dirName))
				if name == "" {
					name = dirName
				}
				agentNames = []string{name}
			} else {
				for i := 1; i <= agentCount; i++ {
					agentNames = append(agentNames, fmt.Sprintf("agent-%03d", i))
				}
			}

			// Write fleet.yaml
			var fleet strings.Builder
			fleet.WriteString("# yaml-language-server: $schema=.build/fleet-schema.json\n")
			fleet.WriteString("agents:\n")
			for _, name := range agentNames {
				fmt.Fprintf(&fleet, "  - %s\n", name)
			}
			fleet.WriteString("\nshared:\n")
			fleet.WriteString("  gateway:\n")
			fleet.WriteString("    services: []\n")
			fleet.WriteString("  installations: []\n")

			if err := os.WriteFile("fleet.yaml", []byte(fleet.String()), 0644); err != nil {
				return fmt.Errorf("writing fleet.yaml: %w", err)
			}

			// Write per-agent directories
			for _, name := range agentNames {
				if err := os.MkdirAll(name, 0755); err != nil {
					return fmt.Errorf("creating %s/: %w", name, err)
				}

				var agent strings.Builder
				agent.WriteString("# yaml-language-server: $schema=../.build/schema.json\n")
				fmt.Fprintf(&agent, "name: %s\n", name)
				fmt.Fprintf(&agent, "core_version: %s\n", coreVersionForInit())
				agent.WriteString("runtime:\n")
				fmt.Fprintf(&agent, "  image: \"@builtin/%s\"\n", rt)
				agent.WriteString("  entrypoint: [\"sleep\", \"infinity\"]\n")
				agent.WriteString("gateway:\n")
				agent.WriteString("  services: []\n")
				agent.WriteString("installations: []\n")

				agentPath := filepath.Join(name, "agent.yaml")
				if err := os.WriteFile(agentPath, []byte(agent.String()), 0644); err != nil {
					return fmt.Errorf("writing %s: %w", agentPath, err)
				}
			}

			// Write .env.example
			if err := os.WriteFile(".env.example", []byte("# Shared secrets\n"), 0644); err != nil {
				return fmt.Errorf("writing .env.example: %w", err)
			}

			fmt.Printf("\nCreated fleet.yaml with %d agent(s)\n", agentCount)
			for _, name := range agentNames {
				fmt.Printf("  %s/agent.yaml\n", name)
			}
			fmt.Println("\nNext steps:")
			fmt.Println("  1. Add gateway services and plugins")
			fmt.Println("  2. Create .env with your secrets")
			fmt.Println("  3. agent-sandbox generate")
			fmt.Println("  4. agent-sandbox compose up --build -d")
			return nil
		},
	}
}

func selectRuntime(reader *bufio.Reader) string {
	fmt.Println("\nAvailable runtimes:")
	fmt.Println("  1) codex       — OpenAI Codex")
	fmt.Println("  2) claude-code — Anthropic Claude Code")
	fmt.Println("  3) pi          — Pi coding agent")
	choice := prompt(reader, "Runtime [1]: ")
	switch strings.TrimSpace(choice) {
	case "2":
		return "claude-code"
	case "3":
		return "pi"
	default:
		return "codex"
	}
}

func prompt(reader *bufio.Reader, message string) string {
	fmt.Print(message)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func coreVersionForInit() string {
	if version == "dev" {
		return "latest"
	}
	return version
}

func mustCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "agent"
	}
	return dir
}
