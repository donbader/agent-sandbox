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
	"sort"
	"strings"

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
	root.AddCommand(validateCmd(&dir))
	root.AddCommand(auditCmd(&dir))
	root.AddCommand(pluginsCmd())
	root.AddCommand(initCmd())
	root.AddCommand(upgradeCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func generateCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate .build/ artifacts from agent config",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Detect fleet vs single-agent mode
			if config.HasFleetConfig(*dir) {
				return generateFleet(*dir)
			}
			return generateSingle(*dir)
		},
	}

	return cmd
}

func generateSingle(dir string) error {
	outDir := filepath.Join(dir, ".build")

	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	if err := generateAgent(dir, outDir, cfg, nil, false); err != nil {
		return err
	}

	// Ensure schema comment in agent.yaml
	if err := ensureSchemaComment(filepath.Join(dir, "agent.yaml"), ".build/schema.json"); err != nil {
		return err
	}

	fmt.Printf("Generated artifacts in %s\n", outDir)
	return nil
}

func generateFleet(dir string) error {
	fleet, err := config.LoadFleet(dir)
	if err != nil {
		return err
	}

	outDir := filepath.Join(dir, ".build")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// Generate each agent into its own subdirectory, collecting env vars for fleet .env.example
	var agentNames []string
	seen := map[string]bool{}
	var allEnvVars []string
	for _, agentName := range fleet.Agents {
		agentDir := filepath.Join(dir, agentName)
		agentOutDir := filepath.Join(outDir, agentName)

		cfg, err := config.Load(agentDir)
		if err != nil {
			return fmt.Errorf("agent %q: %w", agentName, err)
		}

		// Merge shared features
		cfg.Features = config.MergeSharedFeatures(fleet.Shared.Features, cfg.Features)

		// Collect env vars from this agent's merged config
		for _, v := range generate.ScanConfigEnvVars(cfg.Features) {
			if !seen[v] {
				seen[v] = true
				allEnvVars = append(allEnvVars, v)
			}
		}

		if err := generateAgent(agentDir, agentOutDir, cfg, &fleet.Shared, true); err != nil {
			return fmt.Errorf("agent %q: %w", agentName, err)
		}

		// Ensure schema comment in sub-agent's agent.yaml
		schemaRel := filepath.Join("..", ".build", agentName, "schema.json")
		if err := ensureSchemaComment(filepath.Join(agentDir, "agent.yaml"), schemaRel); err != nil {
			return fmt.Errorf("agent %q: schema comment: %w", agentName, err)
		}

		agentNames = append(agentNames, agentName)
	}

	// Generate combined docker-compose.yml
	if err := writeFleetCompose(outDir, agentNames); err != nil {
		return err
	}

	// Sort env vars for stable output
	sort.Strings(allEnvVars)

	if err := writeFleetEnvExample(dir, allEnvVars); err != nil {
		return err
	}

	// Generate fleet-level schema for fleet.yaml
	if err := generate.WriteFleetSchema(outDir); err != nil {
		return err
	}

	// Ensure schema comment in fleet.yaml
	if err := ensureSchemaComment(filepath.Join(dir, "fleet.yaml"), ".build/fleet-schema.json"); err != nil {
		return err
	}

	fmt.Printf("Generated fleet artifacts in %s (%d agents)\n", outDir, len(agentNames))
	return nil
}

func generateAgent(dir, outDir string, cfg *config.AgentConfig, _ *config.SharedBlock, skipEnvExample bool) error {
	// Resolve runtime
	rt, err := resolve.ResolveRuntime(dir, cfg.Runtime)
	if err != nil {
		return fmt.Errorf("resolving runtime %q: %w", cfg.Runtime, err)
	}

	// Resolve features
	var features []*resolve.FeatureContributions
	hasChannelManager := false
	for i, entry := range cfg.Features {
		instanceName := fmt.Sprintf("features[%d]", i)
		if entry.Name != "" {
			instanceName = entry.Name
		}
		contrib, err := resolve.ResolveFeature(dir, entry.Plugin, instanceName, entry.Config)
		if err != nil {
			return fmt.Errorf("resolving feature %q (plugin %q): %w", instanceName, entry.Plugin, err)
		}
		features = append(features, contrib)
		if contrib.ChannelName != "" {
			hasChannelManager = true
		}
	}

	g := &generate.Generator{
		Config:         cfg,
		Runtime:        rt,
		Features:       features,
		Gateway:        cfg.GatewayEnabled(),
		ChannelManager: hasChannelManager,
		SkipEnvExample: skipEnvExample,
		GatewaySpec: generate.GatewaySpec{
			BuildImage: "golang:1.26.4-alpine",
			BinaryPath: "/gateway",
			ListenPort: 8443,
			DNSPort:    53,
		},
		ChannelManagerSpec: generate.ChannelManagerSpec{
			BuildImage: "node:22-slim",
			InstallCmd: "npm install",
			BuildCmd:   "npm run build",
			DistDir:    "/src/dist",
			EntryPoint: "node /opt/channel-manager/dist/index.js",
		},
		Dir:    dir,
		OutDir: outDir,
	}

	return g.Run()
}

func writeFleetCompose(outDir string, agents []string) error {
	var b strings.Builder
	b.WriteString("# Generated by agent-sandbox (fleet mode)\n")
	b.WriteString("# Do not edit — regenerate with: agent-sandbox generate\n\n")

	b.WriteString("include:\n")
	for _, name := range agents {
		_, _ = fmt.Fprintf(&b, "  - path: %s/docker-compose.yml\n", name)
	}

	composePath := filepath.Join(outDir, "docker-compose.yml")
	return os.WriteFile(composePath, []byte(b.String()), 0644)
}

// writeFleetEnvExample generates a single .env.example at the fleet root
// containing all env vars from all agents.
func writeFleetEnvExample(dir string, envVars []string) error {
	if len(envVars) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("# Environment variables for agent-sandbox fleet\n")
	b.WriteString("# Copy to .env and fill in values\n\n")
	for _, v := range envVars {
		_, _ = fmt.Fprintf(&b, "%s=\n", v)
	}

	path := filepath.Join(dir, ".env.example")
	return os.WriteFile(path, []byte(b.String()), 0644)
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

func validateCmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate agent.yaml config without generating artifacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(*dir)
			if err != nil {
				return err
			}

			// Resolve runtime
			_, err = resolve.ResolveRuntime(*dir, cfg.Runtime)
			if err != nil {
				return fmt.Errorf("runtime: %w", err)
			}

			// Resolve each feature
			for i, entry := range cfg.Features {
				instanceName := fmt.Sprintf("features[%d]", i)
				if entry.Name != "" {
					instanceName = entry.Name
				}
				_, err := resolve.ResolveFeature(*dir, entry.Plugin, instanceName, entry.Config)
				if err != nil {
					return fmt.Errorf("feature %q (plugin %q): %w", instanceName, entry.Plugin, err)
				}
			}

			// Check for .env file if config references env vars
			envPath := filepath.Join(*dir, ".env")
			if _, err := os.Stat(envPath); os.IsNotExist(err) {
				// Only warn — it's not an error to not have .env during validation
				fmt.Fprintf(os.Stderr, "Warning: no .env file found at %s\n", envPath)
			}

			fmt.Println("✓ Config is valid")
			return nil
		},
	}
}

func pluginsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "List available plugins",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Runtime plugins:")
			fmt.Println("  codex        — OpenAI Codex agent (node:22-slim)")
			fmt.Println("  claude-code  — Anthropic Claude Code agent (node:22-slim)")
			fmt.Println("  pi           — Pi coding agent (node:22-slim)")
			fmt.Println()
			fmt.Println("Feature plugins:")
			for name, plugin := range resolve.RegisteredPlugins() {
				desc := describePlugin(name, plugin)
				fmt.Printf("  %-14s — %s\n", name, desc)
			}
		},
	}
}

func describePlugin(name string, plugin resolve.FeaturePlugin) string {
	descriptions := map[string]string{
		"custom-runtime":    "Custom packages, startup hooks, persistent volumes",
		"telegram":          "Telegram bot channel via gateway MITM",
		"github-pat":        "GitHub PAT injection via gateway MITM",
		"external-services": "Connect to external services with optional header injection",
		"claude-code":       "Anthropic Claude Code runtime configuration",
		"pi":                "Pi coding agent runtime configuration",
		"mcp-oauth":         "OAuth token injection for remote MCP servers",
	}
	if desc, ok := descriptions[name]; ok {
		return desc
	}
	return "Feature plugin"
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
			runtime := "codex"
			switch strings.TrimSpace(runtimeChoice) {
			case "2":
				runtime = "claude-code"
			case "3":
				runtime = "pi"
			}

			// Features
			fmt.Println("\nOptional features (comma-separated numbers, or blank for none):")
			fmt.Println("  1) github-pat      — GitHub PAT credential injection")
			fmt.Println("  2) telegram        — Telegram bot channel")
			fmt.Println("  3) custom-runtime  — Custom packages, hooks, volumes")
			featureChoice := prompt(reader, "Features []: ")

			var features []string
			var envVars []string
			for _, ch := range strings.Split(featureChoice, ",") {
				switch strings.TrimSpace(ch) {
				case "1":
					features = append(features, "github-pat")
					envVars = append(envVars, "GITHUB_PAT")
				case "2":
					features = append(features, "telegram")
					envVars = append(envVars, "TELEGRAM_BOT_TOKEN")
				case "3":
					features = append(features, "custom-runtime")
				}
			}

			// Generate agent.yaml
			var b strings.Builder
			b.WriteString("# yaml-language-server: $schema=.build/schema.json\n")
			_, _ = fmt.Fprintf(&b, "name: %s\n", name)
			_, _ = fmt.Fprintf(&b, "runtime: %s\n", runtime)

			if len(features) > 0 {
				b.WriteString("\nfeatures:\n")
				for _, f := range features {
					switch f {
					case "github-pat":
						b.WriteString("  - plugin: github-pat\n")
						b.WriteString("    token: \"${GITHUB_PAT}\"\n")
					case "telegram":
						username := prompt(reader, "Telegram username (with @): ")
						if username == "" {
							username = "@your_username"
						}
					b.WriteString("  - plugin: telegram\n")
					b.WriteString("    access_control:\n")
					_, _ = fmt.Fprintf(&b, "      allowed_users: [\"%s\"]\n", username)
				case "custom-runtime":
					b.WriteString("  - plugin: custom-runtime\n")
					b.WriteString("    commands:\n")
					b.WriteString("      - \"apt-get update && apt-get install -y --no-install-recommends ripgrep && rm -rf /var/lib/apt/lists/*\"\n")
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
	resp, err := http.Get(url)
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
	resp, err := http.Get(url)
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
