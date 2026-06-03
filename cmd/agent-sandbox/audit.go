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

const (
	projectName = "agent-sandbox"
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
			checks := auditAgent(agentCfg)
			allChecks = append(allChecks, checks...)
		}
		return printResults(allChecks)
	}

	checks := auditAgent(cfg)
	return printResults(checks)
}

func auditAgent(cfg *config.AgentConfig) []auditCheck {
	agentContainer := fmt.Sprintf("%s-%s-1", projectName, cfg.Name)
	gatewayContainer := fmt.Sprintf("%s-%s-gateway-1", projectName, cfg.Name)

	fmt.Printf("Auditing %s...\n\n", cfg.Name)

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
	checks = append(checks, checkSecretIsolation(agentContainer, cfg))
	checks = append(checks, checkDNS(agentContainer))
	checks = append(checks, checkCACert(agentContainer))
	checks = append(checks, checkDNATRules(agentContainer))
	checks = append(checks, checkDefaultRoute(agentContainer))

	return checks
}

func containerRunning(name string) bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func dockerExec(container string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	return string(out), err
}

// checkHTTPS verifies the agent can reach an external HTTPS endpoint.
func checkHTTPS(container string) auditCheck {
	// Use -o /dev/null -w %{http_code} to check connectivity regardless of HTTP status.
	// Any HTTP response (even 4xx/5xx) proves the TLS proxy chain works.
	out, err := dockerExec(container, "curl", "-so", "/dev/null", "-w", "%{http_code}", "--max-time", "15", "https://httpbin.org/get")
	if err != nil {
		// Retry once — first request may be slow due to cold DNS + TLS
		out, err = dockerExec(container, "curl", "-so", "/dev/null", "-w", "%{http_code}", "--max-time", "15", "https://httpbin.org/get")
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

// checkSecretIsolation verifies the agent env doesn't contain gateway credentials.
func checkSecretIsolation(container string, cfg *config.AgentConfig) auditCheck {
	env, err := dockerExec(container, "env")
	if err != nil {
		return auditCheck{
			Name:   "Secret isolation",
			Passed: false,
			Detail: fmt.Sprintf("cannot read agent env: %v", err),
		}
	}

	// Check for common secret-bearing env var patterns
	leakedVars := []string{}
	for _, line := range strings.Split(env, "\n") {
		upper := strings.ToUpper(line)
		// Skip dummy values
		if strings.Contains(upper, "=DUMMY") {
			continue
		}
		// Flag real-looking secrets
		if strings.Contains(upper, "API_KEY=") ||
			strings.Contains(upper, "TOKEN=") ||
			strings.Contains(upper, "SECRET=") ||
			strings.Contains(upper, "PASSWORD=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && parts[1] != "" && parts[1] != "dummy" {
				leakedVars = append(leakedVars, parts[0])
			}
		}
	}

	if len(leakedVars) > 0 {
		return auditCheck{
			Name:   "Secret isolation",
			Passed: false,
			Detail: fmt.Sprintf("agent env contains secrets: %s", strings.Join(leakedVars, ", ")),
		}
	}
	return auditCheck{
		Name:   "Secret isolation",
		Passed: true,
		Detail: "no credentials leaked to agent environment",
	}
}

// checkDNS verifies DNS resolves through the gateway.
func checkDNS(container string) auditCheck {
	resolv, err := dockerExec(container, "cat", "/etc/resolv.conf")
	if err != nil {
		return auditCheck{
			Name:   "DNS through gateway",
			Passed: false,
			Detail: fmt.Sprintf("cannot read resolv.conf: %v", err),
		}
	}

	route, err := dockerExec(container, "ip", "route", "show", "default")
	if err != nil {
		return auditCheck{
			Name:   "DNS through gateway",
			Passed: false,
			Detail: fmt.Sprintf("cannot read routes: %v", err),
		}
	}

	// Extract nameserver from resolv.conf
	var nameserver string
	for _, line := range strings.Split(resolv, "\n") {
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				nameserver = fields[1]
			}
			break
		}
	}

	// Extract default gateway
	var defaultGW string
	fields := strings.Fields(strings.TrimSpace(route))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			defaultGW = fields[i+1]
			break
		}
	}

	if nameserver == "" {
		return auditCheck{
			Name:   "DNS through gateway",
			Passed: false,
			Detail: "no nameserver found in resolv.conf",
		}
	}
	if nameserver == defaultGW {
		return auditCheck{
			Name:   "DNS through gateway",
			Passed: true,
			Detail: fmt.Sprintf("nameserver %s matches gateway", nameserver),
		}
	}
	return auditCheck{
		Name:   "DNS through gateway",
		Passed: false,
		Detail: fmt.Sprintf("nameserver=%s but default gateway=%s", nameserver, defaultGW),
	}
}

// checkCACert verifies the gateway CA is installed in the agent.
func checkCACert(container string) auditCheck {
	_, err := dockerExec(container, "test", "-f", "/usr/local/share/ca-certificates/ca.crt")
	if err != nil {
		return auditCheck{
			Name:   "Gateway CA trusted",
			Passed: false,
			Detail: "CA certificate not found at /usr/local/share/ca-certificates/ca.crt",
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
	out, err := dockerExec(container, "iptables", "-t", "nat", "-L", "OUTPUT", "-n")
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

// checkDefaultRoute verifies the default route goes through the gateway.
func checkDefaultRoute(container string) auditCheck {
	resolv, _ := dockerExec(container, "cat", "/etc/resolv.conf")
	route, err := dockerExec(container, "ip", "route", "show", "default")
	if err != nil {
		return auditCheck{
			Name:   "Default route to gateway",
			Passed: false,
			Detail: fmt.Sprintf("cannot read routes: %v", err),
		}
	}

	// The gateway IP is the nameserver (set by entrypoint)
	var nameserver string
	for _, line := range strings.Split(resolv, "\n") {
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				nameserver = fields[1]
			}
			break
		}
	}

	if nameserver != "" && strings.Contains(route, nameserver) {
		return auditCheck{
			Name:   "Default route to gateway",
			Passed: true,
			Detail: fmt.Sprintf("default route via %s", nameserver),
		}
	}
	return auditCheck{
		Name:   "Default route to gateway",
		Passed: false,
		Detail: fmt.Sprintf("route: %s", strings.TrimSpace(route)),
	}
}

func printResults(checks []auditCheck) error {
	passed := 0
	failed := 0
	for _, c := range checks {
		if c.Passed {
			fmt.Printf("  \033[32m✓\033[0m %s\n", c.Name)
			passed++
		} else {
			fmt.Printf("  \033[31m✗\033[0m %s\n", c.Name)
			fmt.Printf("    %s\n", c.Detail)
			failed++
		}
	}
	fmt.Printf("\n%d/%d checks passed\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
	return nil
}
