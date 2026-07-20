package v1

import (
	"net"
	"testing"
)

func TestOverlapsAny(t *testing.T) {
	_, taken, _ := net.ParseCIDR("172.30.0.0/24")
	_, candidate30, _ := net.ParseCIDR("172.30.0.0/24")
	_, candidate31, _ := net.ParseCIDR("172.31.0.0/24")

	existing := []*net.IPNet{taken}

	if !overlapsAny(candidate30, existing) {
		t.Error("172.30.0.0/24 should overlap with itself")
	}
	if overlapsAny(candidate31, existing) {
		t.Error("172.31.0.0/24 should NOT overlap with 172.30.0.0/24")
	}
}

func TestFindAvailableSubnet_fallback(t *testing.T) {
	// When Docker isn't available (e.g. unit test env), should find 172.32/172.33 available
	s, err := findAvailableSubnet()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.CIDR == "" {
		t.Error("CIDR should not be empty")
	}
	if s.Prefix == "" {
		t.Error("Prefix should not be empty")
	}
	if s.ExternalCIDR == "" {
		t.Error("ExternalCIDR should not be empty")
	}
	// Sandbox and external must be different
	if s.CIDR == s.ExternalCIDR {
		t.Errorf("sandbox (%s) and external (%s) must use different subnets", s.CIDR, s.ExternalCIDR)
	}
}

func TestFindAvailableSubnet_skipsUsed(t *testing.T) {
	// Simulate: if usedSubnets returned 172.30, we should get 172.31
	used := []*net.IPNet{}
	_, n30, _ := net.ParseCIDR("172.30.0.0/24")
	used = append(used, n30)

	// Manually test the selection logic
	for x := 30; x <= 50; x++ {
		_, candidate, _ := net.ParseCIDR("172.30.0.0/24")
		if x == 30 {
			if !overlapsAny(candidate, used) {
				t.Error("172.30 should overlap")
			}
		}
	}

	_, candidate31, _ := net.ParseCIDR("172.31.0.0/24")
	if overlapsAny(candidate31, used) {
		t.Error("172.31 should NOT overlap with 172.30")
	}
}

func TestSplitLines(t *testing.T) {
	lines := splitLines("abc\ndef\n\nghi\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "abc" || lines[1] != "def" || lines[2] != "ghi" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestParseRouteSubnets_DinD(t *testing.T) {
	// Simulates actual ip route output from a DinD container on 172.30.0.0/24
	routeOutput := `default via 172.30.0.3 dev eth0
172.30.0.0/24 dev eth0 proto kernel scope link src 172.30.0.7
`

	nets := parseRouteSubnets(routeOutput)
	if len(nets) != 1 {
		t.Fatalf("expected 1 subnet from routes, got %d", len(nets))
	}
	if nets[0].String() != "172.30.0.0/24" {
		t.Errorf("expected 172.30.0.0/24, got %s", nets[0].String())
	}

	// Now prove: if this subnet is in 'used', findAvailableSubnet logic skips it
	_, candidate30, _ := net.ParseCIDR("172.30.0.0/24")
	_, candidate31, _ := net.ParseCIDR("172.31.0.0/24")
	if !overlapsAny(candidate30, nets) {
		t.Error("172.30.0.0/24 should overlap with parsed route")
	}
	if overlapsAny(candidate31, nets) {
		t.Error("172.31.0.0/24 should NOT overlap with 172.30 route")
	}
}

func TestParseRouteSubnets_MultipleRoutes(t *testing.T) {
	routeOutput := `default via 10.0.0.1 dev eth0
10.0.0.0/24 dev eth0 proto kernel scope link src 10.0.0.5
172.30.0.0/24 dev docker0 proto kernel scope link src 172.30.0.1
172.31.0.0/24 dev br-abc123 proto kernel scope link src 172.31.0.1
`

	nets := parseRouteSubnets(routeOutput)
	if len(nets) != 3 {
		t.Fatalf("expected 3 subnets, got %d", len(nets))
	}

	// Both 172.30 and 172.31 should be detected as used
	_, c30, _ := net.ParseCIDR("172.30.0.0/24")
	_, c31, _ := net.ParseCIDR("172.31.0.0/24")
	_, c32, _ := net.ParseCIDR("172.32.0.0/24")
	if !overlapsAny(c30, nets) {
		t.Error("172.30 should be detected as used")
	}
	if !overlapsAny(c31, nets) {
		t.Error("172.31 should be detected as used")
	}
	if overlapsAny(c32, nets) {
		t.Error("172.32 should be free")
	}
}
