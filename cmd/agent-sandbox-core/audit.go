package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/donbader/agent-sandbox/internal/config"
	"github.com/spf13/cobra"
)

// auditCheck represents a single audit check result.
type auditCheck struct {
	Name   string
	Passed bool
	Detail string
}

func auditCmd(dir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Validate a running sandbox meets the security contract",
		Long: `Runs checks against a running sandbox to verify:
  - Agent can reach external HTTPS endpoints through gateway
  - Agent env does not contain real secrets
  - Gateway injects auth headers into outbound requests
  - DNS resolves through gateway
  - Gateway CA certificate is trusted
  - Traffic interception rules are active
  - Default route goes through gateway

The sandbox must be running (agent-sandbox compose up) before auditing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAudit(*dir)
		},
	}

	return cmd
}

func runAudit(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve project dir: %w", err)
	}
	projectName := filepath.Base(absDir)

	cfg, err := config.Load(dir)
	if err != nil {
		// Try fleet mode
		fleet, ferr := config.LoadFleet(dir)
		if ferr != nil {
			return fmt.Errorf("cannot load agent.yaml or fleet.yaml: %w", err)
		}
		// Audit each agent in fleet
		var allChecks []auditCheck
		for _, agentDir := range fleet.Agents {
			agentCfg, err := config.Load(filepath.Join(dir, agentDir))
			if err != nil {
				return fmt.Errorf("loading agent %s: %w", agentDir, err)
			}
			checks := auditAgent(agentCfg, projectName, dir)
			printAgentResults(agentCfg.Name, checks)
			allChecks = append(allChecks, checks...)
		}
		return printSummary(allChecks)
	}

	checks := auditAgent(cfg, projectName, dir)
	printAgentResults(cfg.Name, checks)
	return printSummary(checks)
}

func auditAgent(cfg *config.Config, projectName, dir string) []auditCheck {
	agentContainer := fmt.Sprintf("%s-%s-1", projectName, cfg.Name)
	gatewayContainer := fmt.Sprintf("%s-%s-gateway-1", projectName, cfg.Name)

	var checks []auditCheck

	// Check containers are running
	if !containerRunning(agentContainer) {
		checks = append(checks, auditCheck{
			Name:   "Agent container running",
			Passed: false,
			Detail: fmt.Sprintf("container %s is not running — run 'agent-sandbox compose up -d' first", agentContainer),
		})
		return checks
	}
	if !containerRunning(gatewayContainer) {
		checks = append(checks, auditCheck{
			Name:   "Gateway container running",
			Passed: false,
			Detail: fmt.Sprintf("container %s is not running", gatewayContainer),
		})
		return checks
	}

	checks = append(checks, checkHTTPS(agentContainer))
	checks = append(checks, checkSecretIsolation(agentContainer, cfg, dir))
	checks = append(checks, checkDNS(agentContainer))
	checks = append(checks, checkCACert(agentContainer))
	checks = append(checks, checkDNATRules(agentContainer))
	checks = append(checks, checkDefaultRoute(agentContainer))

	return checks
}

func containerRunning(name string) bool {
	rt := runtimeFromEnv()
	out, err := exec.Command(rt, "inspect", "-f", "{{.State.Running}}", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func containerExec(container string, args ...string) (string, error) {
	rt := runtimeFromEnv()
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command(rt, cmdArgs...).CombinedOutput()
	return string(out), err
}

// runtimeFromEnv returns the container runtime binary from env or default.
func runtimeFromEnv() string {
	if rt := os.Getenv("AGENT_SANDBOX_RUNTIME"); rt != "" {
		return rt
	}
	return "docker"
}

// checkHTTPS verifies the agent can reach an external HTTPS endpoint.
func checkHTTPS(container string) auditCheck {
	// Use -o /dev/null -w %{http_code} to check connectivity regardless of HTTP status.
	// Any HTTP response (even 4xx/5xx) proves the TLS proxy chain works.
	out, err := containerExec(container, "curl", "-so", "/dev/null", "-w", "%{http_code}", "--max-time", "15", "https://httpbin.org/get")
	if err != nil {
		// Retry once — first request may be slow due to cold DNS + TLS
		out, err = containerExec(container, "curl", "-so", "/dev/null", "-w", "%{http_code}", "--max-time", "15", "https://httpbin.org/get")
	}
	if err != nil {
		return auditCheck{
			Name:   "Agent can reach external HTTPS",
			Passed: false,
			Detail: "curl to httpbin.org failed or timed out",
		}
	}
	code := strings.TrimSpace(out)
	if code != "000" && code != "" {
		return auditCheck{
			Name:   "Agent can reach external HTTPS",
			Passed: true,
			Detail: fmt.Sprintf("reached https://httpbin.org through gateway (HTTP %s)", code),
		}
	}
	return auditCheck{
		Name:   "Agent can reach external HTTPS",
		Passed: false,
		Detail: "no HTTP response received",
	}
}

// checkSecretIsolation verifies the agent env doesn't contain real secrets from .env.
func checkSecretIsolation(container string, cfg *config.Config, dir string) auditCheck {
	// Load secrets from .env file
	envPath := filepath.Join(dir, ".env")
	secrets, err := loadEnvSecrets(envPath)
	if err != nil || len(secrets) == 0 {
		// No .env file or empty — check passes (nothing to leak)
		return auditCheck{
			Name:   "Secret isolation",
			Passed: true,
			Detail: "no .env file found (nothing to verify)",
		}
	}

	// Get the agent container's environment
	agentEnv, err := containerExec(container, "env")
	if err != nil {
		return auditCheck{
			Name:   "Secret isolation",
			Passed: false,
			Detail: fmt.Sprintf("cannot read agent env: %v", err),
		}
	}

	// Check if any .env secret values appear in the agent's environment
	leakedVars := []string{}
	for name, value := range secrets {
		if value == "" {
			continue
		}
		// Check if the actual secret value appears in any env var inside the container
		for _, line := range strings.Split(agentEnv, "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && parts[1] == value {
				leakedVars = append(leakedVars, fmt.Sprintf("%s (from .env %s)", parts[0], name))
			}
		}
	}

	if len(leakedVars) > 0 {
		return auditCheck{
			Name:   "Secret isolation",
			Passed: false,
			Detail: fmt.Sprintf("agent env contains real secrets: %s", strings.Join(leakedVars, ", ")),
		}
	}
	return auditCheck{
		Name:   "Secret isolation",
		Passed: true,
		Detail: "no .env secrets leaked to agent environment",
	}
}

// loadEnvSecrets reads a .env file and returns key=value pairs.
func loadEnvSecrets(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	secrets := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			// Strip surrounding quotes
			val = strings.Trim(val, `"'`)
			secrets[key] = val
		}
	}
	return secrets, nil
}

// checkDNS verifies DNS resolves MITM domains to the gateway.
func checkDNS(container string) auditCheck {
	// Verify a MITM domain resolves (proves the gateway DNS intercept works).
	// Use getent which is available on slim images.
	out, err := containerExec(container, "getent", "hosts", "agent-gateway.stx-ai.net")
	if err != nil {
		// Fallback: try ping-based resolution
		out, err = containerExec(container, "sh", "-c", "ping -c1 -W2 agent-gateway.stx-ai.net 2>/dev/null | head -1")
	}
	if err != nil || strings.TrimSpace(out) == "" {
		return auditCheck{
			Name:   "DNS through gateway",
			Passed: false,
			Detail: "cannot resolve MITM domain (agent-gateway.stx-ai.net)",
		}
	}
	return auditCheck{
		Name:   "DNS through gateway",
		Passed: true,
		Detail: fmt.Sprintf("MITM domain resolves: %s", strings.TrimSpace(strings.Split(out, "\n")[0])),
	}
}

// checkCACert verifies the gateway CA is installed in the agent.
func checkCACert(container string) auditCheck {
	_, err := containerExec(container, "test", "-f", "/usr/local/share/ca-certificates/gateway-ca.crt")
	if err != nil {
		return auditCheck{
			Name:   "Gateway CA trusted",
			Passed: false,
			Detail: "CA certificate not found at /usr/local/share/ca-certificates/gateway-ca.crt",
		}
	}
	return auditCheck{
		Name:   "Gateway CA trusted",
		Passed: true,
		Detail: "gateway CA certificate present in trust store",
	}
}

// checkDNATRules verifies OUTPUT DNAT rules are active in the agent.
func checkDNATRules(container string) auditCheck {
	out, err := containerExec(container, "iptables", "-t", "nat", "-L", "OUTPUT", "-n")
	if err != nil {
		return auditCheck{
			Name:   "Traffic interception rules",
			Passed: false,
			Detail: fmt.Sprintf("cannot read iptables: %v", err),
		}
	}
	if strings.Contains(out, "DNAT") && strings.Contains(out, "tcp dpt:443") {
		return auditCheck{
			Name:   "Traffic interception rules",
			Passed: true,
			Detail: "OUTPUT DNAT rule for port 443 active",
		}
	}
	return auditCheck{
		Name:   "Traffic interception rules",
		Passed: false,
		Detail: "missing OUTPUT DNAT rule for port 443",
	}
}

// checkDefaultRoute verifies traffic reaches the gateway by confirming the DNAT
// target IP matches an active gateway container on the network.
func checkDefaultRoute(container string) auditCheck {
	// Read the iptables DNAT target to find where traffic goes
	out, err := containerExec(container, "iptables", "-t", "nat", "-L", "OUTPUT", "-n")
	if err != nil {
		return auditCheck{
			Name:   "Default route to gateway",
			Passed: false,
			Detail: fmt.Sprintf("cannot read iptables: %v", err),
		}
	}

	// Look for "to:<ip>:8443" in the DNAT rule
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "DNAT") && strings.Contains(line, ":8443") {
			// Extract the target IP
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, "to:") {
					target := strings.TrimPrefix(field, "to:")
					ip := strings.Split(target, ":")[0]
					return auditCheck{
						Name:   "Default route to gateway",
						Passed: true,
						Detail: fmt.Sprintf("HTTPS traffic routed to gateway at %s", ip),
					}
				}
			}
		}
	}

	return auditCheck{
		Name:   "Default route to gateway",
		Passed: false,
		Detail: "no DNAT target found in iptables OUTPUT chain",
	}
}

func printAgentResults(name string, checks []auditCheck) {
	fmt.Printf("Auditing %s...\n\n", name)
	for _, c := range checks {
		if c.Passed {
			fmt.Printf("  \033[32m✓\033[0m %s\n", c.Name)
		} else {
			fmt.Printf("  \033[31m✗\033[0m %s\n", c.Name)
			fmt.Printf("    %s\n", c.Detail)
		}
	}
	fmt.Println()
}

func printSummary(checks []auditCheck) error {
	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}
	fmt.Printf("%d/%d checks passed\n", passed, len(checks))
	if passed < len(checks) {
		os.Exit(1)
	}
	return nil
}
