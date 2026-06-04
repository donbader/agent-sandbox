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
		Short:              "Docker compose passthrough (auto-injects -f .build/docker-compose.yml)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			composePath := filepath.Join(*dir, ".build", "docker-compose.yml")
			if _, err := os.Stat(composePath); os.IsNotExist(err) {
				return fmt.Errorf("%s not found — run 'agent-sandbox generate' first", composePath)
			}

			composeArgs := []string{"-f", composePath, "--project-name", "agent-sandbox"}
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

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new agent-sandbox project (interactive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if agent.yaml already exists
			if _, err := os.Stat("agent.yaml"); err == nil {
				return fmt.Errorf("agent.yaml already exists in this directory")
			}

			reader := bufio.NewReader(os.Stdin)

			// Agent name
			dirName := filepath.Base(mustCwd())
			name := prompt(reader, fmt.Sprintf("Agent name [%s]: ", dirName))
			if name == "" {
				name = dirName
			}

			// Runtime selection
			fmt.Println("\nAvailable runtimes:")
			fmt.Println("  1) codex       — OpenAI Codex")
			fmt.Println("  2) claude-code — Anthropic Claude Code")
			fmt.Println("  3) pi          — Pi coding agent")
			runtimeChoice := prompt(reader, "Runtime [1]: ")
			rt := "codex"
			switch strings.TrimSpace(runtimeChoice) {
			case "2":
				rt = "claude-code"
			case "3":
				rt = "pi"
			}

			// Features
			fmt.Println("\nOptional features (comma-separated numbers, or blank for none):")
			fmt.Println("  1) github-pat      — GitHub PAT credential injection")
			fmt.Println("  2) custom-runtime  — Custom packages, hooks, volumes")
			featureChoice := prompt(reader, "Features []: ")

			var features []string
			var envVars []string
			for _, ch := range strings.Split(featureChoice, ",") {
				switch strings.TrimSpace(ch) {
				case "1":
					features = append(features, "github-pat")
					envVars = append(envVars, "GITHUB_PAT")
				case "2":
					features = append(features, "custom-runtime")
				}
			}

			// Generate agent.yaml
			var b strings.Builder
			b.WriteString("# yaml-language-server: $schema=.build/schema.json\n")
			_, _ = fmt.Fprintf(&b, "name: %s\n", name)
			_, _ = fmt.Fprintf(&b, "runtime: %s\n", rt)

			if len(features) > 0 {
				b.WriteString("\nplugins:\n")
				for _, f := range features {
					switch f {
					case "github-pat":
						b.WriteString("  - plugin: github-pat\n")
						b.WriteString("    options:\n")
						b.WriteString("      token: \"${GITHUB_PAT}\"\n")
					case "custom-runtime":
						b.WriteString("  - plugin: custom-runtime\n")
						b.WriteString("    options:\n")
						b.WriteString("      commands:\n")
						b.WriteString("        - \"apt-get update && apt-get install -y --no-install-recommends ripgrep && rm -rf /var/lib/apt/lists/*\"\n")
					}
				}
			}

			if err := os.WriteFile("agent.yaml", []byte(b.String()), 0644); err != nil {
				return fmt.Errorf("writing agent.yaml: %w", err)
			}

			// Generate .env if there are env vars
			if len(envVars) > 0 {
				var env strings.Builder
				for _, v := range envVars {
					_, _ = fmt.Fprintf(&env, "%s=\n", v)
				}
				if err := os.WriteFile(".env", []byte(env.String()), 0644); err != nil {
					return fmt.Errorf("writing .env: %w", err)
				}
				fmt.Println("\nCreated .env — fill in your secrets")
			}

			fmt.Println("Created agent.yaml")
			fmt.Println("\nNext steps:")
			fmt.Println("  agent-sandbox generate")
			fmt.Println("  agent-sandbox compose up --build -d")
			return nil
		},
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
