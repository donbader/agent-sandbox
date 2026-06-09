package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
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

	root.AddCommand(generateV1Cmd(&dir))
	root.AddCommand(composeCmd(&dir))
	root.AddCommand(auditCmd(&dir))
	root.AddCommand(initCmd())
	root.AddCommand(upgradeCmd())
	root.AddCommand(gatewayURLCmd(&dir))

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

func gatewayURLCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway-url",
		Short: "Print the gateway's public URL (resolves dynamic port)",
		RunE: func(cmd *cobra.Command, args []string) error {
			composePath := filepath.Join(*dir, ".build", "docker-compose.yml")
			if _, err := os.Stat(composePath); os.IsNotExist(err) {
				return fmt.Errorf("%s not found — run 'agent-sandbox generate' first", composePath)
			}

			absDir, err := filepath.Abs(*dir)
			if err != nil {
				return fmt.Errorf("resolve project dir: %w", err)
			}
			projectName := filepath.Base(absDir)

			// Load config to get the agent name (gateway service = <name>-gateway)
			cfg, err := config.Load(filepath.Join(*dir, "agent.yaml"))
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			gatewayService := cfg.Name + "-gateway"

			runtime := runtimeBinary(*dir)
			c := exec.Command(runtime, "compose",
				"-f", composePath,
				"--project-name", projectName,
				"port", gatewayService, "8080",
			)
			out, err := c.Output()
			if err != nil {
				return fmt.Errorf("gateway not running or port not exposed — is 'agent-sandbox compose up' running?")
			}

			hostPort := strings.TrimSpace(string(out))
			if hostPort == "" {
				return fmt.Errorf("could not resolve gateway port")
			}

			// docker compose port returns "0.0.0.0:PORT" or ":::PORT"
			// Normalize to localhost
			hostPort = strings.Replace(hostPort, "0.0.0.0:", "localhost:", 1)
			if strings.HasPrefix(hostPort, ":::") {
				hostPort = "localhost:" + strings.TrimPrefix(hostPort, ":::")
			}

			fmt.Printf("http://%s\n", hostPort)
			return nil
		},
	}
	return cmd
}

// runtimeBinary determines the container runtime CLI to use.
// Priority: AGENT_SANDBOX_RUNTIME env var > agent.yaml runtime_engine > "docker"
func runtimeBinary(dir string) string {
	if rt := os.Getenv("AGENT_SANDBOX_RUNTIME"); rt != "" {
		return rt
	}
	// Try to load config for runtime_engine setting
	cfg, err := loadConfigSafe(dir)
	if err == nil && cfg.RuntimeEngine != "" {
		return cfg.RuntimeEngineBinary()
	}
	return "docker"
}

// loadConfigSafe attempts to load config without failing fatally.
func loadConfigSafe(dir string) (*config.Config, error) {
	return config.Load(dir)
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new agent-sandbox project (interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if agent.yaml or fleet.yaml already exists
			if _, err := os.Stat("agent.yaml"); err == nil {
				return fmt.Errorf("agent.yaml already exists in this directory")
			}
			if _, err := os.Stat("fleet.yaml"); err == nil {
				return fmt.Errorf("fleet.yaml already exists in this directory")
			}

			reader := bufio.NewReader(os.Stdin)

			// Fleet mode selection
			agentCountStr := prompt(reader, "How many agents? [1]: ")
			agentCount := 1
			if agentCountStr != "" {
				if _, err := fmt.Sscanf(agentCountStr, "%d", &agentCount); err != nil || agentCount < 1 {
					return fmt.Errorf("invalid agent count: %q (must be a positive integer)", agentCountStr)
				}
			}

			if agentCount == 1 {
				return initSingleAgent(reader)
			}
			return initFleet(reader, agentCount)
		},
	}
}

func initSingleAgent(reader *bufio.Reader) error {
	dirName := filepath.Base(mustCwd())
	name := prompt(reader, fmt.Sprintf("Agent name [%s]: ", dirName))
	if name == "" {
		name = dirName
	}

	rt := selectRuntime(reader)

	var b strings.Builder
	b.WriteString("# yaml-language-server: $schema=.build/schema.json\n")
	_, _ = fmt.Fprintf(&b, "name: %s\n", name)
	b.WriteString("core_version: latest\n")
	b.WriteString("runtime:\n")
	_, _ = fmt.Fprintf(&b, "  image: \"@builtin/%s\"\n", rt)
	b.WriteString("  entrypoint: [\"sleep\", \"infinity\"]\n")
	b.WriteString("gateway:\n")
	b.WriteString("  services: []\n")
	b.WriteString("installations: []\n")

	if err := os.WriteFile("agent.yaml", []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("writing agent.yaml: %w", err)
	}

	fmt.Println("\nCreated agent.yaml")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Add gateway services and plugins to agent.yaml")
	fmt.Println("  2. Create .env with your secrets")
	fmt.Println("  3. agent-sandbox generate")
	fmt.Println("  4. agent-sandbox compose up --build -d")
	return nil
}

func initFleet(reader *bufio.Reader, count int) error {
	rt := selectRuntime(reader)

	// Generate fleet.yaml
	var fleet strings.Builder
	fleet.WriteString("# yaml-language-server: $schema=.build/fleet-schema.json\n")
	fleet.WriteString("agents:\n")
	for i := 1; i <= count; i++ {
		_, _ = fmt.Fprintf(&fleet, "  - agent-%03d\n", i)
	}
	fleet.WriteString("\nshared:\n")
	fleet.WriteString("  gateway:\n")
	fleet.WriteString("    services: []\n")
	fleet.WriteString("  installations: []\n")

	if err := os.WriteFile("fleet.yaml", []byte(fleet.String()), 0644); err != nil {
		return fmt.Errorf("writing fleet.yaml: %w", err)
	}

	// Generate per-agent directories
	for i := 1; i <= count; i++ {
		agentName := fmt.Sprintf("agent-%03d", i)
		if err := os.MkdirAll(agentName, 0755); err != nil {
			return fmt.Errorf("creating %s/: %w", agentName, err)
		}

		var agent strings.Builder
		agent.WriteString("# yaml-language-server: $schema=../.build/schema.json\n")
		_, _ = fmt.Fprintf(&agent, "name: %s\n", agentName)
		agent.WriteString("core_version: latest\n")
		agent.WriteString("runtime:\n")
		_, _ = fmt.Fprintf(&agent, "  image: \"@builtin/%s\"\n", rt)
		agent.WriteString("installations: []\n")

		agentPath := filepath.Join(agentName, "agent.yaml")
		if err := os.WriteFile(agentPath, []byte(agent.String()), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", agentPath, err)
		}
	}

	// Generate .env.example
	if err := os.WriteFile(".env.example", []byte("# Shared secrets\n"), 0644); err != nil {
		return fmt.Errorf("writing .env.example: %w", err)
	}

	fmt.Printf("\nCreated fleet.yaml with %d agents\n", count)
	for i := 1; i <= count; i++ {
		fmt.Printf("  agent-%03d/agent.yaml\n", i)
	}
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Add shared gateway services and plugins to fleet.yaml")
	fmt.Println("  2. Customize per-agent config in each agent-NNN/agent.yaml")
	fmt.Println("  3. Create .env with your secrets")
	fmt.Println("  4. agent-sandbox generate")
	fmt.Println("  5. agent-sandbox compose up --build -d")
	return nil
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

func mustCwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "agent"
	}
	return dir
}

const upgradeRepo = "donbader/agent-sandbox"

func upgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Self-update to the latest release",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Fetch latest release tag from GitHub
			latest, err := fetchLatestVersion()
			if err != nil {
				return fmt.Errorf("checking for updates: %w", err)
			}

			current := version
			if current == latest || "v"+current == latest {
				fmt.Printf("Already up to date (%s)\n", current)
				return nil
			}

			fmt.Printf("Current: %s → Latest: %s\n", current, latest)

			// Determine platform
			goos := runtime.GOOS
			goarch := runtime.GOARCH

			// Download new binary
			filename := fmt.Sprintf("agent-sandbox_%s_%s_%s.tar.gz", strings.TrimPrefix(latest, "v"), goos, goarch)
			url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", upgradeRepo, latest, filename)

			fmt.Printf("Downloading %s...\n", filename)
			tmpDir, err := os.MkdirTemp("", "agent-sandbox-upgrade-*")
			if err != nil {
				return fmt.Errorf("creating temp dir: %w", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			tarPath := filepath.Join(tmpDir, filename)
			if err := downloadFile(url, tarPath); err != nil {
				return fmt.Errorf("downloading release: %w", err)
			}

			// Extract
			extractCmd := exec.Command("tar", "xzf", tarPath, "-C", tmpDir)
			if err := extractCmd.Run(); err != nil {
				return fmt.Errorf("extracting archive: %w", err)
			}

			// Find current binary path
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("finding current binary: %w", err)
			}
			execPath, err = filepath.EvalSymlinks(execPath)
			if err != nil {
				return fmt.Errorf("resolving binary path: %w", err)
			}

			// Replace binary
			newBinary := filepath.Join(tmpDir, "agent-sandbox")
			if err := os.Rename(newBinary, execPath); err != nil {
				// Try with sudo if permission denied
				if os.IsPermission(err) {
					fmt.Println("Requires elevated permissions...")
					sudoCmd := exec.Command("sudo", "mv", newBinary, execPath)
					sudoCmd.Stdin = os.Stdin
					sudoCmd.Stdout = os.Stdout
					sudoCmd.Stderr = os.Stderr
					if err := sudoCmd.Run(); err != nil {
						return fmt.Errorf("installing binary: %w", err)
					}
				} else {
					return fmt.Errorf("replacing binary: %w", err)
				}
			}

			fmt.Printf("Upgraded to %s\n", latest)
			return nil
		},
	}
}

func fetchLatestVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", upgradeRepo)
	resp, err := http.Get(url) //nolint:gosec // URL is constructed from a constant
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec // URL constructed from known release format
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(f, resp.Body)
	return err
}
