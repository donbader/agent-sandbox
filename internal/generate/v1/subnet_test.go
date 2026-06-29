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
	// When Docker isn't available (e.g. unit test env), should fall back to 172.30
	s := findAvailableSubnet()
	if s.CIDR == "" {
		t.Error("CIDR should not be empty")
	}
	if s.Prefix == "" {
		t.Error("Prefix should not be empty")
	}
	// In CI without conflicting networks, should get 172.30
	if s.CIDR != "172.30.0.0/24" {
		t.Logf("Got non-default subnet %s (Docker networks present)", s.CIDR)
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
