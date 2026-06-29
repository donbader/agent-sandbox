package v1

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// sandboxNet holds the chosen subnets for compose networks.
type sandboxNet struct {
	CIDR       string // sandbox network, e.g. "172.30.0.0/24"
	Prefix     string // for static IPs, e.g. "172.30.0"
	ExternalCIDR string // external network, e.g. "172.31.0.0/24"
}

// findAvailableSubnet probes Docker for existing networks and returns two
// consecutive available 172.X.0.0/24 subnets (starting at X=30, up to X=50).
// One for the sandbox (internal) network, one for the external network.
// Falls back to 172.30/172.31 if Docker is unavailable.
func findAvailableSubnet() sandboxNet {
	used := usedSubnets()

	for x := 30; x <= 49; x++ {
		cidr1 := fmt.Sprintf("172.%d.0.0/24", x)
		cidr2 := fmt.Sprintf("172.%d.0.0/24", x+1)
		_, c1, _ := net.ParseCIDR(cidr1)
		_, c2, _ := net.ParseCIDR(cidr2)
		if !overlapsAny(c1, used) && !overlapsAny(c2, used) {
			return sandboxNet{
				CIDR:         cidr1,
				Prefix:       fmt.Sprintf("172.%d.0", x),
				ExternalCIDR: cidr2,
			}
		}
	}
	return sandboxNet{CIDR: "172.30.0.0/24", Prefix: "172.30.0", ExternalCIDR: "172.31.0.0/24"}
}

// usedSubnets returns all subnets currently allocated by Docker networks
// AND subnets in use by the host's network interfaces/routes.
func usedSubnets() []*net.IPNet {
	var nets []*net.IPNet

	// 1. Check Docker networks
	out, err := exec.Command("docker", "network", "ls", "--format", "{{.ID}}").Output()
	if err == nil {
		ids := splitLines(string(out))
		if len(ids) > 0 {
			args := append([]string{"network", "inspect", "--format", "{{json .IPAM.Config}}"}, ids...)
			inspect, err := exec.Command("docker", args...).Output()
			if err == nil {
				for _, line := range splitLines(string(inspect)) {
					if line == "" || line == "null" {
						continue
					}
					var configs []struct {
						Subnet string `json:"Subnet"`
					}
					if err := json.Unmarshal([]byte(line), &configs); err != nil {
						continue
					}
					for _, c := range configs {
						if c.Subnet == "" {
							continue
						}
						_, n, err := net.ParseCIDR(c.Subnet)
						if err == nil {
							nets = append(nets, n)
						}
					}
				}
			}
		}
	}

	// 2. Check system routes (catches host interface subnets in DinD)
	routeOut, err := exec.Command("ip", "route").Output()
	if err == nil {
		nets = append(nets, parseRouteSubnets(string(routeOut))...)
	}

	return nets
}

// overlapsAny checks if candidate overlaps with any existing network.
func overlapsAny(candidate *net.IPNet, existing []*net.IPNet) bool {
	for _, e := range existing {
		if candidate.Contains(e.IP) || e.Contains(candidate.IP) {
			return true
		}
	}
	return false
}

// splitLines splits output by newline, filtering empty strings.
func splitLines(s string) []string {
	var out []string
	for _, line := range split(s, '\n') {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func split(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

// splitFields splits a string by whitespace (like strings.Fields).
func splitFields(s string) []string {
	return strings.Fields(s)
}

// parseRouteSubnets extracts CIDR subnets from `ip route` output.
func parseRouteSubnets(output string) []*net.IPNet {
	var nets []*net.IPNet
	for _, line := range splitLines(output) {
		fields := splitFields(line)
		if len(fields) > 0 && strings.Contains(fields[0], "/") {
			_, n, err := net.ParseCIDR(fields[0])
			if err == nil {
				nets = append(nets, n)
			}
		}
	}
	return nets
}
