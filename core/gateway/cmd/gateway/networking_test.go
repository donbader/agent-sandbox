package main

import (
	"os"
	"strings"
	"testing"
)

// TestGatewayRouteScript_NoIptablesDependency verifies that the gateway-authored
// route script does not require iptables in containers. The gateway handles
// traffic interception via PREROUTING; containers only need 'ip route'.
func TestGatewayRouteScript_NoIptablesDependency(t *testing.T) {
	// Read the script that writeGatewayRouteScript would produce.
	// We test the template by reading the source and extracting the script literal.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	// Find the script template between the markers.
	content := string(src)
	start := strings.Index(content, `script := ` + "`#!/bin/sh")
	if start < 0 {
		t.Fatal("could not find script template in main.go")
	}
	end := strings.Index(content[start:], "fi\n`")
	if end < 0 {
		t.Fatal("could not find end of script template")
	}
	script := content[start : start+end+4]

	// The script MUST NOT require iptables in containers.
	if strings.Contains(script, "command -v iptables") ||
		strings.Contains(script, "iptables -t nat") {
		t.Error("gateway route script should not require iptables in containers; " +
			"traffic interception is handled by gateway PREROUTING")
	}

	// The script MUST set a default route via the gateway.
	if !strings.Contains(script, "ip route") {
		t.Error("gateway route script must set a default route via gateway IP")
	}

	// The script MUST install the CA certificate.
	if !strings.Contains(script, "ca.crt") {
		t.Error("gateway route script must install the gateway CA certificate")
	}

	// The script MUST configure DNS.
	if !strings.Contains(script, "resolv.conf") {
		t.Error("gateway route script must configure DNS to point at gateway")
	}
}

// TestGatewayRouteScript_FallbackNoIptables verifies the fallback script
// (core/scripts/gateway-route.sh) also doesn't require iptables.
func TestGatewayRouteScript_FallbackNoIptables(t *testing.T) {
	script, err := os.ReadFile("../../../scripts/gateway-route.sh")
	if err != nil {
		t.Skipf("fallback script not found: %v", err)
	}
	content := string(script)

	// Must not invoke iptables (comments mentioning it are fine).
	if strings.Contains(content, "iptables -") || strings.Contains(content, "command -v iptables") {
		t.Error("fallback gateway-route.sh should not require iptables; " +
			"traffic interception is handled by gateway PREROUTING")
	}

	if !strings.Contains(content, "ip route") {
		t.Error("fallback script must set default route via gateway")
	}

	if !strings.Contains(content, "ca.crt") {
		t.Error("fallback script must install CA certificate")
	}
}

// TestSetupIptables_Contract documents the gateway-side iptables contract.
// The actual iptables commands can only run inside a privileged container,
// so this test validates the function signature and documents the expected rules.
func TestSetupIptables_Contract(t *testing.T) {
	// Read main.go and verify setupIptables enables forwarding + PREROUTING.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	content := string(src)

	// Must enable IP forwarding.
	if !strings.Contains(content, "/proc/sys/net/ipv4/ip_forward") {
		t.Error("setupIptables must enable IP forwarding via /proc/sys/net/ipv4/ip_forward")
	}

	// Must set PREROUTING REDIRECT for port 443.
	if !strings.Contains(content, "PREROUTING") || !strings.Contains(content, "443") {
		t.Error("setupIptables must REDIRECT forwarded port 443 to proxy")
	}

	// Must redirect to port 8443 (proxy listen port).
	if !strings.Contains(content, "8443") {
		t.Error("setupIptables must redirect to port 8443")
	}
}
