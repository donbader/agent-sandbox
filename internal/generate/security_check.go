package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SecurityCheck represents a single security verification.
type SecurityCheck struct {
	Name   string
	Passed bool
	Detail string
}

// validateOutput reads generated files and verifies security invariants.
// Only runs in gateway mode (single-container mode has no security boundary).
// Always prints what was checked. Returns error on first critical violation.
func (g *Generator) validateOutput() error {
	if !g.Gateway {
		return nil
	}

	fmt.Println("Security checks (gateway mode):")

	// Read generated files
	composePath := filepath.Join(g.OutDir, "docker-compose.yml")
	composeData, err := os.ReadFile(composePath)
	if err != nil {
		return fmt.Errorf("security check: cannot read docker-compose.yml: %w", err)
	}
	compose := string(composeData)

	entrypointPath := filepath.Join(g.OutDir, "entrypoint.sh")
	entrypointData, err := os.ReadFile(entrypointPath)
	if err != nil {
		return fmt.Errorf("security check: cannot read entrypoint.sh: %w", err)
	}
	entrypoint := string(entrypointData)

	// Run all checks
	var checks []SecurityCheck
	checks = append(checks, g.checkAgentNetworkIsolation(compose))
	checks = append(checks, g.checkSecretIsolation(compose))
	checks = append(checks, g.checkGatewayCredentials(compose))
	checks = append(checks, g.checkRouteEnforcement(entrypoint))
	if g.hasMITMDomains() {
		checks = append(checks, g.checkCACertReadOnly(compose))
	}
	checks = append(checks, g.checkNoPrivileged(compose))

	// Print results and fail on first violation
	for _, check := range checks {
		if check.Passed {
			fmt.Printf("  ✓ %s: %s\n", check.Name, check.Detail)
		} else {
			fmt.Printf("  ✗ %s: %s\n", check.Name, check.Detail)
			return fmt.Errorf("security check failed: %s: %s", check.Name, check.Detail)
		}
	}

	return nil
}

// checkAgentNetworkIsolation verifies the agent service is on the internal network only.
func (g *Generator) checkAgentNetworkIsolation(compose string) SecurityCheck {
	agentSection := extractServiceSection(compose, g.Config.Name)

	hasInternal := strings.Contains(agentSection, "internal:")
	hasDefault := strings.Contains(agentSection, "default:")

	if hasInternal && !hasDefault {
		return SecurityCheck{
			Name:   "Agent network isolation",
			Passed: true,
			Detail: "agent service on internal network only",
		}
	}
	return SecurityCheck{
		Name:   "Agent network isolation",
		Passed: false,
		Detail: "agent service must be on internal network only (no default/internet)",
	}
}

// checkSecretIsolation verifies no ${VAR} credential patterns in the agent environment.
func (g *Generator) checkSecretIsolation(compose string) SecurityCheck {
	agentSection := extractServiceSection(compose, g.Config.Name)
	agentEnv := extractEnvironmentSection(agentSection)

	matches := envVarPattern.FindAllStringSubmatch(agentEnv, -1)
	if len(matches) > 0 {
		// Collect the leaked variable names
		var leaked []string
		for _, m := range matches {
			leaked = append(leaked, "${"+m[1]+"}")
		}
		return SecurityCheck{
			Name:   "Secret isolation",
			Passed: false,
			Detail: fmt.Sprintf("agent environment contains %s", strings.Join(leaked, ", ")),
		}
	}
	return SecurityCheck{
		Name:   "Secret isolation",
		Passed: true,
		Detail: "no credentials in agent environment",
	}
}

// checkGatewayCredentials verifies the gateway service has ${VAR} patterns (holds secrets).
func (g *Generator) checkGatewayCredentials(compose string) SecurityCheck {
	gatewayName := g.Config.Name + "-gateway"
	gatewaySection := extractServiceSection(compose, gatewayName)
	gatewayEnv := extractEnvironmentSection(gatewaySection)

	matches := envVarPattern.FindAllStringSubmatch(gatewayEnv, -1)
	if len(matches) > 0 {
		return SecurityCheck{
			Name:   "Gateway credentials",
			Passed: true,
			Detail: fmt.Sprintf("gateway service has %d credential(s)", len(matches)),
		}
	}
	// If no env vars are configured at all, that's fine — not every setup has secrets
	if len(g.mergedEnvVars()) == 0 {
		return SecurityCheck{
			Name:   "Gateway credentials",
			Passed: true,
			Detail: "no credentials configured (none expected)",
		}
	}
	return SecurityCheck{
		Name:   "Gateway credentials",
		Passed: false,
		Detail: "gateway service should hold credentials but has none",
	}
}

// checkRouteEnforcement verifies the agent entrypoint sets default route via gateway.
func (g *Generator) checkRouteEnforcement(entrypoint string) SecurityCheck {
	if strings.Contains(entrypoint, "ip route replace default via") {
		return SecurityCheck{
			Name:   "Route enforcement",
			Passed: true,
			Detail: "agent entrypoint sets default route via gateway",
		}
	}
	return SecurityCheck{
		Name:   "Route enforcement",
		Passed: false,
		Detail: "entrypoint.sh must contain 'ip route replace default via' for traffic routing",
	}
}

// checkCACertReadOnly verifies the shared-certs volume is mounted read-only in the agent.
func (g *Generator) checkCACertReadOnly(compose string) SecurityCheck {
	agentSection := extractServiceSection(compose, g.Config.Name)

	// Look for shared-certs mount with :ro suffix
	if regexp.MustCompile(`shared-certs:[^:\s]+:ro`).MatchString(agentSection) {
		return SecurityCheck{
			Name:   "CA cert read-only",
			Passed: true,
			Detail: "shared-certs mounted read-only in agent",
		}
	}
	return SecurityCheck{
		Name:   "CA cert read-only",
		Passed: false,
		Detail: "shared-certs must be mounted read-only (:ro) in agent service",
	}
}

// checkNoPrivileged verifies no service runs in privileged mode.
func (g *Generator) checkNoPrivileged(compose string) SecurityCheck {
	if strings.Contains(compose, "privileged: true") {
		return SecurityCheck{
			Name:   "No privileged mode",
			Passed: false,
			Detail: "compose file contains 'privileged: true'",
		}
	}
	return SecurityCheck{
		Name:   "No privileged mode",
		Passed: true,
		Detail: "no privileged containers",
	}
}

// extractServiceSection extracts the YAML block for a named service from compose content.
// It finds the service header (e.g., "  myservice:") and returns everything until the next
// top-level key (services, networks, volumes) or another service at the same indent level.
func extractServiceSection(compose, serviceName string) string {
	lines := strings.Split(compose, "\n")

	// Find the service header line: "  <name>:" with exactly 2-space indent under services
	startIdx := -1
	serviceIndent := -1
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		// Match "  servicename:" — service names are indented under "services:"
		if strings.TrimSpace(trimmed) == serviceName+":" && indent > 0 {
			startIdx = i + 1
			serviceIndent = indent
			break
		}
	}

	if startIdx == -1 {
		return ""
	}

	// Collect lines until we hit another key at the same or lower indent level
	var section strings.Builder
	for i := startIdx; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			section.WriteString(line + "\n")
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent <= serviceIndent {
			break
		}
		section.WriteString(line + "\n")
	}

	return section.String()
}

// extractEnvironmentSection extracts the environment block from a service section.
func extractEnvironmentSection(serviceSection string) string {
	lines := strings.Split(serviceSection, "\n")

	startIdx := -1
	envIndent := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "environment:" {
			startIdx = i + 1
			envIndent = len(line) - len(strings.TrimLeft(line, " "))
			break
		}
	}

	if startIdx == -1 {
		return ""
	}

	var section strings.Builder
	for i := startIdx; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent <= envIndent {
			break
		}
		section.WriteString(line + "\n")
	}

	return section.String()
}
