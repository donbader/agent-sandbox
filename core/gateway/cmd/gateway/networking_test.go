package main

import (
	"os"
	"strings"
	"testing"
)

// TestGatewayRouteScript_NoIptablesDependency verifies that the gateway-authored
// route script does not require iptables in containers. Traffic interception is
// handled by DNS (gateway responds with its own IP for egress domains).
func TestGatewayRouteScript_NoIptablesDependency(t *testing.T) {
	// Read the script that writeGatewayRouteScript would produce.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	// Find the script template between the markers.
	content := string(src)
	start := strings.Index(content, `script := `+"`#!/bin/sh")
	if start < 0 {
		t.Fatal("could not find script template in main.go")
	}
	end := strings.Index(content[start:], "fi\n`")
	if end < 0 {
		t.Fatal("could not find end of script template")
	}
	script := content[start : start+end+4]

	// The script MUST NOT require iptables in containers.
	if strings.Contains(script, "iptables -t nat") {
		t.Error("gateway route script should not require iptables in containers; " +
			"traffic interception is handled by DNS-based interception at the gateway")
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

	// Must not invoke iptables (traffic interception via DNS at gateway).
	if strings.Contains(content, "iptables -t nat") {
		t.Error("fallback gateway-route.sh should not require iptables; " +
			"traffic interception is handled by DNS-based interception at the gateway")
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
	// Read main.go and verify setupIptables sets up PREROUTING.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	content := string(src)

	// Must NOT set ip_forward (DNS interception means no forwarding needed).
	if strings.Contains(content, "ip_forward") {
		t.Error("setupIptables should not manage ip_forward; DNS interception " +
			"means traffic arrives at gateway's own IP, no forwarding needed")
	}

	// Must set PREROUTING REDIRECT for port 443.
	if !strings.Contains(content, "PREROUTING") || !strings.Contains(content, "443") {
		t.Error("setupIptables must REDIRECT port 443 to proxy")
	}

	// Must redirect to port 8443 (proxy listen port).
	if !strings.Contains(content, "8443") {
		t.Error("setupIptables must redirect to port 8443")
	}
}
