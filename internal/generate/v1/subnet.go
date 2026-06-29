package v1

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
)

// sandboxNet holds the chosen subnet for the sandbox network.
type sandboxNet struct {
	CIDR   string // e.g. "172.30.0.0/24"
	Prefix string // e.g. "172.30.0" — for building static IPs like Prefix + ".2"
}

// findAvailableSubnet probes Docker for existing networks and returns the first
// available 172.X.0.0/24 subnet (starting at X=30, up to X=50).
// Falls back to 172.30.0.0/24 if Docker is unavailable.
func findAvailableSubnet() sandboxNet {
	used := usedSubnets()

	for x := 30; x <= 50; x++ {
		cidr := fmt.Sprintf("172.%d.0.0/24", x)
		_, candidate, _ := net.ParseCIDR(cidr)
		if !overlapsAny(candidate, used) {
			return sandboxNet{
				CIDR:   cidr,
				Prefix: fmt.Sprintf("172.%d.0", x),
			}
		}
	}
	// All taken (unlikely) — fall back to default and let Docker fail with a clear error.
	return sandboxNet{CIDR: "172.30.0.0/24", Prefix: "172.30.0"}
}

// usedSubnets returns all subnets currently allocated by Docker networks.
func usedSubnets() []*net.IPNet {
	out, err := exec.Command("docker", "network", "ls", "--format", "{{.ID}}").Output()
	if err != nil {
		return nil
	}

	ids := splitLines(string(out))
	if len(ids) == 0 {
		return nil
	}

	args := append([]string{"network", "inspect", "--format", "{{json .IPAM.Config}}"}, ids...)
	inspect, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil
	}

	var nets []*net.IPNet
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
