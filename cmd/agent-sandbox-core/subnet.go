package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// validateAndFixSubnets reads the compose YAML, tries to create Docker networks
// with the specified subnets, and if they fail (due to IPAM conflicts), patches
// the YAML with subnets that actually work. This is the proper runtime fix for
// DinD environments where static guesses can't cover all IPAM configurations.
func validateAndFixSubnets(composePath string, runtime string) error {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return err
	}

	content := string(data)

	// Find all subnet declarations in the YAML
	re := regexp.MustCompile(`subnet:\s*([\d.]+/\d+)`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil // No subnets specified, nothing to validate
	}

	// Check each subnet by actually trying to create a Docker network
	allValid := true
	for _, m := range matches {
		subnet := m[1]
		if !trySubnet(subnet, runtime) {
			allValid = false
			break
		}
	}

	if allValid {
		return nil // All subnets are available, proceed
	}

	// Find working subnets by probing Docker
	// Need len(matches) consecutive subnets that all work
	needed := len(matches)
	workingSubnets, err := findWorkingSubnets(needed, runtime)
	if err != nil {
		return fmt.Errorf("could not find %d available subnets: %w", needed, err)
	}

	// Patch the YAML with working subnets
	// Also need to patch any static IPs that reference the old subnet prefix
	newContent := content
	for i, m := range matches {
		oldSubnet := m[1]
		newSubnet := workingSubnets[i]
		newContent = strings.Replace(newContent, "subnet: "+oldSubnet, "subnet: "+newSubnet, 1)

		// Patch static IPs: extract prefix from old and new subnets
		oldPrefix := subnetPrefix(oldSubnet)
		newPrefix := subnetPrefix(newSubnet)
		if oldPrefix != "" && newPrefix != "" && oldPrefix != newPrefix {
			newContent = strings.ReplaceAll(newContent, "ipv4_address: "+oldPrefix+".", "ipv4_address: "+newPrefix+".")
		}
	}

	return os.WriteFile(composePath, []byte(newContent), 0644)
}

// trySubnet attempts to create a Docker network with the given subnet.
// Returns true if the subnet is available, false otherwise.
func trySubnet(subnet string, runtime string) bool {
	name := "agent-sandbox-probe-" + strings.ReplaceAll(subnet, "/", "-")
	name = strings.ReplaceAll(name, ".", "-")

	cmd := exec.Command(runtime, "network", "create", "--subnet", subnet, name)
	if err := cmd.Run(); err != nil {
		return false
	}

	// Clean up probe network
	cleanup := exec.Command(runtime, "network", "rm", name)
	_ = cleanup.Run()
	return true
}

// findWorkingSubnets finds N consecutive available subnets by probing Docker.
// Tries 172.32.0.0/24 through 172.63.0.0/24, then 10.200.0.0/24 through 10.220.0.0/24.
func findWorkingSubnets(n int, runtime string) ([]string, error) {
	candidates := []string{}

	// Primary range: 172.32-63 (outside Docker's default 172.17.0.0/12 pool)
	for x := 32; x <= 63; x++ {
		candidates = append(candidates, fmt.Sprintf("172.%d.0.0/24", x))
	}
	// Fallback range: 10.200-220
	for x := 200; x <= 220; x++ {
		candidates = append(candidates, fmt.Sprintf("10.%d.0.0/24", x))
	}

	// Find N consecutive working subnets
	for i := 0; i <= len(candidates)-n; i++ {
		allWork := true
		for j := 0; j < n; j++ {
			if !trySubnet(candidates[i+j], runtime) {
				allWork = false
				// Skip to after the failing one
				i = i + j
				break
			}
		}
		if allWork {
			return candidates[i : i+n], nil
		}
	}

	return nil, fmt.Errorf("exhausted all candidate subnets")
}

// subnetPrefix extracts the first 3 octets from a CIDR (e.g. "172.32.0.0/24" → "172.32.0")
func subnetPrefix(cidr string) string {
	parts := strings.Split(cidr, "/")
	if len(parts) == 0 {
		return ""
	}
	octets := strings.Split(parts[0], ".")
	if len(octets) < 3 {
		return ""
	}
	return strings.Join(octets[:3], ".")
}
